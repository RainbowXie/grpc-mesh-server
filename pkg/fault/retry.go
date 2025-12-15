package fault

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryConfig defines retry behavior
type RetryConfig struct {
	// MaxAttempts is the maximum number of retry attempts (0 = no retries)
	MaxAttempts int

	// InitialDelay is the delay before the first retry
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration

	// Multiplier is the backoff multiplier (e.g., 2.0 for exponential)
	Multiplier float64

	// Jitter adds randomness to retry delays to avoid thundering herd
	Jitter bool

	// RetryableErrors is a function to determine if an error is retryable
	RetryableErrors func(error) bool

	// OnRetry is called before each retry attempt
	OnRetry func(attempt int, delay time.Duration, err error)
}

// DefaultRetryConfig returns a sensible default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:     3,
		InitialDelay:    100 * time.Millisecond,
		MaxDelay:        10 * time.Second,
		Multiplier:      2.0,
		Jitter:          true,
		RetryableErrors: DefaultRetryableErrors,
	}
}

// DefaultRetryableErrors determines if a gRPC error is retryable
func DefaultRetryableErrors(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are not retryable
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	// Check gRPC status codes
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC errors are retryable by default
		return true
	}

	switch st.Code() {
	case codes.Unavailable,
		codes.ResourceExhausted,
		codes.Aborted,
		codes.DeadlineExceeded,
		codes.Internal:
		return true
	default:
		return false
	}
}

// Retry executes fn with exponential backoff retry logic
func Retry(ctx context.Context, config *RetryConfig, fn func() error) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	var lastErr error
	for attempt := 0; attempt <= config.MaxAttempts; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry
		if attempt >= config.MaxAttempts {
			break
		}

		// Check if error is retryable
		if config.RetryableErrors != nil && !config.RetryableErrors(err) {
			return err
		}

		// Check context
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Calculate delay
		delay := calculateDelay(attempt, config)

		// Call retry callback
		if config.OnRetry != nil {
			config.OnRetry(attempt+1, delay, err)
		}

		// Wait before retry
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", config.MaxAttempts, lastErr)
}

// calculateDelay computes the delay for a given attempt with exponential backoff and jitter
func calculateDelay(attempt int, config *RetryConfig) time.Duration {
	// Exponential backoff: InitialDelay * (Multiplier ^ attempt)
	delay := float64(config.InitialDelay)
	for i := 0; i < attempt; i++ {
		delay *= config.Multiplier
	}

	// Cap at MaxDelay
	if delay > float64(config.MaxDelay) {
		delay = float64(config.MaxDelay)
	}

	result := time.Duration(delay)

	// Add jitter if enabled
	if config.Jitter {
		// Add random jitter: [0.5 * delay, 1.5 * delay]
		jitter := 0.5 + rand.Float64() // [0.5, 1.5)
		result = time.Duration(float64(result) * jitter)
	}

	return result
}

// RetryWithRecovery wraps Retry with panic recovery
func RetryWithRecovery(ctx context.Context, config *RetryConfig, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during retry: %v", r)
		}
	}()

	return Retry(ctx, config, fn)
}

// RetryableFunc is a function that can be retried
type RetryableFunc func(ctx context.Context) error

// Executor provides a convenient way to configure and execute retries
type Executor struct {
	config *RetryConfig
}

// NewExecutor creates a new retry executor with the given config
func NewExecutor(config *RetryConfig) *Executor {
	if config == nil {
		config = DefaultRetryConfig()
	}
	return &Executor{config: config}
}

// Execute runs the function with retry logic
func (e *Executor) Execute(ctx context.Context, fn RetryableFunc) error {
	return Retry(ctx, e.config, func() error {
		return fn(ctx)
	})
}

// WithMaxAttempts creates a new executor with updated max attempts
func (e *Executor) WithMaxAttempts(maxAttempts int) *Executor {
	newConfig := *e.config
	newConfig.MaxAttempts = maxAttempts
	return &Executor{config: &newConfig}
}

// WithInitialDelay creates a new executor with updated initial delay
func (e *Executor) WithInitialDelay(delay time.Duration) *Executor {
	newConfig := *e.config
	newConfig.InitialDelay = delay
	return &Executor{config: &newConfig}
}

// WithMaxDelay creates a new executor with updated max delay
func (e *Executor) WithMaxDelay(delay time.Duration) *Executor {
	newConfig := *e.config
	newConfig.MaxDelay = delay
	return &Executor{config: &newConfig}
}
