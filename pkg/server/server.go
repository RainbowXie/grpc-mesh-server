package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/config"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/logging"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/metrics"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/reverse"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/tunnel"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// Server is the main gRPC-Mesh server that coordinates tunnel connections,
// session management, and reverse RPC invocations.
//
// The server listens for TLS+Yamux connections from mesh nodes, manages their
// lifecycle, and provides both control-plane operations (registration, heartbeat)
// and data-plane operations (reverse RPC invocation via the InvokePlane service).
//
// Example usage:
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	srv, err := server.New(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	if err := srv.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer srv.Stop()
//
//	// Block until interrupted
//	<-ctx.Done()
type Server struct {
	cfg *config.Config

	ctx    context.Context
	cancel context.CancelFunc

	registry       *registry.SessionManager
	tunnelServer   *tunnel.Server
	grpcServer     *grpc.Server
	metricsServer  *metrics.Exporter
	reverseGateway *reverse.Gateway
	invokeProxy    *InvokeProxy

	logger *zap.Logger
}

// New creates a new gRPC-Mesh server instance with the provided configuration.
//
// This function initializes all server components including:
//   - Logging subsystem
//   - Session registry for tracking connected nodes
//   - TLS tunnel listener for accepting node connections
//   - Reverse gateway for routing RPC invocations
//   - gRPC server for the InvokePlane service
//   - Metrics exporter for Prometheus monitoring
//
// The server is not started automatically; call Start() after creation.
//
// Returns an error if:
//   - Logging initialization fails
//   - TLS certificates are invalid or missing
//   - Tunnel server creation fails
//
// Example:
//
//	cfg := &config.Config{
//	    Server: config.ServerConfig{
//	        GRPCAddress:    ":50051",
//	        MetricsAddress: ":9090",
//	    },
//	    Listener: config.ListenerConfig{
//	        Address:  ":8443",
//	        CertFile: "server.crt",
//	        KeyFile:  "server.key",
//	    },
//	}
//	srv, err := server.New(cfg)
func New(cfg *config.Config) (*Server, error) {
	if err := logging.Init(cfg.Logging.Level); err != nil {
		return nil, fmt.Errorf("failed to init logging: %w", err)
	}

	logger := logging.L()
	reg := registry.New(logger)
	authPolicy := control.NewAuthPolicy(cfg.Auth.Enabled, cfg.Auth.AllowedTokens)

	tunnelSrv, err := tunnel.New(
		cfg.Listener.Address,
		cfg.Listener.CertFile,
		cfg.Listener.KeyFile,
		cfg.Listener.CAFile,
		authPolicy,
		reg,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel server: %w", err)
	}

	// Staleness cleanup was previously never started: dead sessions stayed
	// registered forever. Tie its lifetime to the server's own lifecycle;
	// created after the last failing path so cancel is never leaked.
	ctx, cancel := context.WithCancel(context.Background())
	reg.StartCleanupLoop(ctx, 30*time.Second, 2*time.Minute)

	gateway := reverse.NewGateway(reg, logger)
	grpcSrv := grpc.NewServer()
	metricsSrv := metrics.NewExporter(cfg.Server.MetricsAddress, logger)

	// Create InvokeProxy with permissive defaults
	invokeProxy := NewInvokeProxy(reg, gateway, nil, nil, logger)

	// Register InvokePlane service
	rpc.RegisterInvokePlaneServiceServer(grpcSrv, invokeProxy)

	return &Server{
		cfg:            cfg,
		ctx:            ctx,
		cancel:         cancel,
		registry:       reg,
		tunnelServer:   tunnelSrv,
		grpcServer:     grpcSrv,
		metricsServer:  metricsSrv,
		reverseGateway: gateway,
		invokeProxy:    invokeProxy,
		logger:         logger,
	}, nil
}

// Start starts all server components in the following order:
//  1. Metrics exporter (Prometheus endpoint)
//  2. Tunnel listener (TLS+Yamux for node connections)
//  3. gRPC server (InvokePlane service for reverse RPC)
//
// The gRPC server runs in a background goroutine and listens on the address
// specified in the configuration. Errors from the gRPC server are logged but
// do not cause Start() to return an error.
//
// Returns an error if any component fails to start. If an error occurs, some
// components may have already started; call Stop() to clean up.
//
// This method is non-blocking except for the initial setup. The server will
// continue running until Stop() is called.
//
// Example:
//
//	if err := srv.Start(); err != nil {
//	    log.Fatalf("Failed to start server: %v", err)
//	}
//	log.Println("Server is running")
func (s *Server) Start() error {
	s.logger.Info("starting gRPC-Mesh server")

	if err := s.metricsServer.Start(); err != nil {
		return fmt.Errorf("failed to start metrics: %w", err)
	}

	if err := s.tunnelServer.Start(); err != nil {
		return fmt.Errorf("failed to start tunnel: %w", err)
	}

	lis, err := net.Listen("tcp", s.cfg.Server.GRPCAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.Server.GRPCAddress, err)
	}

	go func() {
		s.logger.Info("gRPC server listening", zap.String("addr", s.cfg.Server.GRPCAddress))
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", zap.Error(err))
		}
	}()

	s.logger.Info("server started successfully")
	return nil
}

