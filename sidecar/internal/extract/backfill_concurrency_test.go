package extract

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// Backfill with Concurrency > 1 must process every item exactly once (no drops, no
// double-counts) and stay race-free (run with -race). Uses Tier-1 only (nil llmClient
// forces SkipTier2), so no LLM is needed.
func TestBackfillConcurrent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	const n = 50
	for i := 0; i < n; i++ {
		id := "k" + strconv.Itoa(i)
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: id, SourceType: store.SourceTypeNote, Title: id,
			NormalizedText: "Call 06 12 34 56 78 before 2026-08-01.", VersionID: "v1",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	// BatchSize < n so multiple batches run; Concurrency 4 parallelizes each batch.
	stats, err := Backfill(ctx, db, nil, BackfillOptions{Concurrency: 4, BatchSize: 8})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Total != n {
		t.Errorf("total = %d, want %d (every item processed exactly once)", stats.Total, n)
	}
	if stats.Errors != 0 {
		t.Errorf("errors = %d, want 0", stats.Errors)
	}
}
