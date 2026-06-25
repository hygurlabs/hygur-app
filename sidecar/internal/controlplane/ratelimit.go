package controlplane

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rateLimiter is a tiny in-memory, mutex-guarded fixed-window limiter. The
// control plane is a single process (`hygur-console serve`), so per-process
// counters are sufficient — no external store. It guards the credential and
// ingest endpoints against brute-force / flooding; limits are deliberately
// generous so a normal user never hits them.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]*window
	now    func() time.Time
}

type window struct {
	count int
	reset time.Time
}

// newRateLimiter builds a limiter of `limit` events per `window` per key, with a
// background sweeper that drops expired entries so the map can't grow unbounded.
func newRateLimiter(limit int, win time.Duration) *rateLimiter {
	rl := &rateLimiter{limit: limit, window: win, hits: map[string]*window{}, now: time.Now}
	go rl.sweepLoop()
	return rl
}

// allow records one event for key and reports whether it's within the window
// budget. Over the limit → false (caller returns 429).
func (rl *rateLimiter) allow(key string) bool {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	w := rl.hits[key]
	if w == nil || !w.reset.After(now) {
		rl.hits[key] = &window{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if w.count >= rl.limit {
		return false
	}
	w.count++
	return true
}

// sweepLoop periodically removes expired windows. Runs for the process lifetime.
func (rl *rateLimiter) sweepLoop() {
	t := time.NewTicker(rl.window)
	defer t.Stop()
	for range t.C {
		now := rl.now()
		rl.mu.Lock()
		for k, w := range rl.hits {
			if !w.reset.After(now) {
				delete(rl.hits, k)
			}
		}
		rl.mu.Unlock()
	}
}

// clientIP extracts the remote host (no port) as the rate-limit key.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
