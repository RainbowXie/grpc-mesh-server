package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTokenBucketLimiter tests token bucket rate limiting
func TestTokenBucketLimiter(t *testing.T) {
	limiter := NewTokenBucketLimiter(10, 10) // 10 req/sec, burst 10
	ctx := context.Background()

	// First burst should succeed
	for i := 0; i < 10; i++ {
		allowed := limiter.Allow(ctx, "peer-001", "method")
		assert.True(t, allowed, "burst requests should be allowed")
	}

	// Next request should be denied (bucket empty)
	allowed := limiter.Allow(ctx, "peer-001", "method")
	assert.False(t, allowed, "request beyond burst should be denied")

	// Wait for token refill
	time.Sleep(150 * time.Millisecond)

	// Should allow again
	allowed = limiter.Allow(ctx, "peer-001", "method")
	assert.True(t, allowed, "request after refill should be allowed")
}

// TestConcurrencyLimiter tests concurrent request limiting
func TestConcurrencyLimiter(t *testing.T) {
	limiter := NewConcurrencyLimiter(3) // Max 3 concurrent
	ctx := context.Background()

	// First 3 should succeed
	for i := 0; i < 3; i++ {
		allowed := limiter.Allow(ctx, "peer-001", "method")
		assert.True(t, allowed, "concurrent requests up to limit should be allowed")
	}

	// 4th should fail
	allowed := limiter.Allow(ctx, "peer-001", "method")
	assert.False(t, allowed, "concurrent request beyond limit should be denied")

	// Release one
	limiter.Release(ctx, "peer-001", "method")

	// Should allow again
	allowed = limiter.Allow(ctx, "peer-001", "method")
	assert.True(t, allowed, "request after release should be allowed")

	// Cleanup
	limiter.Release(ctx, "peer-001", "method")
	limiter.Release(ctx, "peer-001", "method")
	limiter.Release(ctx, "peer-001", "method")
}

// TestPerPeerLimiting tests that limits are per-peer
func TestPerPeerLimiting(t *testing.T) {
	limiter := NewTokenBucketLimiter(5, 5)
	ctx := context.Background()

	// Exhaust limit for peer-001
	for i := 0; i < 5; i++ {
		limiter.Allow(ctx, "peer-001", "method")
	}
	assert.False(t, limiter.Allow(ctx, "peer-001", "method"))

	// peer-002 should have its own limit
	allowed := limiter.Allow(ctx, "peer-002", "method")
	assert.True(t, allowed, "different peer should have separate limit")
}

// TestRateLimiterConcurrent tests concurrent access to rate limiter
func TestRateLimiterConcurrent(t *testing.T) {
	limiter := NewTokenBucketLimiter(100, 100)
	ctx := context.Background()

	var wg sync.WaitGroup
	allowedCount := 0
	deniedCount := 0
	var mu sync.Mutex

	// Launch concurrent requests
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if limiter.Allow(ctx, "peer-001", "method") {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			} else {
				mu.Lock()
				deniedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	t.Logf("Allowed: %d, Denied: %d", allowedCount, deniedCount)
	assert.Equal(t, 200, allowedCount+deniedCount)
	assert.LessOrEqual(t, allowedCount, 100, "should not exceed burst limit")
}

// TestNoOpLimiter tests no-op limiter
func TestNoOpLimiter(t *testing.T) {
	limiter := NewNoOpLimiter()
	ctx := context.Background()

	// Should always allow
	for i := 0; i < 100; i++ {
		allowed := limiter.Allow(ctx, "peer", "method")
		assert.True(t, allowed, "no-op limiter should always allow")
	}

	// Release should be safe
	limiter.Release(ctx, "peer", "method")
}
