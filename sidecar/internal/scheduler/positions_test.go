package scheduler

import (
	"context"
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func TestPositionsFingerprint(t *testing.T) {
	a := &store.Decision{ID: "decision:a", Statement: "Use local-first", DecidedOn: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"}
	b := &store.Decision{ID: "decision:b", Statement: "Bank with X", DecidedOn: "2026-02-01T00:00:00Z", UpdatedAt: "2026-02-01T00:00:00Z"}

	// Order-independent: the same set yields the same fingerprint.
	if positionsFingerprint([]*store.Decision{a, b}) != positionsFingerprint([]*store.Decision{b, a}) {
		t.Error("fingerprint must be order-independent")
	}
	// A new decision changes it.
	c := &store.Decision{ID: "decision:c", Statement: "Hire an accountant", DecidedOn: "2026-03-01T00:00:00Z", UpdatedAt: "2026-03-01T00:00:00Z"}
	if positionsFingerprint([]*store.Decision{a, b}) == positionsFingerprint([]*store.Decision{a, b, c}) {
		t.Error("adding a decision must change the fingerprint")
	}
	// Editing a decision (updated_at moves) changes it — triggers regeneration.
	bEdited := &store.Decision{ID: "decision:b", Statement: "Bank with Y", DecidedOn: "2026-02-01T00:00:00Z", UpdatedAt: "2026-04-01T00:00:00Z"}
	if positionsFingerprint([]*store.Decision{a, b}) == positionsFingerprint([]*store.Decision{a, bEdited}) {
		t.Error("editing a decision must change the fingerprint")
	}
}

func TestPositionsSynopsisNilSafe(t *testing.T) {
	var d *DailyBrief
	if d.PositionsSynopsis(context.Background(), true) != "" {
		t.Error("nil DailyBrief must return empty, not panic")
	}
}
