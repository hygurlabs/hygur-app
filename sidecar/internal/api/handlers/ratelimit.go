package handlers

import (
	"sync"
	"time"
)

// rateLimiter is a minimal token bucket: it refills perMin tokens per minute up
// to a burst of perMin, and Allow() is non-blocking. A cloud tenant is a single
// process, so a process-wide limiter is effectively a per-tenant request-rate
// fuse — a fast guard against a runaway client loop, complementing the slower
// monthly token cap.
type rateLimiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	perSec float64
	last   time.Time
}

func newRateLimiter(perMin int) *rateLimiter {
	return &rateLimiter{
		tokens: float64(perMin),
		max:    float64(perMin),
		perSec: float64(perMin) / 60.0,
		last:   time.Now(),
	}
}

// Allow reports whether a request may proceed now, consuming one token.
func (l *rateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.perSec
	if l.tokens > l.max {
		l.tokens = l.max
	}
	l.last = now
	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}
