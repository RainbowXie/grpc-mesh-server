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
	auth := control.NewAuthPolicy(false, nil)

	srv, err := New("127.0.0.1:0", "", "", "", auth, reg, logger)
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer srv.Stop()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	// acceptLoop normally does wg.Add(1) before spawning handleConn; calling
	// it directly must mirror that or the deferred Done() underflows.
	srv.wg.Add(1)
	go srv.handleConn(serverConn)

	sess, err := yamux.Client(clientConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	defer sess.Close()

	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}

	// Handshake in the raw JSON shape Rust nodes send (Handshake::to_payload):
	// field names differ from the Go ControlMessage wrapper on purpose.
	handshake := map[string]any{
		"node_id":            "test-node",
		"token":              "test-token",
		"version":            "1.0.0",
		"supported_features": []string{"yamux-reverse-grpc"},
		"timestamp_unix_sec": time.Now().Unix(),
	}
	if _, err := stream.Write(controlFrame(t, handshake)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

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
