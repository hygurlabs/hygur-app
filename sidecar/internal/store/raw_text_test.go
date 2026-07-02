package store

import (
	"context"
	"testing"
	"time"
)

// TestKnowledgeItemDisplayText covers the raw-with-fallback selector directly.
func TestKnowledgeItemDisplayText(t *testing.T) {
	withRaw := &KnowledgeItem{NormalizedText: "650€ ores mission x", RawText: "650€\nORES\nmission X"}
	if got := withRaw.DisplayText(); got != "650€\nORES\nmission X" {
		t.Errorf("DisplayText() with raw = %q, want the raw text", got)
	}
	// Pre-raw_text item: empty RawText falls back to NormalizedText.
	noRaw := &KnowledgeItem{NormalizedText: "650€ ores mission x"}
	if got := noRaw.DisplayText(); got != "650€ ores mission x" {
		t.Errorf("DisplayText() without raw = %q, want the normalized text", got)
	}
}

// TestRawTextRoundTrip verifies both columns survive an Insert→Get, with the raw
// text keeping line breaks + case and the normalized text staying collapsed.
func TestRawTextRoundTrip(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now()
	item := &KnowledgeItem{
		ContentID:      "note:raw-1",
		SourceType:     "note",
		Title:          "Facture",
		NormalizedText: "650€ ores mission x",
		RawText:        "650€\nORES\nmission X",
		Metadata:       map[string]any{},
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}

	got, err := db.GetKnowledgeItem(ctx, "note:raw-1")
	if err != nil {
		t.Fatalf("GetKnowledgeItem: %v", err)
	}
	if got.RawText != "650€\nORES\nmission X" {
		t.Errorf("RawText = %q, want line breaks + case preserved", got.RawText)
	}
	if got.NormalizedText != "650€ ores mission x" {
		t.Errorf("NormalizedText = %q, want collapsed/lowercased", got.NormalizedText)
	}
	if got.DisplayText() != "650€\nORES\nmission X" {
		t.Errorf("DisplayText() = %q, want raw", got.DisplayText())
	}
}

// TestRawTextNullFallback simulates a row ingested before the raw_text column
// existed (raw_text IS NULL): Get must not crash and DisplayText falls back to
// normalized_text.
func TestRawTextNullFallback(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	// Insert a legacy-shaped row with raw_text left NULL, bypassing the typed
	// writer (which always sets raw_text).
	if _, err := db.SQLDB().ExecContext(ctx, `
		INSERT INTO knowledge_items (content_id, source_type, title, normalized_text, raw_text, version_id, created_at, updated_at)
		VALUES ('note:legacy', 'note', 'Old', 'old note body', NULL, 'v1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	got, err := db.GetKnowledgeItem(ctx, "note:legacy")
	if err != nil {
		t.Fatalf("GetKnowledgeItem (NULL raw_text) crashed: %v", err)
	}
	if got.RawText != "" {
		t.Errorf("RawText = %q, want empty for a NULL column", got.RawText)
	}
	if got.DisplayText() != "old note body" {
		t.Errorf("DisplayText() = %q, want fallback to normalized_text", got.DisplayText())
	}
}
