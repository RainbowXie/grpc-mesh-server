package server

import (
	"context"
	"sync"
	"time"
)

// RateLimiter interface
type RateLimiter interface {
	Allow(ctx context.Context, peerID, method string) bool
	Release(ctx context.Context, peerID, method string)
}

// TokenBucketLimiter implements token bucket algorithm
type TokenBucketLimiter struct {
	rate  int
	burst int
	
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    int
	lastRefill time.Time
}

// NewTokenBucketLimiter creates a new token bucket limiter
func NewTokenBucketLimiter(rate, burst int) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*bucket),
	}
}

func (tbl *TokenBucketLimiter) Allow(ctx context.Context, peerID, method string) bool {
	key := peerID + ":" + method
	
	tbl.mu.Lock()
	defer tbl.mu.Unlock()
	
	b, exists := tbl.buckets[key]
	if !exists {
		b = &bucket{
			tokens:    tbl.burst,
			lastRefill: time.Now(),
		}
		tbl.buckets[key] = b
	}
	
	// Refill tokens
	now := time.Now()
	elapsed := now.Sub(b.lastRefill)
	tokensToAdd := int(elapsed.Seconds() * float64(tbl.rate))
	
	if tokensToAdd > 0 {
		b.tokens += tokensToAdd
		if b.tokens > tbl.burst {
			b.tokens = tbl.burst
		}
		b.lastRefill = now
	}
	
	// Check and consume
	if b.tokens > 0 {
		b.tokens--
		return true
	}
	
	return false
}

func (tbl *TokenBucketLimiter) Release(ctx context.Context, peerID, method string) {
	// Token bucket doesn't need explicit release
}

// ConcurrencyLimiter limits concurrent requests
type ConcurrencyLimiter struct {
	maxConcurrent int
	
	mu      sync.Mutex
	current map[string]int
}

// NewConcurrencyLimiter creates a new concurrency limiter
func NewConcurrencyLimiter(max int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		maxConcurrent: max,
		current:       make(map[string]int),
	}
}

func (cl *ConcurrencyLimiter) Allow(ctx context.Context, peerID, method string) bool {
	key := peerID + ":" + method
	
	cl.mu.Lock()
	defer cl.mu.Unlock()
	
	if cl.current[key] < cl.maxConcurrent {
		cl.current[key]++
		return true
	}
	
	return false
}

func (cl *ConcurrencyLimiter) Release(ctx context.Context, peerID, method string) {
	key := peerID + ":" + method
	
	cl.mu.Lock()
	defer cl.mu.Unlock()
	
	if cl.current[key] > 0 {
		cl.current[key]--
	}
}

// NoOpLimiter doesn't limit anything
type NoOpLimiter struct{}

func NewNoOpLimiter() RateLimiter {
	return &NoOpLimiter{}
}

func (nol *NoOpLimiter) Allow(ctx context.Context, peerID, method string) bool {
	return true
}

func (nol *NoOpLimiter) Release(ctx context.Context, peerID, method string) {
}
