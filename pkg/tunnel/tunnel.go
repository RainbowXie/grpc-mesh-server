package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
)

// Server accepts TLS connections and manages yamux sessions for mesh nodes.
//
// The Server handles the lifecycle of incoming TLS+Yamux connections from mesh nodes:
//   - Accepts TLS connections on the configured address
//   - Performs Yamux session multiplexing over each connection
//   - Receives and validates handshake messages on the control stream
//   - Registers authenticated sessions in the registry
//   - Automatically reloads TLS certificates when they change on disk
//
// Each connected node maintains a long-lived Yamux session through which multiple
// streams can be opened for control and data plane operations.
//
// Example usage:
//
//	srv, err := tunnel.New(
//	    ":8443",
//	    "server.crt",
//	    "server.key",
//	    "ca.crt",
//	    authPolicy,
//	    registry,
//	    logger,
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := srv.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer srv.Stop()
type Server struct {
	addr     string
	certFile string
	keyFile  string
	caFile   string

	listener   net.Listener
	tlsConfig  *tls.Config
	authPolicy *control.AuthPolicy
	registry   *registry.SessionManager

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	logger *zap.Logger
}

// New creates a new tunnel server that listens for TLS+Yamux connections.
//
// Parameters:
//   - addr: TCP address to listen on (e.g., ":8443" or "0.0.0.0:8443")
//   - certFile: Path to TLS certificate file (PEM format)
//   - keyFile: Path to TLS private key file (PEM format)
//   - caFile: Path to CA certificate file for client verification (optional)
//   - authPolicy: Policy for authenticating handshake tokens
//   - reg: Session registry for tracking connected nodes
//   - logger: Zap logger instance for structured logging
//
// The server will load the TLS certificate immediately and set up file watching
// to automatically reload the certificate when it changes on disk (if certFile
// and keyFile are provided).
//
// If certFile and keyFile are empty strings, the server will use a minimal
// TLS configuration suitable only for development (not recommended for production).
//
// Returns an error if:
//   - TLS certificate loading fails
//   - Certificate files are invalid or inaccessible
//
// The server is not started automatically; call Start() to begin accepting connections.
func New(
	addr string,
	certFile, keyFile, caFile string,
	authPolicy *control.AuthPolicy,
	reg *registry.SessionManager,
	logger *zap.Logger,
) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())

	srv := &Server{
		addr:       addr,
		certFile:   certFile,
		keyFile:    keyFile,
		caFile:     caFile,
		authPolicy: authPolicy,
		registry:   reg,
		ctx:        ctx,
		cancel:     cancel,
		logger:     logger,
	}

	// Load TLS config
	if err := srv.loadTLSConfig(); err != nil {
		return nil, err
	}

	// Setup cert reloader if files provided
	if certFile != "" && keyFile != "" {
		go srv.watchCerts()
	}

	return srv, nil
}

// Start starts the tunnel listener and begins accepting connections.
//
// This method:
//  1. Creates a TLS listener on the configured address
//  2. Spawns an accept loop in a background goroutine
//  3. Returns immediately after successful startup
//
// The accept loop will continue running until Stop() is called or a fatal
// error occurs. Each accepted connection is handled in its own goroutine.
//
// Returns an error if:
//   - The TCP port is already in use
//   - TLS listener creation fails
//   - Network configuration is invalid
//
// Example:
//
//	if err := srv.Start(); err != nil {
//	    log.Fatalf("Failed to start tunnel: %v", err)
//	}
//	log.Println("Tunnel listener started")
func (s *Server) Start() error {
	tlsListener, err := tls.Listen("tcp", s.addr, s.tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to start TLS listener: %w", err)
	}

	s.listener = tlsListener
	s.logger.Info("tunnel listener started", zap.String("addr", s.addr))

	s.wg.Add(1)
	go s.acceptLoop()

	return nil
}

