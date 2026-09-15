package main

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
)

// These tests exercise the exported ABI functions through the Go-native
// testhooks wrappers (Go test files may not import "C"), applying the same
// allocation and threading rules a C caller would.

// freePortsConfig avoids fixed ports so tests never collide with the
// environment; the no-cert listener exercises the ephemeral dev certificate.
const freePortsConfig = `{"server":{"grpc_address":"127.0.0.1:0","metrics_address":"127.0.0.1:0"},"listener":{"address":"127.0.0.1:0"}}`

func mustNew(t *testing.T, cfg string) uint64 {
	t.Helper()
	h := newServerForTest(cfg)
	if h == 0 {
		t.Fatalf("mesh_server_new failed: %s", lastErrorForTest())
	}
	return h
}

func TestInvalidConfigFailsNew(t *testing.T) {
	// cert without key violates the pairing rule in pkg/config
	if h := newServerForTest(`{"listener":{"cert_file":"/nonexistent.crt"}}`); h != 0 {
		t.Fatalf("expected handle 0 for invalid config, got %d", h)
	}
	if lastErrorForTest() == "" {
		t.Fatal("expected a descriptive last_error after failed new")
	}
}

func TestStartPortConflictDoesNotPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer ln.Close()

	cfg := `{"server":{"grpc_address":"` + ln.Addr().String() + `","metrics_address":"127.0.0.1:0"},"listener":{"address":"127.0.0.1:0"}}`
	h := mustNew(t, cfg)
	defer freeHandleForTest(h)

	if rc := startForTest(h); rc == 0 {
		t.Fatal("start on occupied port must fail")
	}
	if lastErrorForTest() == "" {
		t.Fatal("expected error message for start failure")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	h := mustNew(t, freePortsConfig)
	defer freeHandleForTest(h)

	if rc := startForTest(h); rc != 0 {
		t.Fatalf("start failed: %s", lastErrorForTest())
	}
	if rc := stopForTest(h); rc != 0 {
		t.Fatal("first stop failed")
	}
	if rc := stopForTest(h); rc != 0 {
		t.Fatal("second stop must be a no-op success")
	}
}

func TestInvokeUnknownPeerReturnsDialFailed(t *testing.T) {
	h := mustNew(t, freePortsConfig)
	defer freeHandleForTest(h)

	out, ok := invokeForTest(h, "no-such-node", "a.B/C", nil, 1000)
	if !ok {
		t.Fatalf("invoke infra error: %s", lastErrorForTest())
	}

	var resp struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Success || resp.Error.Code != "DIAL_FAILED" {
		t.Fatalf("expected success=false DIAL_FAILED, got %s", out)
	}
}

func TestDoubleFreeIsNoop(t *testing.T) {
	doubleFreeForTest("hello") // must not crash
}

func TestListNodesEmpty(t *testing.T) {
	h := mustNew(t, freePortsConfig)
	defer freeHandleForTest(h)

	out, ok := listNodesForTest(h)
	if !ok {
		t.Fatalf("list_nodes failed: %s", lastErrorForTest())
	}

	var nodes []map[string]any
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("unmarshal nodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected empty node list, got %s", out)
	}
}

func TestConcurrentInvoke(t *testing.T) {
	h := mustNew(t, freePortsConfig)
	defer freeHandleForTest(h)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := invokeForTest(h, "no-such-node", "a.B/C", nil, 500); ok {
				_ = ok
			}
		}()
	}
	wg.Wait()
}

func TestInvalidHandle(t *testing.T) {
	if rc := startForTest(9999); rc == 0 {
		t.Fatal("start on invalid handle must fail")
	}
	if rc := stopForTest(9999); rc == 0 {
		t.Fatal("stop on invalid handle must fail")
	}
	if _, ok := invokeForTest(9999, "p", "m", nil, 100); ok {
		t.Fatal("invoke on invalid handle must fail")
	}
	if _, ok := listNodesForTest(9999); ok {
		t.Fatal("list_nodes on invalid handle must fail")
	}
}
