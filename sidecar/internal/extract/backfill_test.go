package extract

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func setupBackfillDB(t *testing.T, n int) *store.DB {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	now := time.Now()
	for i := 0; i < n; i++ {
		ki := &store.KnowledgeItem{
			ContentID:      "doc-" + intToStr(i),
			SourceType:     "markdown",
			Title:          "Doc " + intToStr(i),
			NormalizedText: "Cher client,\nMontant : 1234,56 EUR\nIBAN : BE68 5390 0754 7034",
			Metadata:       map[string]any{},
			VersionID:      "v1",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := db.InsertKnowledgeItem(context.Background(), ki); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	return db
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestBackfill_Tier1OnlyEnrichesAllItems(t *testing.T) {
	db := setupBackfillDB(t, 3)
	defer db.Close()

	stats, err := Backfill(context.Background(), db, nil, BackfillOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("Total = %d, want 3", stats.Total)
	}
	if stats.UpdatedTier1 != 3 {
		t.Errorf("UpdatedTier1 = %d, want 3", stats.UpdatedTier1)
	}
	if stats.UpdatedTier2 != 0 {
		t.Errorf("UpdatedTier2 = %d, want 0 (no llm client)", stats.UpdatedTier2)
	}

	// Verify Tier 1 metadata persisted on a sample item.
	item, err := db.GetKnowledgeItem(context.Background(), "doc-1")
	if err != nil || item == nil {
		t.Fatalf("get item: %v", err)
	}
	for _, key := range []string{"extracted_iban", "extracted_amounts"} {
		if _, ok := item.Metadata[key]; !ok {
			t.Errorf("metadata[%q] missing on doc-1", key)
		}
	}
}

func TestBackfill_DryRunDoesNotPersist(t *testing.T) {
	db := setupBackfillDB(t, 2)
	defer db.Close()

	stats, err := Backfill(context.Background(), db, nil, BackfillOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if stats.UpdatedTier1 != 2 {
		t.Errorf("UpdatedTier1 = %d, want 2 (counter increments even in dry-run)", stats.UpdatedTier1)
	}

	item, _ := db.GetKnowledgeItem(context.Background(), "doc-0")
	if _, ok := item.Metadata["extracted_iban"]; ok {
		t.Errorf("dry-run should not persist; metadata = %v", item.Metadata)
	}
}

func TestBackfill_Tier2RunsWithLLMClient(t *testing.T) {
	db := setupBackfillDB(t, 2)
	defer db.Close()

	srv := fakeLLMServer(t, `{"persons":["Alice"],"topics":["TVA"]}`)
	defer srv.Close()
	client := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())

	stats, err := Backfill(context.Background(), db, client, BackfillOptions{BatchSize: 10})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if stats.UpdatedTier2 != 2 {
		t.Errorf("UpdatedTier2 = %d, want 2", stats.UpdatedTier2)
	}

	item, _ := db.GetKnowledgeItem(context.Background(), "doc-0")
	if _, ok := item.Metadata["extracted_persons"]; !ok {
		t.Errorf("extracted_persons missing on doc-0")
	}
	if v, ok := item.Metadata["extracted_v2_version"].(string); !ok || v != Tier2Version {
		t.Errorf("extracted_v2_version = %v, want %s", item.Metadata["extracted_v2_version"], Tier2Version)
	}
}

func TestBackfill_SkipsAlreadyProcessedItems(t *testing.T) {
	db := setupBackfillDB(t, 2)
	defer db.Close()

	// First run — populate Tier 2.
	srv1 := fakeLLMServer(t, `{"persons":["Alice"]}`)
	client1 := llm.NewClientWithHTTP(srv1.URL, 5*time.Second, 0, srv1.Client())
	if _, err := Backfill(context.Background(), db, client1, BackfillOptions{}); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	srv1.Close()

	// Second run — Tier 2 should be skipped because version stamp matches.
	srv2 := fakeLLMServer(t, `{"persons":["should-not-overwrite"]}`)
	defer srv2.Close()
	client2 := llm.NewClientWithHTTP(srv2.URL, 5*time.Second, 0, srv2.Client())

	stats, err := Backfill(context.Background(), db, client2, BackfillOptions{})
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if stats.UpdatedTier2 != 0 {
		t.Errorf("UpdatedTier2 = %d on rerun, want 0 (idempotency)", stats.UpdatedTier2)
	}
	if stats.SkippedV2 != 2 {
		t.Errorf("SkippedV2 = %d, want 2", stats.SkippedV2)
	}

	// Verify the original Tier 2 data is intact.
	item, _ := db.GetKnowledgeItem(context.Background(), "doc-0")
	persons, _ := item.Metadata["extracted_persons"].([]any)
	if len(persons) != 1 || persons[0] != "Alice" {
		t.Errorf("extracted_persons should be untouched; got %v", item.Metadata["extracted_persons"])
	}
}

func TestBackfill_LLMErrorsAreCounted(t *testing.T) {
	db := setupBackfillDB(t, 2)
	defer db.Close()

	srv := errorLLMServer(t)
	defer srv.Close()
	client := llm.NewClientWithHTTP(srv.URL, 1*time.Second, 0, srv.Client())

	stats, err := Backfill(context.Background(), db, client, BackfillOptions{})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if stats.Errors != 2 {
		t.Errorf("Errors = %d, want 2", stats.Errors)
	}
	if stats.UpdatedTier2 != 0 {
		t.Errorf("UpdatedTier2 = %d, want 0", stats.UpdatedTier2)
	}
}