// Stop gracefully stops the tunnel server and cleans up resources.
//
// This method:
//  1. Cancels the server context, signaling all goroutines to exit
//  2. Closes the TLS listener, stopping new connections
//  3. Waits for all active connection handlers to complete
//
// All active Yamux sessions will be closed gracefully, and the certificate
// watcher (if enabled) will be stopped.
//
// This method blocks until all goroutines have exited. It is safe to call
// Stop() multiple times; subsequent calls are no-ops.
//
// Example:
//
//	srv.Stop()
//	log.Println("Tunnel server stopped")
func (s *Server) Stop() error {
	s.cancel()

	if s.listener != nil {
		s.listener.Close()
	}

	s.wg.Wait()
	s.logger.Info("tunnel server stopped")
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				s.logger.Error("accept error", zap.Error(err))
				time.Sleep(time.Second)
				continue
			}
		}

		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	// Force handshake here so handshake failures get explicit, structured logs.
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			fields := append(s.connectionFields(conn), zap.Error(err))
			s.logger.Warn("tls handshake failed", fields...)
			return
		}
	}

	s.logger.Debug("accepted connection", s.connectionFields(conn)...)

	// Create yamux session
	yamuxCfg := yamux.DefaultConfig()
	session, err := yamux.Server(conn, yamuxCfg)
	if err != nil {
		fields := append(s.connectionFields(conn), zap.Error(err))
		s.logger.Error("failed to create yamux session", fields...)
		return
	}
	defer session.Close()

	// Accept control stream
	controlStream, err := session.AcceptStream()
	if err != nil {
		fields := append(s.connectionFields(conn), zap.Error(err))
		s.logger.Error("failed to accept control stream", fields...)
		return
	}

	cs := control.NewControlStream(controlStream)
	defer cs.Close()

	// Receive handshake
	handshake, err := cs.ReceiveHandshake()
	if err != nil {
		s.logger.Error("handshake failed", zap.Error(err))
		return
	}

	// Authorize
	if err := s.authPolicy.Authorize(handshake.Token); err != nil {
		s.logger.Warn("authorization failed",
			zap.String("node_id", handshake.NodeID),
			zap.Error(err))
		return
	}

	peerID := registry.PeerID(handshake.NodeID)

	// Register session
	state, err := s.registry.RegisterSession(peerID, handshake, session)
	if err != nil {
		s.logger.Error("failed to register session", zap.Error(err))
		return
	}

	s.logger.Info("session established",
		zap.String("peer_id", string(peerID)),
		zap.String("version", handshake.Version))

	// Consume control frames until the session ends. Without this loop the
	// node's heartbeat writes exhaust the yamux stream window (~256 KiB) and
	// its heartbeat task freezes; the registry's staleness clock also never
	// advances, so cleanup would evict healthy sessions.
	readDone := make(chan error, 1)
	go s.readControlLoop(peerID, state, cs, readDone)

	select {
	case <-s.ctx.Done():
	case <-session.CloseChan():
		s.logger.Info("session closed", zap.String("peer_id", string(peerID)))
	case err := <-readDone:
		if errors.Is(err, io.EOF) {
			s.logger.Info("node closed control stream", zap.String("peer_id", string(peerID)))
		} else {
			s.logger.Warn("control stream read failed",
				zap.String("peer_id", string(peerID)),
				zap.Error(err))
		}
	}

	// Unblock the read loop and release the stream before deregistering.
	cs.Close()
	session.Close()
	<-readDone

	// Identity-conditional: a replacement under the same peerId must survive
	// this handler exiting after RegisterSession already closed the old yamux.
	s.registry.RemoveIfCurrent(peerID, state)
}

// controlReadIdleTimeout bounds how long the read loop may sit without any
// frame. It matches the registry cleanup's staleness window: a control
// stream silent for this long is a dead node by definition.
const controlReadIdleTimeout = 2 * time.Minute

