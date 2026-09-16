package main

/*
#include <stdlib.h>
#include <stdint.h>
*/
import "C"

import "unsafe"

// Go-native wrappers around the exported ABI functions, for use by the
// package tests (Go test files may not import "C"). None of these are
// //export-ed, so they do not become part of the C ABI.

func newServerForTest(cfg string) uint64 {
	return uint64(mesh_server_new(C.CString(cfg)))
}

func lastErrorForTest() string {
	return C.GoString(mesh_last_error())
}

func startForTest(h uint64) int  { return int(mesh_server_start(C.uint64_t(h))) }
func stopForTest(h uint64) int   { return int(mesh_server_stop(C.uint64_t(h))) }
func freeHandleForTest(h uint64) { mesh_server_free(C.uint64_t(h)) }

// readStringHandle mirrors the C caller pattern: read borrowed data, then
// release the handle exactly once.
func readStringHandle(strHandle uint64) (string, bool) {
	cs := mesh_str_data(C.uint64_t(strHandle))
	if cs == nil {
		return "", false
	}
	defer mesh_str_release(C.uint64_t(strHandle))
	return C.GoString(cs), true
}

func invokeForTest(h uint64, peer, method string, payload []byte, timeoutMs uint32) (string, bool) {
	var ptr unsafe.Pointer
	if len(payload) > 0 {
		ptr = unsafe.Pointer(&payload[0])
	}
	strHandle := mesh_invoke(
		C.uint64_t(h),
		C.CString(peer),
		C.CString(method),
		ptr,
		C.int(len(payload)),
		C.uint32_t(timeoutMs),
	)
	if strHandle == 0 {
		return "", false
	}
	return readStringHandle(uint64(strHandle))
}

func listNodesForTest(h uint64) (string, bool) {
	strHandle := mesh_list_nodes(C.uint64_t(h))
	if strHandle == 0 {
		return "", false
	}
	return readStringHandle(uint64(strHandle))
}

func registerCStringForTest(s string) uint64 { return registerCString(s) }
func strDataForTest(strHandle uint64) string {
	cs := mesh_str_data(C.uint64_t(strHandle))
	if cs == nil {
		return ""
	}
	return C.GoString(cs)
}
func strReleaseForTest(strHandle uint64) { mesh_str_release(C.uint64_t(strHandle)) }
