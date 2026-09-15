package metrics

import (
	"errors"
	"net"
	"testing"

	"go.uber.org/zap/zaptest"
)

// TestExporterDoubleStart guards against a second Start() silently leaking
// the first listener.
func TestExporterDoubleStart(t *testing.T) {
	e := NewExporter("127.0.0.1:0", zaptest.NewLogger(t))

	if err := e.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer e.Stop()

	if err := e.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start: got %v, want ErrAlreadyStarted", err)
	}
}

// TestExporterBindErrorPropagates: a port already in use must surface from
// Start() itself, not as an async log line after a "successful" startup.
func TestExporterBindErrorPropagates(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()

	e := NewExporter(ln.Addr().String(), zaptest.NewLogger(t))
	if err := e.Start(); err == nil {
		e.Stop()
		t.Fatalf("Start on occupied port %s returned nil, want bind error", ln.Addr())
	}
}
