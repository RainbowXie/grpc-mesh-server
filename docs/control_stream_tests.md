# Control Stream Test Suite Documentation

## Overview

The control stream test suite (`pkg/tunnel/controlflow_test.go`) provides comprehensive coverage of the Phase 3 control flow implementation, including session registration, heartbeat management, OpenStream operations, token validation, and failure scenarios.

## Test Coverage

### 1. Session Registration & Lifecycle

#### `TestControlFlowRegistration`
**Purpose**: Verifies the complete handshake and session registration flow.

**What it tests**:
- TLS + Yamux connection establishment
- Control stream creation
- Handshake message exchange
- Session registration in SessionManager
- Metadata preservation (NodeID, version, features, timestamps)
- Yamux session health after registration

**Expected behavior**:
- Session is registered with correct NodeID
- Handshake data is preserved in SessionState
- ConnectedAt and LastHeartbeat timestamps are set
- Session is not marked as closed immediately
- At least 1 yamux stream exists (control stream)

---

#### `TestControlFlowSessionReconnect`
**Purpose**: Validates that nodes can reconnect and replace existing sessions.

**What it tests**:
- First connection establishment
- Session registration
- Connection closure
- Second connection (reconnect) with same NodeID
- Old session cleanup
- New session replacement

**Expected behavior**:
- Reconnected session has newer ConnectedAt timestamp
- First yamux session is closed
- Second yamux session is active
- SessionManager tracks the latest session

---

### 2. Stream Operations

#### `TestControlFlowOpenStream`
**Purpose**: Validates bidirectional stream creation after registration.

**What it tests**:
- Session registration
- OpenStream call from server side (reverse direction)
- Echo server on node side
- Data transmission in both directions

**Expected behavior**:
- OpenStream succeeds after session is registered
- Stream is bidirectional (read + write)
- Data echoed back correctly
- No errors during I/O operations

---

#### `TestControlFlowMultipleStreams`
**Purpose**: Verifies concurrent stream operations over a single session.

**What it tests**:
- Concurrent OpenStream calls (10 streams)
- Parallel I/O operations
- Echo server handling multiple connections
- No stream interference or data corruption

**Expected behavior**:
- All 10 streams open successfully
- Each stream receives correct echo response
- No cross-stream data leakage
- All operations complete within timeout

---

#### `TestControlFlowOpenStreamBeforeRegistration`
**Purpose**: Ensures graceful failure when opening streams to non-existent sessions.

**What it tests**:
- OpenStream call to non-registered NodeID
- Error handling and classification

**Expected behavior**:
- Returns `registry.ErrSessionNotFound`
- No panic or hang
- Error is immediately returned

---

### 3. Authentication & Authorization

#### `TestControlFlowTokenRequired`
**Purpose**: Verifies token validation when RequireToken is enabled.

**What it tests**:
- Token validation during handshake
- Valid token acceptance
- Session registration with correct token

**Expected behavior**:
- Node with valid token is registered
- Session is accessible via SessionManager
- Heartbeats work normally

---

#### `TestControlFlowTokenInvalid`
**Purpose**: Validates that invalid tokens are rejected.

**What it tests**:
- Handshake with wrong token
- Server rejection of invalid authentication
- No session registration on auth failure

**Expected behavior**:
- Session is NOT registered
- Connection may be closed by server
- SessionManager does not contain the rejected node

---

### 4. Handshake Validation

#### `TestControlFlowHandshakeMissingNodeID`
**Purpose**: Ensures handshakes without node_id are rejected.

**What it tests**:
- Handshake with empty NodeID
- Server validation logic
- Error handling

**Expected behavior**:
- No session registered
- SessionManager remains empty
- Connection is rejected

---

#### `TestControlFlowHandshakeTimeout`
**Purpose**: Verifies deadline enforcement during handshake.

**What it tests**:
- Short HandshakeDeadline configuration (200ms)
- Delayed handshake submission (400ms delay)
- Timeout handling

**Expected behavior**:
- Session is not registered
- Server closes connection after timeout
- No resource leaks

---

### 5. Heartbeat & Cleanup

#### `TestTunnelServerHeartbeatTimeout` (in load_test.go)
**Purpose**: Validates heartbeat loop and session removal on missed heartbeats.

**What it tests**:
- Session registration with heartbeat loop
- Heartbeat stop simulation
- Session timeout detection
- Automatic session removal

**Expected behavior**:
- Session is registered initially
- Session is removed after heartbeats stop
- Cleanup happens within configured timeouts

---

#### `TestControlFlowSessionCleanup`
**Purpose**: Verifies automatic cleanup of stale sessions.

**What it tests**:
- Session with active heartbeats
- Heartbeat stop to make session stale
- Cleanup interval and StaleAfter policy
- Session pruning

