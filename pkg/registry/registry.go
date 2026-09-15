package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
)

var (
	// ErrSessionNotFound is returned when attempting to access a session that
	// does not exist in the registry.
	ErrSessionNotFound = errors.New("session not found")

	// ErrInvalidHandshake is returned when a handshake message is nil or malformed.
	ErrInvalidHandshake = errors.New("invalid handshake")
)

// PeerID is a unique identifier for a mesh node.
//
// Each connected node must have a unique PeerID that is established during
// the handshake phase and used throughout the session lifecycle for routing
// and session management.
type PeerID string

// Session is an alias for SessionState for API compatibility with legacy code.
//
// Deprecated: Use SessionState directly in new code.
type Session = SessionState

// SessionState holds the complete state of a connected mesh node session.
//
// A session represents an active TLS+Yamux connection from a mesh node and
// includes all metadata needed for communication, authentication, and monitoring.
// Sessions are managed by the SessionManager and automatically cleaned up when
// connections are closed or become stale.
type SessionState struct {
	PeerID        PeerID
	Handshake     *control.Handshake
	Session       *yamux.Session
	ControlChan   chan *control.ControlMessage
	Connected     bool
	ConnectedAt   time.Time
	LastHeartbeat time.Time
	mu            sync.RWMutex
}

// TouchHeartbeat updates the last heartbeat timestamp to the current time.
//
// This method is called automatically when a heartbeat message is received
// from the connected node. The timestamp is used for staleness detection
// and connection health monitoring.
//
// This method is thread-safe.
func (s *SessionState) TouchHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastHeartbeat = time.Now()
}

// Close gracefully closes the session and all associated resources.
//
// This method:
//  1. Marks the session as disconnected
//  2. Closes the control message channel
//  3. Closes the underlying Yamux session
//
// After Close() is called, any pending channel operations will fail and
// attempts to open new streams will return errors.
//
// This method is thread-safe and idempotent.
func (s *SessionState) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Connected = false
	close(s.ControlChan)

	if s.Session != nil {
		return s.Session.Close()
	}
	return nil
}

func (s *SessionState) snapshot() *SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &SessionState{
		PeerID:        s.PeerID,
		Handshake:     cloneHandshake(s.Handshake),
		Connected:     s.Connected,
		ConnectedAt:   s.ConnectedAt,
		LastHeartbeat: s.LastHeartbeat,
	}
}

// meshMethodsKey is the handshake metadata key that carries the
// comma-separated list of method names a node reports at connect time.
const meshMethodsKey = "mesh.methods"

// Methods returns the method names the node reported in its handshake
// metadata. It is empty when the node did not report any.
func (s *SessionState) Methods() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return parseMeshMethods(s.Handshake)
}

func parseMeshMethods(h *control.Handshake) []string {
	if h == nil {
		return nil
	}
	raw := h.Metadata[meshMethodsKey]
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	methods := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			methods = append(methods, trimmed)
		}
	}
	return methods
}

// SessionStats contains aggregate statistics about all managed sessions.
//
// These statistics are useful for monitoring, health checks, and capacity planning.
type SessionStats struct {
	TotalSessions    int
	ActiveSessions   int
	InactiveSessions int
	TotalHeartbeats  uint64
}

// SessionManager manages all active mesh node sessions in a thread-safe manner.
//
// The SessionManager is the central registry for all connected nodes and provides:
//   - Session registration and deregistration
//   - Session lookup by peer ID
//   - Heartbeat tracking and staleness detection
//   - Yamux stream management for RPC invocations
//   - Automatic cleanup of stale sessions
//
// All public methods are thread-safe and can be called concurrently.
//
// Example usage:
//
//	manager := registry.New(logger)
//	session, err := manager.RegisterSession(peerID, handshake, yamuxSession)
//	if err != nil {
//	    return err
//	}
//
//	// Later, open a stream for RPC
//	stream, err := manager.OpenStream(ctx, peerID)
type SessionManager struct {
	sessions map[PeerID]*SessionState
	mu       sync.RWMutex
	logger   *zap.Logger
}

// New creates a new session manager with an empty session registry.
//
// The manager is ready to accept session registrations immediately after creation.
//
// Parameters:
//   - logger: Zap logger for structured logging of session lifecycle events
//
// Example:
//
//	manager := registry.New(zap.L())
func New(logger *zap.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[PeerID]*SessionState),
		logger:   logger,
	}
}