// Stop gracefully shuts down all server components in the following order:
//  1. gRPC server (graceful stop with connection draining)
//  2. Tunnel server (closes listener and active sessions)
//  3. Metrics exporter (stops HTTP server)
//
// This method blocks until all components have completed their shutdown.
// It is safe to call Stop() multiple times; subsequent calls are no-ops.
//
// All active sessions will be closed, and in-flight requests will be allowed
// to complete (within gRPC's graceful stop timeout).
//
// Example:
//
//	defer srv.Stop()
//	// or
//	sigCh := make(chan os.Signal, 1)
//	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
//	<-sigCh
//	srv.Stop()
func (s *Server) Stop() {
	s.logger.Info("stopping server")

	s.cancel()

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	if s.tunnelServer != nil {
		s.tunnelServer.Stop()
	}

	if s.metricsServer != nil {
		s.metricsServer.Stop()
	}

	s.logger.Info("server stopped")
}

// ReverseGateway returns the reverse gateway instance used for routing
// RPC invocations to connected mesh nodes.
//
// The gateway provides methods for invoking both unary and streaming RPCs
// on remote nodes via their established Yamux sessions.
//
// This accessor is primarily useful for:
//   - Custom RPC routing logic
//   - Programmatic invocation outside the gRPC service
//   - Testing and monitoring
//
// Example:
//
//	gateway := srv.ReverseGateway()
//	resp, err := gateway.Invoke(ctx, "node-123", &rpc.InvokeRequest{
//	    PeerId: "node-123",
//	    Method: "calculator.Add",
//	    Payload: payload,
//	})
func (s *Server) ReverseGateway() *reverse.Gateway {
	return s.reverseGateway
}

// Registry returns the session registry that tracks all connected mesh nodes.
//
// The registry provides methods for:
//   - Listing connected nodes and their metadata
//   - Querying session state and heartbeat status
//   - Opening Yamux streams to specific nodes
//   - Managing session lifecycle
//
// This accessor is useful for:
//   - Implementing custom health checks
//   - Building admin/monitoring interfaces
//   - Session introspection and debugging
//
// Example:
//
//	reg := srv.Registry()
//	sessions := reg.List()
//	for _, sess := range sessions {
//	    fmt.Printf("Node: %s, Connected: %v, Last HB: %v\n",
//	        sess.PeerID, sess.Connected, sess.LastHeartbeat)
//	}
func (s *Server) Registry() *registry.SessionManager {
	return s.registry
}

// InvokePlaneService implements the gRPC InvokePlane service for handling
// reverse RPC invocations to mesh nodes.
//
// This service is registered on the gRPC server and processes incoming
// Invoke and InvokeStream requests by routing them through the reverse gateway
// to the appropriate mesh node.
//
// The service is instantiated automatically by New() and should not typically
// be created directly by users.
type InvokePlaneService struct {
	rpc.UnimplementedInvokePlaneServiceServer
	gateway *reverse.Gateway
	logger  *zap.Logger
}

// Invoke implements unary RPC invocation by routing the request through the
// reverse gateway to the target mesh node.
//
// The method:
//  1. Extracts the peer ID from the request
//  2. Routes the invocation through the gateway
//  3. Returns the response or error
//
// This method handles all error conditions gracefully and returns structured
// error details in the response rather than gRPC status errors (except for
// critical failures).
//
// The context deadline/timeout is honored throughout the invocation chain.
func (s *InvokePlaneService) Invoke(
	ctx context.Context,
	req *rpc.InvokeRequest,
) (*rpc.InvokeResponse, error) {
	peerID := registry.PeerID(req.PeerId)
	return s.gateway.Invoke(ctx, peerID, req)
}
