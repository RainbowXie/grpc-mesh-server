// Command meshlib builds the grpc-mesh-server as a c-shared library
// (go build -buildmode=c-shared), exposing a minimal C ABI for embedding
// from foreign runtimes such as Python. The generated meshlib.h declares
// the exported signatures.
//
// Conventions:
//   - Handles are opaque uint64 values; 0 is never a valid handle.
//   - Functions return 0 on success and non-zero on failure; a descriptive
//     message is available via mesh_last_error on the calling thread.
//   - Strings returned by mesh_invoke / mesh_list_nodes are heap allocated
//     and MUST be released with mesh_free_string. Releasing twice is a no-op.
//   - mesh_last_error returns a thread-local pointer owned by the library;
//     it is valid until the next ABI call on the same thread and must not
//     be freed.
//   - No function panics across the boundary; every export recovers.
package main

/*
#include <stdlib.h>
#include <stdint.h>

// Thread-local storage for the most recent error message. Exported Go
// functions run on the caller's OS thread, so this is per-calling-thread.
static __thread char *mesh_err_str = NULL;

static void mesh_set_err(char *s) {
	if (mesh_err_str != NULL) {
		free(mesh_err_str);
	}
	mesh_err_str = s;
}

static const char *mesh_get_err(void) {
	return mesh_err_str != NULL ? mesh_err_str : "";
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/config"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/registry"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/server"
)

// meshServer is the Go side of an ABI handle.
type meshServer struct {
	srv *server.Server
}

var (
	handlesMu sync.Mutex
	nextID    uint64
	handles   = map[uint64]*meshServer{}
)

// liveStrings tracks every heap string the ABI handed out so that
// mesh_free_string can make double releases a no-op instead of UB.
var (
	stringsMu sync.Mutex
	live      = map[unsafe.Pointer]bool{}
)

func setErr(format string, args ...any) {
	C.mesh_set_err(C.CString(fmt.Sprintf(format, args...)))
}

// goCString allocates a NUL-terminated copy of s via C malloc and registers
// it for safe release.
func goCString(s string) *C.char {
	cs := C.CString(s)
	stringsMu.Lock()
	live[unsafe.Pointer(cs)] = true
	stringsMu.Unlock()
	return cs
}

//export mesh_server_new
func mesh_server_new(jsonConfig *C.char) (handle C.uint64_t) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_server_new: %v", r)
			handle = 0
		}
	}()

	cfgJSON := C.GoString(jsonConfig)
	cfg, err := config.LoadBytes([]byte(cfgJSON))
	if err != nil {
		setErr("invalid config: %v", err)
		return 0
	}

	srv, err := server.New(cfg)
	if err != nil {
		setErr("failed to construct server: %v", err)
		return 0
	}

	handlesMu.Lock()
	defer handlesMu.Unlock()
	nextID++
	handles[nextID] = &meshServer{srv: srv}
	return C.uint64_t(nextID)
}

//export mesh_server_start
func mesh_server_start(handle C.uint64_t) (rc C.int) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_server_start: %v", r)
			rc = 1
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return 1
	}
	if err := ms.srv.Start(); err != nil {
		setErr("start failed: %v", err)
		return 1
	}
	return 0
}

//export mesh_server_stop
func mesh_server_stop(handle C.uint64_t) (rc C.int) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_server_stop: %v", r)
			rc = 1
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return 1
	}
	ms.srv.Stop() // idempotent by contract
	return 0
}

//export mesh_server_free
func mesh_server_free(handle C.uint64_t) {
	defer func() { _ = recover() }()

	handlesMu.Lock()
	ms, ok := handles[uint64(handle)]
	delete(handles, uint64(handle))
	handlesMu.Unlock()
	if ok {
		ms.srv.Stop()
	}
}

//export mesh_invoke
func mesh_invoke(handle C.uint64_t, peerID, method *C.char, payload unsafe.Pointer, payloadLen C.int, timeoutMs C.uint32_t) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_invoke: %v", r)
			out = nil
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return nil
	}

	var payloadBytes []byte
	if payloadLen > 0 {
		payloadBytes = C.GoBytes(payload, payloadLen)
	}

	req := &rpc.InvokeRequest{
		PeerId:    C.GoString(peerID),
		Method:    C.GoString(method),
		Payload:   payloadBytes,
		TimeoutMs: uint32(timeoutMs),
	}

	resp, err := ms.srv.ReverseGateway().Invoke(context.Background(), registry.PeerID(req.PeerId), req)
	if err != nil {
		setErr("invoke failed: %v", err)
		return nil
	}

	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		setErr("marshal response: %v", err)
		return nil
	}
	return goCString(string(data))
}

//export mesh_list_nodes
func mesh_list_nodes(handle C.uint64_t) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_list_nodes: %v", r)
			out = nil
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return nil
	}

	type nodeJSON struct {
		NodeID        string   `json:"node_id"`
		Version       string   `json:"version"`
		Methods       []string `json:"methods"`
		ConnectedAt   string   `json:"connected_at"`
		LastHeartbeat string   `json:"last_heartbeat"`
	}

	nodes := make([]nodeJSON, 0)
	for _, s := range ms.srv.Registry().List() {
		methods := s.Methods()
		if methods == nil {
			methods = []string{}
		}
		nodes = append(nodes, nodeJSON{
			NodeID:        string(s.PeerID),
			Version:       s.Handshake.Version,
			Methods:       methods,
			ConnectedAt:   s.ConnectedAt.Format(time.RFC3339Nano),
			LastHeartbeat: s.LastHeartbeat.Format(time.RFC3339Nano),
		})
	}

	data, err := json.Marshal(nodes)
	if err != nil {
		setErr("marshal nodes: %v", err)
		return nil
	}
	return goCString(string(data))
}

//export mesh_free_string
func mesh_free_string(cs *C.char) {
	defer func() { _ = recover() }()

	if cs == nil {
		return
	}
	p := unsafe.Pointer(cs)
	stringsMu.Lock()
	// Only free pointers we handed out; a double release simply misses the
	// map and becomes a no-op.
	if live[p] {
		delete(live, p)
		C.free(p)
	}
	stringsMu.Unlock()
}

//export mesh_last_error
func mesh_last_error() *C.char {
	defer func() { _ = recover() }()
	return (*C.char)(unsafe.Pointer(C.mesh_get_err()))
}

func lookup(id uint64) (*meshServer, bool) {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	ms, ok := handles[id]
	return ms, ok
}

func main() {
	// buildmode=c-shared requires a main package; the library is meant to be
	// loaded, not executed.
}