// Register registers a pre-constructed peer session in the registry.
//
// This is a legacy compatibility wrapper around the session registration logic.
// New code should use RegisterSession() instead for more explicit control.
//
// If a session with the same peer ID already exists, it will be closed and
// replaced with the new session.
//
// Parameters:
//   - ctx: Context for cancellation (currently unused but reserved for future use)
//   - session: The pre-constructed session to register
//
// Returns ErrInvalidHandshake if the session is nil.
//
// Deprecated: Use RegisterSession() in new code.
func (sm *SessionManager) Register(ctx context.Context, session *SessionState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session == nil {
		return ErrInvalidHandshake
	}

	// Close existing session if any
	if existing, ok := sm.sessions[session.PeerID]; ok {
		sm.logger.Warn("replacing existing session", zap.String("peer_id", string(session.PeerID)))
		existing.Close()
	}

	sm.sessions[session.PeerID] = session
	sm.logger.Info("registered session",
		zap.String("peer_id", string(session.PeerID)))

	return nil
}

// RegisterSession registers a new peer session with the provided components.
//
// This method creates a SessionState from the provided parameters and registers
// it in the manager. If a session with the same peer ID already exists, the old
// session is closed and replaced.
//
// Parameters:
//   - peerID: Unique identifier for the connecting node
//   - handshake: Handshake message containing node metadata and authentication
//   - session: Active Yamux session for multiplexed streams
//
// Returns:
//   - The newly created and registered SessionState
//   - ErrInvalidHandshake if the handshake is nil
//
// The returned session is already registered and ready for use. It includes
// an initialized control channel for receiving control plane messages.
//
// Example:
//
//	state, err := manager.RegisterSession(
//	    registry.PeerID("node-123"),
//	    handshake,
//	    yamuxSession,
//	)
//	if err != nil {
//	    return err
//	}
//	log.Printf("Registered node: %s", state.PeerID)
func (sm *SessionManager) RegisterSession(
	peerID PeerID,
	handshake *control.Handshake,
	session *yamux.Session,
) (*SessionState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if handshake == nil {
		return nil, ErrInvalidHandshake
	}

	now := time.Now()
	state := &SessionState{
		PeerID:        peerID,
		Handshake:     handshake,
		Session:       session,
		ControlChan:   make(chan *control.ControlMessage, 100),
		Connected:     true,
		ConnectedAt:   now,
		LastHeartbeat: now,
	}

	// Close existing session if any
	if existing, ok := sm.sessions[peerID]; ok {
		sm.logger.Warn("replacing existing session", zap.String("peer_id", string(peerID)))
		existing.Close()
	}

	sm.sessions[peerID] = state
	sm.logger.Info("registered session",
		zap.String("peer_id", string(peerID)),
		zap.String("version", handshake.Version))

	return state, nil
}

// Get retrieves a session by peer ID.
//
// This method returns the live session state, not a snapshot. Callers should
// not hold references to the returned session for extended periods as it may
// become stale.
//
// Parameters:
//   - peerID: The unique identifier of the node to look up
//
// Returns:
//   - The session state if found
//   - A boolean indicating whether the session exists (true) or not (false)
//
// Example:
//
//	session, ok := manager.Get(registry.PeerID("node-123"))
//	if !ok {
//	    return fmt.Errorf("node not found")
//	}
//	fmt.Printf("Node connected at: %v\n", session.ConnectedAt)
func (sm *SessionManager) Get(peerID PeerID) (*SessionState, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	state, ok := sm.sessions[peerID]
	return state, ok
}

// Remove removes a session from the registry and closes all its resources.
//
// This method:
//  1. Looks up the session by peer ID
//  2. Closes the session (Yamux connection, control channel)
//  3. Removes it from the registry
//
// If the session does not exist, ErrSessionNotFound is returned.
//
// Parameters:
//   - peerID: The unique identifier of the node to remove
//
// Returns:
//   - nil on successful removal
//   - ErrSessionNotFound if no session exists for the given peer ID
//
// Example:
//
//	if err := manager.Remove(registry.PeerID("node-123")); err != nil {
//	    log.Printf("Failed to remove session: %v", err)
//	}
func (sm *SessionManager) Remove(peerID PeerID) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	state, ok := sm.sessions[peerID]
	if !ok {
		return ErrSessionNotFound
	}

	state.Close()
	delete(sm.sessions, peerID)
	sm.logger.Info("removed session", zap.String("peer_id", string(peerID)))

	return nil
}

