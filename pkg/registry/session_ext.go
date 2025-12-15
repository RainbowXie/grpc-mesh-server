package registry

import (
	"context"
	"sync"
	"time"

	"github.com/grpc-mesh/grpc-mesh-server/pkg/fault"
	"github.com/grpc-mesh/grpc-mesh-server/pkg/rpc"
	"go.uber.org/zap"
)

// ExtendedSessionManager extends SessionManager with method registry and circuit breakers
type ExtendedSessionManager struct {
	sessionManager  *SessionManager
	methodRegistry  *MethodRegistry
	cbConfig        *fault.CircuitBreakerConfig
	circuitBreakers map[PeerID]*fault.CircuitBreaker
	mu              sync.RWMutex
	logger          *zap.Logger
}

// HealthCheckResult contains health information for a peer
type HealthCheckResult struct {
	PeerID              PeerID
	SessionHealthy      bool
	CircuitBreakerState fault.State
	MethodCount         int
	LastHeartbeat       time.Time
	Timestamp           time.Time
}

// ExtendedStats contains extended statistics
type ExtendedStats struct {
	SessionStats          SessionStats
	TotalMethods          int
	TotalPeers            int
	AverageMethodsPerPeer float64
	CircuitBreakerStats   map[PeerID]fault.State
}

// NewExtendedSessionManager creates a new extended session manager
func NewExtendedSessionManager(
	sessionManager *SessionManager,
	methodRegistry *MethodRegistry,
	cbConfig *fault.CircuitBreakerConfig,
	logger *zap.Logger,
) *ExtendedSessionManager {
	if cbConfig == nil {
		cbConfig = &fault.CircuitBreakerConfig{
			MaxFailures:         5,
			Timeout:             10 * time.Second,
			FailureRatio:        0.5,
			HalfOpenMaxAttempts: 3,
		}
	}

	return &ExtendedSessionManager{
		sessionManager:  sessionManager,
		methodRegistry:  methodRegistry,
		cbConfig:        cbConfig,
		circuitBreakers: make(map[PeerID]*fault.CircuitBreaker),
		logger:          logger,
	}
}

// RegisterSession registers a session and initializes its circuit breaker
func (e *ExtendedSessionManager) RegisterSession(ctx context.Context, session *Session) error {
	if err := e.sessionManager.Register(ctx, session); err != nil {
		return err
	}

	// Create circuit breaker for this peer
	e.mu.Lock()
	if _, exists := e.circuitBreakers[session.PeerID]; !exists {
		cb := fault.NewCircuitBreaker(*e.cbConfig)
		cb.OnStateChange(func(from, to fault.State) {
			e.logger.Info("circuit breaker state changed",
				zap.String("peer_id", string(session.PeerID)),
				zap.Int32("from", int32(from)),
				zap.Int32("to", int32(to)))
		})
		e.circuitBreakers[session.PeerID] = cb
	}
	e.mu.Unlock()

	return nil
}

// RemoveSession removes a session and cleans up associated resources
func (e *ExtendedSessionManager) RemoveSession(peerID PeerID) error {
	// Remove from session manager
	if err := e.sessionManager.Remove(peerID); err != nil {
		return err
	}

	// Remove methods
	e.methodRegistry.RemovePeer(peerID)

	// Remove circuit breaker
	e.mu.Lock()
	delete(e.circuitBreakers, peerID)
	e.mu.Unlock()

	return nil
}

// UpdateMethods updates the method registry for a peer
func (e *ExtendedSessionManager) UpdateMethods(peerID PeerID, methods []*rpc.MethodDescriptor) error {
	// Verify session exists
	if _, exists := e.sessionManager.Get(peerID); !exists {
		return ErrSessionNotFound
	}

	e.methodRegistry.UpdateMethods(peerID, methods)
	return nil
}

// GetCircuitBreaker returns the circuit breaker for a peer
func (e *ExtendedSessionManager) GetCircuitBreaker(peerID PeerID) (*fault.CircuitBreaker, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cb, exists := e.circuitBreakers[peerID]
	return cb, exists
}

// HealthCheck performs a comprehensive health check on a peer
func (e *ExtendedSessionManager) HealthCheck(peerID PeerID) *HealthCheckResult {
	result := &HealthCheckResult{
		PeerID:    peerID,
		Timestamp: time.Now(),
	}

	// Check session
	session, exists := e.sessionManager.Get(peerID)
	if exists {
		result.SessionHealthy = true
		result.LastHeartbeat = session.LastHeartbeat
	}

	// Check circuit breaker
	e.mu.RLock()
	if cb, cbExists := e.circuitBreakers[peerID]; cbExists {
		result.CircuitBreakerState = cb.State()
	}
	e.mu.RUnlock()

	// Check methods
	if methods, methodsExist := e.methodRegistry.GetPeerMethods(peerID); methodsExist {
		result.MethodCount = len(methods)
	}

	return result
}

// GetExtendedStats returns extended statistics
func (e *ExtendedSessionManager) GetExtendedStats() *ExtendedStats {
	stats := &ExtendedStats{
		SessionStats:        e.sessionManager.Stats(),
		CircuitBreakerStats: make(map[PeerID]fault.State),
	}

	// Get method registry stats
	registryStats := e.methodRegistry.Stats()
	stats.TotalMethods = registryStats.TotalMethods
	stats.TotalPeers = registryStats.TotalPeers
	stats.AverageMethodsPerPeer = registryStats.AverageMethods

	// Get circuit breaker states
	e.mu.RLock()
	for peerID, cb := range e.circuitBreakers {
		stats.CircuitBreakerStats[peerID] = fault.State(cb.State())
	}
	e.mu.RUnlock()

	return stats
}

// ExecuteWithCircuitBreaker executes a function with circuit breaker protection
func (e *ExtendedSessionManager) ExecuteWithCircuitBreaker(
	ctx context.Context,
	peerID PeerID,
	fn func() error,
) error {
	cb, exists := e.GetCircuitBreaker(peerID)
	if !exists {
		// No circuit breaker, execute directly
		return fn()
	}

	return cb.Execute(ctx, fn)
}

// SessionManager returns the underlying session manager
func (e *ExtendedSessionManager) SessionManager() *SessionManager {
	return e.sessionManager
}

// MethodRegistry returns the method registry
func (e *ExtendedSessionManager) MethodRegistry() *MethodRegistry {
	return e.methodRegistry
}

// ListHealthyPeers returns a list of peers that are healthy
func (e *ExtendedSessionManager) ListHealthyPeers() []PeerID {
	sessions := e.sessionManager.ListAll()
	healthy := make([]PeerID, 0, len(sessions))

	for _, session := range sessions {
		health := e.HealthCheck(session.PeerID)
		if health.SessionHealthy && health.CircuitBreakerState != fault.StateOpen {
			healthy = append(healthy, session.PeerID)
		}
	}

	return healthy
}

// FindHealthyPeersWithMethod finds healthy peers that provide a specific method
func (e *ExtendedSessionManager) FindHealthyPeersWithMethod(methodName string) []PeerID {
	allPeers := e.methodRegistry.FindPeersWithMethod(methodName)
	healthy := make([]PeerID, 0, len(allPeers))

	for _, peerID := range allPeers {
		health := e.HealthCheck(peerID)
		if health.SessionHealthy && health.CircuitBreakerState != fault.StateOpen {
			healthy = append(healthy, peerID)
		}
	}

	return healthy
}
