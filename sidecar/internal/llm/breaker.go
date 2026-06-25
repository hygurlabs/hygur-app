package llm

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Circuit-breaker defaults for the chat/inference path. Tuned for graceful
// degradation, not precision: trip after a few consecutive outages, then probe
// again after a short cooldown so recovery is automatic.
const (
	defaultBreakerThreshold = 3                // consecutive outage failures to open
	defaultBreakerCooldown  = 30 * time.Second // fast-fail window before a probe
)

// circuitBreaker is a minimal closed/open breaker for the inference endpoint.
// After `threshold` consecutive outages it opens for `cooldown`, during which
// calls fast-fail (the caller returns ErrLLMUnavailable) instead of hammering a
// dead backend; once the cooldown elapses one call is allowed through as a probe
// — a clean completion closes it again. Safe for concurrent use. A nil or
// zero-threshold breaker is disabled (always allows).
type circuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	fails     int
	openUntil time.Time
}

func newCircuitBreaker() *circuitBreaker {
	// Kill-switch: HYGUR_LLM_BREAKER_DISABLE=1/true → threshold 0 = always allow
	// (never trips), for debugging or if the breaker ever misfires.
	if v := strings.TrimSpace(os.Getenv("HYGUR_LLM_BREAKER_DISABLE")); v == "1" || strings.EqualFold(v, "true") {
		return &circuitBreaker{}
	}
	return &circuitBreaker{threshold: defaultBreakerThreshold, cooldown: defaultBreakerCooldown}
}

// allow reports whether a call may proceed at time now (false while open).
func (b *circuitBreaker) allow(now time.Time) bool {
	if b == nil || b.threshold <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return !now.Before(b.openUntil)
}

// record updates the breaker after a call. ok=true (a clean completion) resets
// it; ok=false (an outage — a transport error or retryable 5xx that exhausted
// retries) increments the failure count and opens the breaker at the threshold.
func (b *circuitBreaker) record(ok bool, now time.Time) {
	if b == nil || b.threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ok {
		b.fails = 0
		b.openUntil = time.Time{}
		return
	}
	b.fails++
	if b.fails >= b.threshold {
		b.openUntil = now.Add(b.cooldown)
	}
}
