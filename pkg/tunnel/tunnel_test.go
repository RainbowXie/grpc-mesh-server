package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
)

// TestDevBranchGeneratesUsableCert: with no cert files configured the server
// previously built a tls.Config with no certificate at all — every handshake
// was doomed. The dev branch must carry a usable (ephemeral) certificate.
func TestDevBranchGeneratesUsableCert(t *testing.T) {
	logger := zap.NewNop()
	srv, err := New("127.0.0.1:0", "", "", "",
		control.NewAuthPolicy(false, nil, nil), registry.New(logger), logger)
	if err != nil {
		t.Fatalf("tunnel.New without cert files: %v", err)
	}
	defer srv.Stop()

	if len(srv.tlsConfig.Certificates) == 0 {
		t.Fatal("dev branch produced no certificate — TLS handshakes can never complete")
	}
}

// dialAndHandshake opens a yamux client stream against a fresh handleConn
// and sends one handshake claiming the given identity.
func dialAndHandshake(t *testing.T, srv *Server, nodeID, token string) *yamux.Stream {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	// acceptLoop normally does wg.Add(1) before spawning handleConn; calling
	// it directly must mirror that or the deferred Done() underflows.
	srv.wg.Add(1)
	go srv.handleConn(serverConn)

	sess, err := yamux.Client(clientConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}

	handshake := map[string]any{
		"node_id":            nodeID,
		"token":              token,
		"version":            "1.0.0",
		"supported_features": []string{"yamux-reverse-grpc"},
		"timestamp_unix_sec": time.Now().Unix(),
	}
	if _, err := stream.Write(controlFrame(t, handshake)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	return stream
}

// waitFor polls cond until it holds or the deadline expires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// TestHandleConnRejectsTokenUsedForWrongNode: with node-bound tokens, a
// valid token presented under a different node_id must be rejected. This is
// the impersonation path that previously let any token holder claim an
// arbitrary peerId and replace that node's session.
func TestHandleConnRejectsTokenUsedForWrongNode(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)
	auth := control.NewAuthPolicy(true, nil, map[string]string{
		"legit-node": "legit-token",
	})

	srv, err := New("127.0.0.1:0", "", "", "", auth, reg, logger)
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer srv.Stop()

	stream := dialAndHandshake(t, srv, "victim-node", "legit-token")

	// The handler rejects and closes the connection; the client sees EOF.
	stream.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	if _, err := stream.Read(buf); err == nil {
		t.Fatal("impersonated handshake was not closed by the server")
	}

	if reg.Size() != 0 {
		t.Fatalf("impersonated session was registered (sessions=%d)", reg.Size())
	}
}

// TestHandleConnLegacySharedTokenAcceptsAnyNode documents the legacy
// allowed_tokens mode: identity is not bound, so any token holder may claim
// any node_id. Kept for backward compatibility; see AuthConfig.NodeTokens.
func TestHandleConnLegacySharedTokenAcceptsAnyNode(t *testing.T) {
	logger := zap.NewNop()
	reg := registry.New(logger)
	auth := control.NewAuthPolicy(true, []string{"shared-token"}, nil)

	srv, err := New("127.0.0.1:0", "", "", "", auth, reg, logger)
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer srv.Stop()

	dialAndHandshake(t, srv, "some-random-node", "shared-token")
	waitFor(t, 2*time.Second, func() bool { return reg.Size() == 1 })
}

// controlFrame wraps a JSON payload in the 4-byte big-endian length prefix
// used by the control-stream protocol on both sides.
func controlFrame(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal control payload: %v", err)
	}
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
	copy(out[4:], payload)
	return out
}

// TestHandleConnConsumesHeartbeatsBeyondStreamWindow reproduces the reported
// control-plane freeze in miniature: a node keeps writing heartbeat frames,
// and hashicorp/yamux's 256 KiB initial stream window means the writes stall
// unless the server reads its control stream. The write deadline turns the
// pre-fix permanent stall into a test failure.
func TestHandleConnConsumesHeartbeatsBeyondStreamWindow(t *testing.T) {
	// zaptest.NewLogger panics when a goroutine logs after the test returns;
	// the read loop keeps draining queued frames during teardown, so use a
	// nop logger here — assertions carry the verification.
	logger := zap.NewNop()
	reg := registry.New(logger)
	auth := control.NewAuthPolicy(false, nil, nil)

	srv, err := New("127.0.0.1:0", "", "", "", auth, reg, logger)
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer srv.Stop()

	stream := dialAndHandshake(t, srv, "test-node", "test-token")

	// ~120 B per frame; 4000 frames ≈ 480 KiB > 256 KiB window.
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < 4000; i++ {
		hb := control.NewHeartbeatMessage("test-node", uint64(i))
		if _, err := stream.Write(controlFrame(t, hb)); err != nil {
			t.Fatalf("heartbeat write stalled at frame %d (~%d KiB): %v — server is not consuming control frames",
				i, i*120/1024, err)
		}
	}

	// The consumed heartbeats must refresh the registry's staleness clock.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sessions := reg.List()
		if len(sessions) == 1 && sessions[0].LastHeartbeat.After(sessions[0].ConnectedAt) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	sessions := reg.List()
	t.Fatalf("session heartbeat was never recorded (sessions=%d)", len(sessions))
}
