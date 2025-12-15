package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/control"
	"github.com/hashicorp/yamux"
	"go.uber.org/zap"
)

var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrInvalidHandshake = errors.New("invalid handshake")
)

// PeerID is a unique identifier for a peer
type PeerID string

// Session is an alias for SessionState for API compatibility
type Session = SessionState

// SessionState holds the state of a connected peer session
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

// TouchHeartbeat updates the last heartbeat timestamp
func (s *SessionState) TouchHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastHeartbeat = time.Now()
}

// Close closes the session
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

// SessionStats contains statistics about sessions
type SessionStats struct {
	TotalSessions    int
	ActiveSessions   int
	InactiveSessions int
	TotalHeartbeats  uint64
}

// SessionManager manages all peer sessions
type SessionManager struct {
	sessions map[PeerID]*SessionState
	mu       sync.RWMutex
	logger   *zap.Logger
}

// New creates a new session manager
func New(logger *zap.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[PeerID]*SessionState),
		logger:   logger,
	}
}

// Register registers a new peer session (API compatibility wrapper)
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

// RegisterSession registers a new peer session
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

// Get retrieves a session by peer ID
func (sm *SessionManager) Get(peerID PeerID) (*SessionState, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	state, ok := sm.sessions[peerID]
	return state, ok
}

// Remove removes a session
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

// List returns all sessions
func (sm *SessionManager) List() []*SessionState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*SessionState, 0, len(sm.sessions))
	for _, state := range sm.sessions {
		result = append(result, state.snapshot())
	}

	return result
}

// ListAll returns all sessions (API compatibility alias)
func (sm *SessionManager) ListAll() []*SessionState {
	return sm.List()
}

// Size returns the number of active sessions
func (sm *SessionManager) Size() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// OpenStream opens a new yamux stream to a peer
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

// ControlChannel returns the control message channel for a peer
func (sm *SessionManager) ControlChannel(peerID PeerID) (chan *control.ControlMessage, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	state, ok := sm.sessions[peerID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return state.ControlChan, nil
}

// Heartbeat updates heartbeat timestamp
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

// MarkDisconnected marks a session as disconnected
func (sm *SessionManager) MarkDisconnected(peerID PeerID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if state, ok := sm.sessions[peerID]; ok {
		state.mu.Lock()
		state.Connected = false
		state.mu.Unlock()
	}
}

// Cleanup removes stale sessions
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

// Stats returns session statistics
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

func (sm *SessionManager) prune() {
	// Periodic cleanup
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		removed := sm.Cleanup(2 * time.Minute)
		if removed > 0 {
			sm.logger.Info("pruned stale sessions", zap.Int("count", removed))
		}
	}
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
