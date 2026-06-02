package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// fakeEmbedder records the chunks handed to it instead of calling a model.
type fakeEmbedder struct{ got []store.Chunk }

func (f *fakeEmbedder) BatchEmbedAndStore(_ context.Context, chunks []store.Chunk) error {
	f.got = append(f.got, chunks...)
	return nil
}

func newTestItem(db *store.DB, t *testing.T, contentID string) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	if err := db.InsertKnowledgeItem(context.Background(), &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "markdown",
		Title:          "doc",
		NormalizedText: "x",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}
}

func TestIndexSectionsEndToEnd(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	newTestItem(db, t, "ki-1")

	doc := "# Recharges\nIntro.\n\n## Avril 2026\nRecharge du 3 avril : 42 kWh facturés.\n\n## Mai 2026\nRecharge de mai."
	emb := &fakeEmbedder{}

	secCount, chunkCount, err := IndexSections(ctx, db, emb, "ki-1", doc, 512, time.Now())
	if err != nil {
		t.Fatalf("IndexSections: %v", err)
	}
	if secCount != 3 { // H1 + 2×H2 (no preamble: text starts with a heading)
		t.Fatalf("section count = %d, want 3", secCount)
	}
	if chunkCount == 0 || len(emb.got) != chunkCount {
		t.Fatalf("chunkCount=%d but embedder got %d", chunkCount, len(emb.got))
	}

	// Sections persisted, with hierarchy.
	secs, err := db.GetSectionsByContentID(ctx, "ki-1")
	if err != nil {
		t.Fatalf("GetSectionsByContentID: %v", err)
	}
	if len(secs) != 3 {
		t.Fatalf("persisted sections = %d, want 3", len(secs))
	}

	// Chunks persisted and linked to a section.
	chunks, err := db.GetChunksByContentID(ctx, "ki-1")
	if err != nil {
		t.Fatalf("GetChunksByContentID: %v", err)
	}
	if len(chunks) != chunkCount {
		t.Fatalf("persisted chunks = %d, want %d", len(chunks), chunkCount)
	}
	for _, c := range chunks {
		if c.SectionID == nil {
			t.Fatalf("chunk %s has no section_id", c.ChunkID)
		}
	}

	// FTS (trigger-fed) finds the April detail.
	hits, err := db.SearchChunksFTS(ctx, "avril 42 kwh", 10)
	if err != nil {
		t.Fatalf("SearchChunksFTS: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected a BM25 hit for 'avril 42 kwh'")
	}
}

func TestIndexSectionsReindexIdempotent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	newTestItem(db, t, "ki-2")

	doc := "# A\nalpha\n\n# B\nbeta"
	if _, _, err := IndexSections(ctx, db, &fakeEmbedder{}, "ki-2", doc, 512, time.Now()); err != nil {
		t.Fatalf("first index: %v", err)
	}
	// Re-index the SAME document: counts must not accumulate.
	if _, _, err := IndexSections(ctx, db, &fakeEmbedder{}, "ki-2", doc, 512, time.Now()); err != nil {
		t.Fatalf("re-index: %v", err)
	}

	secs, _ := db.GetSectionsByContentID(ctx, "ki-2")
	if len(secs) != 2 {
		t.Fatalf("after re-index, sections = %d, want 2 (no accumulation)", len(secs))
	}
	chunks, _ := db.GetChunksByContentID(ctx, "ki-2")
	if len(chunks) != 2 {
		t.Fatalf("after re-index, chunks = %d, want 2", len(chunks))
	}
	// FTS must not hold stale duplicates either.
	hits, _ := db.SearchChunksFTS(ctx, "alpha", 10)
	if len(hits) != 1 {
		t.Fatalf("FTS 'alpha' hits = %d, want 1 (no stale rows)", len(hits))
	}
}