**Expected behavior**:
- Session exists while heartbeats are active
- Session is removed after StaleAfter duration
- Cleanup runs on schedule

---

## Test Helpers

### `setupTestServer`
Creates a fully configured test server with:
- TLS configuration
- SessionManager
- SessionPolicy (heartbeat, cleanup intervals)
- Optional token authentication

### `dialNode` / `dialNodeWithToken`
Establishes a simulated node connection:
- TLS dial
- Yamux client session
- Control stream creation
- Handshake submission (with optional token)

### `startHeartbeatLoop`
Starts a background goroutine sending periodic heartbeats:
- Configurable interval
- Graceful cancellation
- Context-aware

### `startEchoServer`
Runs an echo server accepting multiple streams:
- Accepts unlimited streams until cancelled
- Each stream echoes received data
- Proper cleanup on context cancellation

### `waitForSession` / `expectSessionRemoved`
Polling helpers for test assertions:
- Wait for session registration
- Wait for session cleanup
- Configurable timeout
- Fail test on timeout

---

## Running Tests

### Run all control flow tests:
```bash
cd grpc-mesh-server
go test ./pkg/tunnel -run TestControlFlow -v
```

### Run specific test:
```bash
go test ./pkg/tunnel -run TestControlFlowOpenStream -v
```

### Run with race detector:
```bash
go test ./pkg/tunnel -run TestControlFlow -race -v
```

### Run all tunnel tests (including load tests):
```bash
go test ./pkg/tunnel -v -timeout=60s
```

---

## Test Parameters

### Timing Configuration
The tests use relatively short timeouts for fast feedback:
- **HandshakeDeadline**: 2s (most tests), 200ms (timeout test)
- **HeartbeatInterval**: 500ms (standard), 50-200ms (cleanup tests)
- **HeartbeatGrace**: 500ms (standard), 100-200ms (cleanup tests)
- **CleanupInterval**: 1s (standard), 200ms (cleanup tests)
- **StaleAfter**: 5s (standard), 500ms (cleanup tests)

### Load Parameters
- **Concurrent streams** (TestControlFlowMultipleStreams): 10
- **Payload size**: varies by test
- **Total test timeout**: 45s

---

## Integration with ROADMAP

These tests fulfill **Phase 3 (Week 3)** requirements:

### ✅ Completed:
1. **PR-Phase3-Session-HeartbeatLoop**: 
   - `TestTunnelServerHeartbeatTimeout`
   - `TestControlFlowSessionCleanup`

2. **PR-Phase3-SessionEvents**: 
   - Event publishing tested implicitly via session lifecycle tests
   - Cleanup hooks verified

3. **PR-Phase3-ControlStream-Testkit**: 
   - `TestControlFlowRegistration` - handshake & registration
   - `TestControlFlowOpenStream` - OpenStream end-to-end
   - `TestControlFlowMultipleStreams` - concurrent streams
   - `TestControlFlowTokenRequired` - valid token
   - `TestControlFlowTokenInvalid` - invalid token rejection
   - `TestControlFlowHandshakeMissingNodeID` - validation failures
   - `TestControlFlowHandshakeTimeout` - deadline enforcement
   - `TestControlFlowSessionReconnect` - reconnection scenarios
   - `TestControlFlowOpenStreamBeforeRegistration` - error cases

---

## Next Steps (Phase 4)

The control stream infrastructure is now ready for:
1. **Reverse gRPC dialing**: Use `sessions.OpenStream()` in gRPC client dialer
2. **Method-level ACLs**: Build on token validation framework
3. **Rate limiting**: Leverage SessionManager state tracking
4. **End-to-end Invoke tests**: Call actual gRPC methods through tunnels

---

## Debugging Failed Tests

### Common Issues:

1. **Timeout during OpenStream**
   - Check that echo server is running
   - Verify session is registered
   - Look for yamux session closure

2. **Session not registered**
   - Check handshake validation errors in logs
   - Verify token configuration matches
   - Ensure NodeID is non-empty

3. **Cleanup test failures**
   - Timing-sensitive; may need adjustment for slow CI
   - Check that cleanup intervals are reasonable
   - Verify heartbeat stop timing

4. **Race conditions**
   - Run with `-race` flag
   - Check for proper mutex usage
   - Verify goroutine cleanup

### Logging:
Tests use `zap.NewNop()` for silence. To enable logging:
```go
logger := zap.NewDevelopment()
```

---

## Coverage Report

To generate coverage:
```bash
go test ./pkg/tunnel -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

Expected coverage for Phase 3 components:
- `control/stream.go`: >90%
- `control/message.go`: >85%
- `control/handshake.go`: >85%
- `tunnel/server.go` (control loop): >80%
- `registry/registry.go`: >90%
