package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// TestBackfillEntityIndex_FromCachedClaims — the deterministic backfill derives
// entity_mentions from claims already cached in metadata (no LLM), so a queried
// entity resolves to the items whose claims mention it.
func TestBackfillEntityIndex_FromCachedClaims(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	note := &store.KnowledgeItem{
		ContentID:      "note-1",
		SourceType:     store.SourceTypeNote,
		Title:          "Compte rendu",
		NormalizedText: "…",
		VersionID:      "note-1-v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Metadata: map[string]any{
			"extracted_claims": []contradict.Claim{
				{Entity: "Acme SARL", Attribute: "contract", Value: "signed", Polarity: "affirm", Quote: "…"},
			},
		},
	}
	if err := db.InsertKnowledgeItem(ctx, note); err != nil {
		t.Fatalf("insert: %v", err)
	}

	ing := NewIngestorWithStore(db)
	n, err := ing.BackfillEntityIndex(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n < 1 {
		t.Fatalf("scanned %d items, want ≥1", n)
	}

	cids, err := db.EntityMentionContentIDs(ctx, []string{contradict.NormKey("Acme SARL")}, 10)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(cids) != 1 || cids[0] != "note-1" {
		t.Errorf("entity index → %v, want [note-1]", cids)
	}
}
