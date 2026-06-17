package retrieval

import (
	"testing"
	"time"
)

func TestAttentionMultiplier(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-24 * time.Hour)
	old := now.Add(-25 * 24 * time.Hour)
	stale := now.Add(-60 * 24 * time.Hour) // outside the recency window

	// Never accessed → identity.
	if m := attentionMultiplier(0, time.Time{}, now); m != 1.0 {
		t.Errorf("hitCount 0 must be identity, got %v", m)
	}

	// Any boost is bounded to [1, 1+attentionMaxBoost].
	hot := attentionMultiplier(1000, recent, now)
	if hot <= 1.0 || hot > 1.0+attentionMaxBoost+1e-9 {
		t.Errorf("boost out of bounds: %v (max %v)", hot, 1.0+attentionMaxBoost)
	}

	// More hits → larger boost (monotonic in frequency, same recency).
	if attentionMultiplier(10, recent, now) <= attentionMultiplier(1, recent, now) {
		t.Error("boost must increase with hit_count")
	}

	// More recent → larger boost (same frequency).
	if attentionMultiplier(5, recent, now) <= attentionMultiplier(5, old, now) {
		t.Error("boost must increase with recency")
	}

	// Stale access (outside the window) still gives a frequency-only boost, but less
	// than a recent one, and never below identity.
	st := attentionMultiplier(5, stale, now)
	if st <= 1.0 {
		t.Errorf("a frequent-but-stale item should still get a small boost, got %v", st)
	}
	if st >= attentionMultiplier(5, recent, now) {
		t.Error("stale must boost less than recent")
	}
}