// RemoveIfCurrent deletes peerID only when the map still holds this exact
// SessionState. A replaced connection's handler must not Close/delete the
// successor that RegisterSession already stored under the same peer ID.
func (sm *SessionManager) RemoveIfCurrent(peerID PeerID, session *SessionState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	current, ok := sm.sessions[peerID]
	if !ok {
		return ErrSessionNotFound
	}
	if current != session {
		sm.logger.Info("skipping stale session remove",
			zap.String("peer_id", string(peerID)))
		return nil
	}

	current.Close()
	delete(sm.sessions, peerID)
	sm.logger.Info("removed session", zap.String("peer_id", string(peerID)))
	return nil
}

// List returns snapshots of all currently registered sessions.
//
// The returned sessions are snapshots taken at the time of the call and will
// not reflect subsequent state changes. Use this method for enumeration,
// monitoring, and reporting purposes.
//
// The order of sessions in the returned slice is not guaranteed.
//
// Returns:
//   - A slice of session snapshots (never nil, but may be empty)
//
// Example:
//
//	sessions := manager.List()
//	for _, sess := range sessions {
//	    fmt.Printf("Node %s: connected=%v, last_hb=%v\n",
//	        sess.PeerID, sess.Connected, sess.LastHeartbeat)
//	}
func (sm *SessionManager) List() []*SessionState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*SessionState, 0, len(sm.sessions))
	for _, state := range sm.sessions {
		result = append(result, state.snapshot())
	}

	return result
}

// ListAll returns all sessions (API compatibility alias).
//
// This is an alias for List() maintained for backward compatibility.
//
// Deprecated: Use List() instead.
func (sm *SessionManager) ListAll() []*SessionState {
	return sm.List()
}

