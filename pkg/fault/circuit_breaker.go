package fault

import (
	"context"
	"errors"
	"sync"
	"time"
)

// State represents circuit breaker state
type State int32

const (
	StateClosed   State = 0 // Normal operation
	StateOpen     State = 1 // Circuit breaker tripped
	StateHalfOpen State = 2 // Testing if service recovered
)

// CircuitBreakerConfig holds configuration
type CircuitBreakerConfig struct {
	MaxFailures         int
	Timeout             time.Duration
	FailureRatio        float64
	HalfOpenMaxAttempts int
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config CircuitBreakerConfig
	
	mu              sync.RWMutex
	state           State
	failures        int
	successes       int
	attempts        int
	lastFailureTime time.Time
	stateChanged    time.Time
	
	onStateChange func(from, to State)
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.HalfOpenMaxAttempts == 0 {
		config.HalfOpenMaxAttempts = 1
	}
	
	return &CircuitBreaker{
		config:       config,
		state:        StateClosed,
		stateChanged: time.Now(),
	}
}

// Execute runs a function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	if !cb.Allow() {
		return errors.New("circuit breaker open")
	}
	
	err := fn()
	
	if err != nil {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}
	
	return err
}

// Allow checks if request is allowed
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	now := time.Now()
	
	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(cb.stateChanged) > cb.config.Timeout {
			cb.setState(StateHalfOpen)
			cb.attempts = 0
			return true
		}
		return false
	case StateHalfOpen:
		return cb.attempts < cb.config.HalfOpenMaxAttempts
	}
	
	return false
}

// RecordSuccess records a successful call
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if cb.state == StateHalfOpen {
		cb.successes++
		if cb.successes >= cb.config.HalfOpenMaxAttempts {
			cb.setState(StateClosed)
			cb.reset()
		}
	} else if cb.state == StateClosed {
		cb.attempts++
		cb.failures = 0
	}
}

// RecordFailure records a failed call
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures++
	cb.lastFailureTime = time.Now()
	cb.attempts++
	
	if cb.shouldTrip() {
		cb.setState(StateOpen)
	}
}

func (cb *CircuitBreaker) shouldTrip() bool {
	if cb.failures >= cb.config.MaxFailures {
		return true
	}
	
	if cb.config.FailureRatio > 0 && cb.attempts > 0 {
		ratio := float64(cb.failures) / float64(cb.attempts)
		return ratio >= cb.config.FailureRatio
	}
	
	return false
}

// State returns current state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset manually resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.setState(StateClosed)
	cb.reset()
}

// ForceOpen manually opens the circuit
func (cb *CircuitBreaker) ForceOpen() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.setState(StateOpen)
}

// ForceClose manually closes the circuit
func (cb *CircuitBreaker) ForceClose() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.setState(StateClosed)
	cb.reset()
}

// OnStateChange registers a callback for state changes
func (cb *CircuitBreaker) OnStateChange(fn func(from, to State)) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.onStateChange = fn
}

func (cb *CircuitBreaker) setState(newState State) {
	oldState := cb.state
	cb.state = newState
	cb.stateChanged = time.Now()
	
	if cb.onStateChange != nil && oldState != newState {
		go cb.onStateChange(oldState, newState)
	}
}

func (cb *CircuitBreaker) reset() {
	cb.failures = 0
	cb.successes = 0
	cb.attempts = 0
}
