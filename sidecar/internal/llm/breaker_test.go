package llm

import (
	"testing"
	"time"
)

func TestCircuitBreaker(t *testing.T) {
	b := newCircuitBreaker()
	base := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	if !b.allow(base) {
		t.Fatal("a fresh breaker must allow")
	}
	// Failures below the threshold keep it closed.
	for i := 0; i < defaultBreakerThreshold-1; i++ {
		b.record(false, base)
	}
	if !b.allow(base) {
		t.Fatal("below threshold the breaker must stay closed")
	}
	// The threshold failure opens it.
	b.record(false, base)
	if b.allow(base) {
		t.Fatal("at the threshold the breaker must be open")
	}
	// It stays open during the cooldown, then allows a probe.
	if b.allow(base.Add(defaultBreakerCooldown - time.Second)) {
		t.Fatal("must stay open during the cooldown")
	}
	if !b.allow(base.Add(defaultBreakerCooldown + time.Second)) {
		t.Fatal("must allow a probe once the cooldown elapses")
	}
	// A clean completion closes it again.
	probe := base.Add(defaultBreakerCooldown + time.Second)
	b.record(true, probe)
	if !b.allow(probe.Add(time.Second)) {
		t.Fatal("a success must close the breaker")
	}

	// A zero-threshold breaker is disabled and always allows.
	d := &circuitBreaker{}
	d.record(false, base)
	d.record(false, base)
	d.record(false, base)
	if !d.allow(base) {
		t.Fatal("a disabled (zero-threshold) breaker must always allow")
	}
}