// Size returns the total number of currently registered sessions.
//
// This includes both connected and disconnected sessions. For more detailed
// statistics, use Stats() instead.
//
// Returns:
//   - The number of sessions in the registry (>= 0)
//
// Example:
//
//	count := manager.Size()
//	log.Printf("Managing %d node sessions", count)
func (sm *SessionManager) Size() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// OpenStream opens a new multiplexed Yamux stream to the specified peer.
//
// This method is used to establish a new stream over an existing Yamux session
// for data-plane operations like reverse RPC invocations.
//
// The operation respects the provided context and will be cancelled if the
// context is cancelled or times out.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - peerID: The unique identifier of the target node
//
// Returns:
//   - A new Yamux stream ready for bidirectional communication
//   - An error if:
//   - The session does not exist (ErrSessionNotFound)
//   - The peer is not connected or the session is closed
//   - Stream creation fails
//   - The context is cancelled or times out
//
// Callers are responsible for closing the returned stream when done.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	stream, err := manager.OpenStream(ctx, registry.PeerID("node-123"))
//	if err != nil {
//	    return fmt.Errorf("failed to open stream: %w", err)
//	}
//	defer stream.Close()
//
//	// Use stream for RPC...
func (sm *SessionManager) OpenStream(ctx context.Context, peerID PeerID) (*yamux.Stream, error) {
	sm.mu.RLock()
	state, ok := sm.sessions[peerID]
	sm.mu.RUnlock()

	if !ok {
		return nil, ErrSessionNotFound
	}

	if !state.Connected || state.Session == nil {
		return nil, fmt.Errorf("peer not connected: %s", peerID)
	}

	// Open stream with context
	done := make(chan struct{})
	var stream *yamux.Stream
	var err error

	go func() {
		stream, err = state.Session.OpenStream()
		close(done)
	}()

	select {
	case <-done:
		return stream, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ControlChannel returns the control message channel for a peer.
//
// The control channel is used for sending control-plane messages (like
// configuration updates, shutdown signals, etc.) to a connected node.
//
// The channel is buffered and will block if the buffer is full. Receivers
// should consume messages promptly to avoid blocking the sender.
//
// Parameters:
//   - peerID: The unique identifier of the target node
//
// Returns:
//   - The control message channel for the session
//   - ErrSessionNotFound if no session exists for the given peer ID
//
// The returned channel will be closed when the session is closed.
//
// Example:
//
//	ch, err := manager.ControlChannel(registry.PeerID("node-123"))
//	if err != nil {
//	    return err
//	}
//
//	msg := &control.ControlMessage{Type: "config_update", ...}
//	select {
//	case ch <- msg:
//	    log.Println("Message sent")
//	case <-time.After(5 * time.Second):
//	    return fmt.Errorf("timeout sending message")
//	}
func (sm *SessionManager) ControlChannel(peerID PeerID) (chan *control.ControlMessage, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	state, ok := sm.sessions[peerID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return state.ControlChan, nil
}

// Heartbeat updates the heartbeat timestamp for a peer session.
//
// This method should be called whenever a heartbeat message is received from
// a node. The timestamp is used for staleness detection and connection health
// monitoring.
//
// Parameters:
//   - ctx: Context (currently unused but reserved for future use)
//   - peerID: The unique identifier of the node that sent the heartbeat
//
// Returns:
//   - nil on success
//   - ErrSessionNotFound if no session exists for the given peer ID
//
// Example:
//
//	if err := manager.Heartbeat(ctx, registry.PeerID("node-123")); err != nil {
//	    log.Printf("Failed to record heartbeat: %v", err)
//	}
func (sm *SessionManager) Heartbeat(ctx context.Context, peerID PeerID) error {
	sm.mu.RLock()
	state, ok := sm.sessions[peerID]
	sm.mu.RUnlock()

	if !ok {
		return ErrSessionNotFound
	}

	state.TouchHeartbeat()
	return nil
}

// MarkDisconnected marks a session as disconnected without removing it.
//
// This method sets the Connected flag to false while keeping the session
// in the registry. This is useful for tracking disconnected sessions that
// may reconnect or need to be cleaned up later.
//
// If the session does not exist, this method is a no-op.
//
// Parameters:
//   - peerID: The unique identifier of the node to mark as disconnected
//
// Example:
//
//	manager.MarkDisconnected(registry.PeerID("node-123"))
func (sm *SessionManager) MarkDisconnected(peerID PeerID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if state, ok := sm.sessions[peerID]; ok {
		state.mu.Lock()
		state.Connected = false
		state.mu.Unlock()
	}
}

// Cleanup removes sessions that are disconnected or haven't sent heartbeats recently.
//
// A session is considered stale if:
//   - It is marked as disconnected, OR
//   - The time since its last heartbeat exceeds maxAge
//
// All stale sessions are closed and removed from the registry.
//
// Parameters:
//   - maxAge: Maximum duration since last heartbeat before a session is considered stale
//
// Returns:
//   - The number of sessions that were removed
//
// This method is safe to call periodically for automatic cleanup. The server
// typically runs this on a timer (e.g., every 30 seconds).
//
// Example:
//
//	// Remove sessions with no heartbeat for 2 minutes
//	removed := manager.Cleanup(2 * time.Minute)
//	if removed > 0 {
//	    log.Printf("Cleaned up %d stale sessions", removed)
//	}
func (sm *SessionManager) Cleanup(maxAge time.Duration) int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	removed := 0

	for peerID, state := range sm.sessions {
		state.mu.RLock()
		isStale := !state.Connected || now.Sub(state.LastHeartbeat) > maxAge
		state.mu.RUnlock()

		if isStale {
			sm.logger.Info("removing stale session",
				zap.String("peer_id", string(peerID)),
				zap.Duration("age", now.Sub(state.LastHeartbeat)))
			state.Close()
			delete(sm.sessions, peerID)
			removed++
		}
	}

	return removed
}

// Stats returns aggregate statistics about all managed sessions.
//
// This method computes statistics by iterating over all sessions and
// examining their state. The returned statistics are a snapshot at the
// time of the call.
//
// Returns:
//   - SessionStats containing counts of total, active, and inactive sessions
//
// Example:
//
//	stats := manager.Stats()
//	log.Printf("Sessions: %d total, %d active, %d inactive",
//	    stats.TotalSessions, stats.ActiveSessions, stats.InactiveSessions)
func (sm *SessionManager) Stats() SessionStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	stats := SessionStats{
		TotalSessions: len(sm.sessions),
	}

	for _, state := range sm.sessions {
		state.mu.RLock()
		if state.Connected {
			stats.ActiveSessions++
		} else {
			stats.InactiveSessions++
		}
		state.mu.RUnlock()
	}

	return stats
}

// StartCleanupLoop runs periodic staleness cleanup until ctx is cancelled.
// The SessionManager does not start it on its own; the process entrypoint
// that owns the server lifecycle must call this exactly once, otherwise
// dead sessions accumulate forever.
func (sm *SessionManager) StartCleanupLoop(ctx context.Context, interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if removed := sm.Cleanup(maxAge); removed > 0 {
					sm.logger.Info("pruned stale sessions", zap.Int("count", removed))
				}
			}
		}
	}()
}

func cloneHandshake(h *control.Handshake) *control.Handshake {
	if h == nil {
		return nil
	}
	return &control.Handshake{
		NodeID:       h.NodeID,
		Version:      h.Version,
		Features:     cloneSlice(h.Features),
		Metadata:     cloneMap(h.Metadata),
		TimestampSec: h.TimestampSec,
		Token:        h.Token,
	}
}

func cloneSlice(s []string) []string {
	if s == nil {
		return nil
	}
	result := make([]string, len(s))
	copy(result, s)
	return result
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
