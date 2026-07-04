package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

// A re-extraction pass must never replace a clean extraction with a worse
// (garbled) one. When the same source_ref is re-pushed with LOWER
// extract_confidence, the existing item is kept ("kept_better"); a HIGHER
// confidence re-push replaces in place ("updated"). This is the fail-closed
// backstop that fixed the garbled TARA item without risking a good one.
func TestIngestText_KeepBetterByConfidence(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	ing := NewIngestorWithEmbeddings(db, nil)
	ctx := context.Background()

	ref := "proton:msg1:att:contract.pdf"

	// 1) A CLEAN extraction lands first (high confidence).
	clean := IngestTextInput{
		Title:      "Contract",
		Text:       "Daily rate: 600 EUR agreed with ACME Gaming Ltd for John Doe.",
		SourceType: "file",
		SourceRef:  ref,
		Metadata:   map[string]any{"extract_confidence": 0.95, "extract_method": "pdftotext"},
	}
	if r, err := ing.IngestText(ctx, clean); err != nil || r.Status != "indexed" {
		t.Fatalf("clean ingest = %+v err=%v, want indexed", r, err)
	}

	// 2) A GARBLED re-extraction of the same ref (low confidence) must NOT win.
	garbled := clean
	garbled.Text = "D a i l y r a t e 6 0 0 E U R A C M E G a m i n g"
	garbled.Metadata = map[string]any{"extract_confidence": 0.02, "extract_method": "ledongthuc"}
	r2, err := ing.IngestText(ctx, garbled)
	if err != nil {
		t.Fatalf("garbled re-push: %v", err)
	}
	if r2.Status != "kept_better" {
		t.Errorf("garbled re-push status = %q, want kept_better", r2.Status)
	}
	got, _ := db.GetKnowledgeItemBySourceRef(ctx, ref)
	if got == nil || !strings.Contains(got.NormalizedText, "600 eur") {
		t.Errorf("stored item was downgraded to garbage: %q", got.NormalizedText)
	}

	// 3) A BETTER re-extraction (higher confidence) DOES replace in place.
	better := clean
	better.Text = "Daily rate: 600 EUR agreed with ACME Gaming Ltd for John Doe (signed)."
	better.Metadata = map[string]any{"extract_confidence": 0.98, "extract_method": "pdftotext"}
	r3, err := ing.IngestText(ctx, better)
	if err != nil {
		t.Fatalf("better re-push: %v", err)
	}
	if r3.Status != "updated" {
		t.Errorf("better re-push status = %q, want updated", r3.Status)
	}
	got2, _ := db.GetKnowledgeItemBySourceRef(ctx, ref)
	if got2 == nil || !strings.Contains(got2.NormalizedText, "signed") {
		t.Errorf("higher-confidence extraction did not replace: %q", got2.NormalizedText)
	}
}
