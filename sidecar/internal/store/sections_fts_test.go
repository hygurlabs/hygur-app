package store

import (
	"context"
	"testing"
	"time"
)

// TestMigrationV9Schema verifies the fresh-DB migration path lands on schema
// version 9 and creates the sections + chunks_fts objects.
func TestMigrationV9Schema(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	v, err := db.GetSchemaVersion()
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != CurrentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, CurrentSchemaVersion)
	}
	// sections + chunks_fts were introduced at v9, so the schema must be at
	// least that. (Later migrations — e.g. v10 chat_sessions — may bump it.)
	if CurrentSchemaVersion < 9 {
		t.Fatalf("sections/fts5 require schema version ≥ 9, got %d", CurrentSchemaVersion)
	}

	// sections table and chunks_fts virtual table must both exist.
	for _, name := range []string{"sections", "chunks_fts"} {
		var got string
		err := db.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE name = ?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("expected object %q to exist: %v", name, err)
		}
	}
}

// TestEnsureRAGSchemaOnHigherVersionDB reproduces the field bug where a DB
// carries a higher schema_version from an abandoned migration lineage (v16) and
// is missing the RAG tables: applyMigrations skips our v9, but ensureRAGSchema
// must still (re)create sections + chunks_fts so indexing doesn't break with
// "no such table: sections".
func TestEnsureRAGSchemaOnHigherVersionDB(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	// Simulate the abandoned-lineage state: drop the RAG objects and record a
	// schema_version well above ours.
	if _, err := db.db.Exec("DROP TABLE IF EXISTS chunks_fts"); err != nil {
		t.Fatalf("drop chunks_fts: %v", err)
	}
	if _, err := db.db.Exec("DROP TABLE IF EXISTS sections"); err != nil {
		t.Fatalf("drop sections: %v", err)
	}
	if _, err := db.db.Exec("INSERT INTO schema_version (version) VALUES (16)"); err != nil {
		t.Fatalf("bump version: %v", err)
	}

	// The recovery path must recreate the tables idempotently.
	if err := ensureRAGSchema(db.db); err != nil {
		t.Fatalf("ensureRAGSchema: %v", err)
	}
	for _, name := range []string{"sections", "chunks_fts"} {
		var got string
		if err := db.db.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, name).Scan(&got); err != nil {
			t.Fatalf("%q not recreated after ensureRAGSchema: %v", name, err)
		}
	}
	// And it must be safe to run again (idempotent).
	if err := ensureRAGSchema(db.db); err != nil {
		t.Fatalf("ensureRAGSchema (second run): %v", err)
	}
}

// TestSectionCRUDAndFTS exercises the Phase 1 storage layer end-to-end:
// section round-trip, the chunk->FTS sync trigger, BM25 search, the
// chunk.section_id link, and trigger-driven cleanup on delete.
func TestSectionCRUDAndFTS(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	item := &KnowledgeItem{
		ContentID:      "ki-recharges",
		SourceType:     "markdown",
		Title:          "Recharges véhicule",
		NormalizedText: "…",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}

	// Section round-trip.
	sec := &Section{
		SectionID:   "sec-avril",
		ContentID:   "ki-recharges",
		Heading:     "Avril 2026",
		HeadingPath: []string{"Recharges véhicule", "Avril 2026"},
		Level:       2,
		Ordinal:     3,
		FullText:    "Détail complet des recharges d'avril 2026.",
		TokenCount:  9,
		Metadata:    map[string]any{"month": "2026-04"},
	}
	if err := db.InsertSection(ctx, sec); err != nil {
		t.Fatalf("InsertSection: %v", err)
	}
	got, err := db.GetSection(ctx, "sec-avril")
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if got == nil {
		t.Fatal("GetSection returned nil")
	}
	if got.Heading != "Avril 2026" || got.Level != 2 || got.Ordinal != 3 {
		t.Errorf("section fields mismatch: %+v", got)
	}
	if len(got.HeadingPath) != 2 || got.HeadingPath[1] != "Avril 2026" {
		t.Errorf("heading_path round-trip failed: %v", got.HeadingPath)
	}
	if got.Metadata["month"] != "2026-04" {
		t.Errorf("metadata round-trip failed: %v", got.Metadata)
	}

	// Chunk insert must (a) carry section_id and (b) auto-populate chunks_fts
	// via the AFTER INSERT trigger — no manual rebuild needed.
	secID := "sec-avril"
	chunk := &Chunk{
		ChunkID:   "ch-recharge-avril",
		ContentID: "ki-recharges",
		SectionID: &secID,
		ChunkHash: "h1",
		Text:      "Recharge du véhicule en avril : 42 kWh facturés.",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("InsertChunk: %v", err)
	}

	// section_id survives the round-trip through GetChunk.
	gotChunk, err := db.GetChunk(ctx, "ch-recharge-avril")
	if err != nil {
		t.Fatalf("GetChunk: %v", err)
	}
	if gotChunk == nil || gotChunk.SectionID == nil || *gotChunk.SectionID != "sec-avril" {
		t.Fatalf("chunk.section_id not persisted: %+v", gotChunk)
	}

	// BM25 lexical search finds the chunk by exact term (diacritics folded).
	hits, err := db.SearchChunksFTS(ctx, "recharge avril", 10)
	if err != nil {
		t.Fatalf("SearchChunksFTS: %v", err)
	}
	if len(hits) != 1 || hits[0].ChunkID != "ch-recharge-avril" {
		t.Fatalf("FTS expected 1 hit ch-recharge-avril, got %+v", hits)
	}
	if hits[0].ContentID != "ki-recharges" {
		t.Errorf("FTS hit content_id = %q, want ki-recharges", hits[0].ContentID)
	}

	// Empty/garbage query is a no-op, not an error.
	if hits, err := db.SearchChunksFTS(ctx, "  !? ", 10); err != nil || len(hits) != 0 {
		t.Errorf("empty query: hits=%v err=%v", hits, err)
	}

	// Small-to-big: a chunk resolves back to its full section block.
	bySection, err := db.GetSectionByChunkID(ctx, "ch-recharge-avril")
	if err != nil {
		t.Fatalf("GetSectionByChunkID: %v", err)
	}
	if bySection == nil || bySection.SectionID != "sec-avril" {
		t.Fatalf("GetSectionByChunkID returned %+v, want section sec-avril", bySection)
	}
	if bySection.FullText != sec.FullText {
		t.Errorf("section full_text mismatch via chunk join")
	}

	// The AFTER DELETE trigger must purge the FTS row too.
	if err := db.DeleteChunksByContentID(ctx, "ki-recharges"); err != nil {
		t.Fatalf("DeleteChunksByContentID: %v", err)
	}
	hits, err = db.SearchChunksFTS(ctx, "recharge", 10)
	if err != nil {
		t.Fatalf("SearchChunksFTS after delete: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected FTS empty after delete, got %+v", hits)
	}
}
