package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

// IngestText must index supplied text and be idempotent by source_ref:
// identical re-push is a no-op ("duplicate"); changed text "updates" in place
// keeping the same content_id (no duplication).
func TestIngestText_IndexesAndIsIdempotent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	ing := NewIngestorWithEmbeddings(db, nil) // nil llm -> chunks without vectors, fine here
	ctx := context.Background()

	in := IngestTextInput{
		Title:      "Declaration TVA Q1",
		Text:       "Premiere ligne du document.\n\nSeconde ligne avec assez de contenu pour produire des sections.",
		SourceType: "file",
		SourceRef:  "files:/tmp/decl.txt",
	}

	r1, err := ing.IngestText(ctx, in)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if r1.Status != "indexed" || r1.ContentID == "" {
		t.Fatalf("first ingest = %+v, want indexed with a content_id", r1)
	}

	r2, err := ing.IngestText(ctx, in)
	if err != nil {
		t.Fatalf("re-ingest identical: %v", err)
	}
	if r2.Status != "duplicate" || r2.ContentID != r1.ContentID {
		t.Errorf("identical re-push = %+v, want duplicate with content_id %s", r2, r1.ContentID)
	}

	in2 := in
	in2.Text = "Contenu totalement different, mis a jour."
	r3, err := ing.IngestText(ctx, in2)
	if err != nil {
		t.Fatalf("re-ingest changed: %v", err)
	}
	if r3.Status != "updated" {
		t.Errorf("changed re-push status = %q, want updated", r3.Status)
	}
	if r3.ContentID != r1.ContentID {
		t.Errorf("content_id changed on update: %s != %s", r3.ContentID, r1.ContentID)
	}

	got, err := db.GetKnowledgeItemBySourceRef(ctx, in.SourceRef)
	if err != nil || got == nil {
		t.Fatalf("lookup by source_ref: item=%v err=%v", got, err)
	}
	if !strings.Contains(got.NormalizedText, "mis a jour") {
		t.Errorf("item was not updated in place: %q", got.NormalizedText)
	}
}