// readControlLoop consumes frames from the session's control stream until
// the stream errors, reaches EOF, or goes silent past the read deadline.
// Heartbeats refresh the session's staleness clock; other message types are
// logged (there is no consumer wired for them yet).
func (s *Server) readControlLoop(
	peerID registry.PeerID,
	state *registry.SessionState,
	cs *control.ControlStream,
	done chan<- error,
) {
	cs.SetDeadline(controlReadIdleTimeout)

	for {
		msg, err := cs.ReceiveControlMessageRaw()
		if err != nil {
			done <- err
			return
		}

		switch msg.Type {
		case "heartbeat":
			hb := msg.Heartbeat
			if hb == nil {
				s.logger.Warn("heartbeat message without payload",
					zap.String("peer_id", string(peerID)))
				continue
			}
			if hb.NodeID != string(peerID) {
				s.logger.Warn("heartbeat node_id mismatch",
					zap.String("expected", string(peerID)),
					zap.String("got", hb.NodeID))
				continue
			}
			// Frame arrival itself proves liveness inside an authenticated
			// session; timestamp anomalies are only logged, never fatal.
			if err := hb.Validate(); err != nil {
				s.logger.Debug("heartbeat timestamp anomaly",
					zap.Uint64("sequence", hb.Sequence),
					zap.Error(err))
			}
			state.TouchHeartbeat()
			s.logger.Debug("heartbeat received",
				zap.Uint64("sequence", hb.Sequence))
		default:
			s.logger.Debug("control message received",
				zap.String("type", msg.Type),
				zap.String("peer_id", string(peerID)))
		}
	}
}

func (s *Server) loadTLSConfig() error {
	if s.certFile == "" || s.keyFile == "" {
		// Use self-signed cert for development
		s.logger.Warn("no TLS cert provided, using insecure config")
		s.tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		return nil
	}

	cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	s.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	s.tlsConfig.GetConfigForClient = func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
		remote := ""
		if chi.Conn != nil && chi.Conn.RemoteAddr() != nil {
			remote = chi.Conn.RemoteAddr().String()
		}
		s.logger.Debug("tls client hello",
			zap.String("remote", remote),
			zap.String("server_name", chi.ServerName),
			zap.Int("cipher_suite_count", len(chi.CipherSuites)),
			zap.Strings("supported_versions", tlsVersionNames(chi.SupportedVersions)),
			zap.Strings("supported_protos", chi.SupportedProtos))
		return nil, nil
	}

	s.logger.Info("loaded TLS certificate",
		zap.String("cert", s.certFile),
		zap.String("key", s.keyFile))

	return nil
}

func (s *Server) connectionFields(conn net.Conn) []zap.Field {
	remote := ""
	local := ""
	if conn.RemoteAddr() != nil {
		remote = conn.RemoteAddr().String()
	}
	if conn.LocalAddr() != nil {
		local = conn.LocalAddr().String()
	}

	fields := []zap.Field{
		zap.String("remote", remote),
		zap.String("local", local),
	}

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return fields
	}

	st := tlsConn.ConnectionState()
	fields = append(fields,
		zap.Bool("handshake_complete", st.HandshakeComplete),
		zap.String("sni", st.ServerName),
		zap.String("tls_version", tlsVersionName(st.Version)),
		zap.String("cipher_suite", tls.CipherSuiteName(st.CipherSuite)),
		zap.Int("peer_cert_count", len(st.PeerCertificates)))

	return fields
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS1.3"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS10:
		return "TLS1.0"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}

func tlsVersionNames(vs []uint16) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, tlsVersionName(v))
	}
	return out
}

func (s *Server) watchCerts() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.logger.Error("failed to create cert watcher", zap.Error(err))
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.certFile); err != nil {
		s.logger.Error("failed to watch cert file", zap.Error(err))
		return
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				s.logger.Info("cert file changed, reloading")
				if err := s.loadTLSConfig(); err != nil {
					s.logger.Error("failed to reload cert", zap.Error(err))
				}
			}
		case err := <-watcher.Errors:
			s.logger.Error("cert watcher error", zap.Error(err))
		}
	}
}

// NewCertReloader creates a TLS configuration by loading certificates from disk.
//
// This is a legacy compatibility function that creates a simple TLS config
// without automatic reloading. New code should use the Server's built-in
// certificate management via New() instead.
//
// Parameters:
//   - certFile: Path to TLS certificate file (PEM format)
//   - keyFile: Path to TLS private key file (PEM format)
//   - logger: Zap logger for error reporting
//
// Returns a tls.Config configured with:
//   - The loaded certificate
//   - TLS 1.2 as minimum version
//
// Returns an error if the certificate files cannot be loaded or are invalid.
//
// Deprecated: Use Server.New() for automatic certificate reloading.
func NewCertReloader(certFile, keyFile string, logger *zap.Logger) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
