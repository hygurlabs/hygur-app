package handlers

import "testing"

// TestRateLimiter_Burst: a fresh bucket allows exactly `perMin` immediate
// requests (the burst), then denies — the refill within one instant is
// negligible, so this is deterministic.
func TestRateLimiter_Burst(t *testing.T) {
	l := newRateLimiter(3)
	for i := 0; i < 3; i++ {
		if !l.Allow() {
			t.Fatalf("request %d should be allowed (burst)", i+1)
		}
	}
	if l.Allow() {
		t.Fatal("4th request should be denied (bucket empty)")
	}
}
