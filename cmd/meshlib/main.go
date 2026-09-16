// Command meshlib builds the grpc-mesh-server as a c-shared library
// (go build -buildmode=c-shared), exposing a minimal C ABI for embedding
// from foreign runtimes such as Python. The generated meshlib.h declares
// the exported signatures.
//
// Conventions:
//   - Handles are opaque uint64 values; 0 is never a valid handle.
//   - Functions return 0 on success and non-zero on failure; a descriptive
//     message is available via mesh_last_error on the calling thread.
//   - mesh_invoke / mesh_list_nodes return a string handle (0 on failure).
//     Read the content with mesh_str_data (borrowed pointer, do not free,
//     valid until release) and release it with mesh_str_release. Releasing
//     the same handle twice is a no-op, and identity is a monotonic id, so
//     allocator address reuse can never make a stale release free a live
//     string.
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

// liveStrings maps a monotonic string-handle id to its C allocation.
// Identity is the id, never the pointer: the allocator may hand a freed
// address to a new string, and only id-based lookup keeps a stale release
// from freeing a live string.
var (
	stringsMu sync.Mutex
	nextStrID uint64
	liveStrs  = map[uint64]*C.char{}
)

func setErr(format string, args ...any) {
	C.mesh_set_err(C.CString(fmt.Sprintf(format, args...)))
}

// registerCString stores s and returns its handle. Handle 0 is never
// issued, so callers can use it as the failure sentinel.
func registerCString(s string) uint64 {
	cs := C.CString(s)
	stringsMu.Lock()
	defer stringsMu.Unlock()
	nextStrID++
	liveStrs[nextStrID] = cs
	return nextStrID
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
func mesh_invoke(handle C.uint64_t, peerID, method *C.char, payload unsafe.Pointer, payloadLen C.int, timeoutMs C.uint32_t) (out C.uint64_t) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_invoke: %v", r)
			out = 0
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return 0
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
		return 0
	}

	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		setErr("marshal response: %v", err)
		return 0
	}
	return C.uint64_t(registerCString(string(data)))
}

//export mesh_list_nodes
func mesh_list_nodes(handle C.uint64_t) (out C.uint64_t) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_list_nodes: %v", r)
			out = 0
		}
	}()

	ms, ok := lookup(uint64(handle))
	if !ok {
		setErr("invalid handle: %d", uint64(handle))
		return 0
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
		return 0
	}
	return C.uint64_t(registerCString(string(data)))
}

//export mesh_str_data
func mesh_str_data(strHandle C.uint64_t) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			setErr("panic in mesh_str_data: %v", r)
			out = nil
		}
	}()

	stringsMu.Lock()
	defer stringsMu.Unlock()
	// Borrowed pointer: valid until mesh_str_release on this handle; NULL
	// for unknown or already-released handles.
	return liveStrs[uint64(strHandle)]
}

//export mesh_str_release
func mesh_str_release(strHandle C.uint64_t) {
	defer func() { _ = recover() }()

	stringsMu.Lock()
	cs, ok := liveStrs[uint64(strHandle)]
	delete(liveStrs, uint64(strHandle))
	stringsMu.Unlock()
	// A second release finds no entry and is a no-op.
	if ok {
		C.free(unsafe.Pointer(cs))
	}
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
