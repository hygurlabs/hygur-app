package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestTokenUsageDailySince_AndDump covers the admin-cost read path: per-day rows
// for run-rate, and the read-only DumpTokenUsage (no migrations) used in-pod.
func TestTokenUsageDailySince_AndDump(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "u.db")
	const key = "usage-dump-key"

	db, err := NewDBWithKey(path, key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if err := db.RecordTokenUsage(ctx, TokenCategoryChat, 100, 50); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordTokenUsage(ctx, TokenCategoryEmbedding, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPricing(ctx, Pricing{ChatInPer1M: 1, ChatOutPer1M: 2, IngestPer1M: 0.1, Currency: "€"}); err != nil {
		t.Fatal(err)
	}

	rows, err := db.TokenUsageDailySince(ctx, "2000-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("daily rows: want 2, got %d", len(rows))
	}
	_ = db.Close()

	// Read-only dump (DB now closed) reproduces the rows + pricing, no migrations.
	days, pricing, err := DumpTokenUsage(ctx, path, key, "2000-01-01")
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	if len(days) != 2 {
		t.Fatalf("dump rows: want 2, got %d", len(days))
	}
	if pricing.ChatOutPer1M != 2 {
		t.Fatalf("dump pricing: got %+v", pricing)
	}
}
