package ingest

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// SectionStore is the persistence surface the central indexer needs.
// *store.DB satisfies it.
type SectionStore interface {
	InsertSection(ctx context.Context, s *store.Section) error
	InsertChunk(ctx context.Context, c *store.Chunk) error
	DeleteSectionsByContentID(ctx context.Context, contentID string) error
	DeleteChunksByContentID(ctx context.Context, contentID string) error
}

// SectionEmbedder embeds chunks and stores their vectors.
// *llm.EmbeddingService satisfies it.
type SectionEmbedder interface {
	BatchEmbedAndStore(ctx context.Context, chunks []store.Chunk) error
}

// IndexSections is THE chunking + indexing path for store-backed callers
// (mail, notes, …). It parses `text` into hierarchical sections + embed-sized
// chunks (BuildSections) and persists them via PersistSections, replacing any
// prior rows for contentID so a re-index is idempotent.
//
// The caller must already have inserted/updated the knowledge_items row for
// contentID (sections/chunks FK to it). chunkBudget ≤ 0 falls back to
// DefaultChunkTokenBudget. Returns the number of sections and chunks written.
func IndexSections(ctx context.Context, st SectionStore, emb SectionEmbedder, contentID, text string, chunkBudget int, now time.Time) (sectionCount, chunkCount int, err error) {
	built := BuildSections(contentID, text, chunkBudget)
	if len(built) == 0 {
		// Emptied document → drop any stale rows so nothing is left orphaned.
		return 0, 0, clearPrior(ctx, st, contentID)
	}
	return PersistSections(ctx, st, emb, built, now)
}

// PersistSections writes prebuilt sections + their chunks (replacing any prior
// rows for the same content), then embeds the chunks. The content ID is taken
// from the sections themselves. Use this when the chunk plan was already
// produced by BuildSections (e.g. to report a count before deciding to persist).
//
// On embedding failure it returns an error WITHOUT rolling back the rows it
// wrote — the caller owns the knowledge_items lifecycle and decides whether to
// delete the item (which cascades to sections/chunks).
func PersistSections(ctx context.Context, st SectionStore, emb SectionEmbedder, built []SectionChunk, now time.Time) (sectionCount, chunkCount int, err error) {
	if len(built) == 0 {
		return 0, 0, nil
	}
	contentID := built[0].Section.ContentID

	// Idempotent re-index: drop prior sections/chunks for this document. The
	// chunks_fts triggers purge the FTS rows when chunks are deleted.
	if err := clearPrior(ctx, st, contentID); err != nil {
		return 0, 0, err
	}

	allChunks := make([]store.Chunk, 0, len(built))
	for i := range built {
		sec := built[i].Section
		sec.CreatedAt = now
		if err := st.InsertSection(ctx, &sec); err != nil {
			return 0, 0, fmt.Errorf("insert section %q: %w", sec.SectionID, err)
		}
		for j := range built[i].Chunks {
			ch := built[i].Chunks[j]
			ch.CreatedAt = now
			if err := st.InsertChunk(ctx, &ch); err != nil {
				return 0, 0, fmt.Errorf("insert chunk %q: %w", ch.ChunkID, err)
			}
			allChunks = append(allChunks, ch)
		}
	}

	if embedderUsable(emb) && len(allChunks) > 0 {
		if err := emb.BatchEmbedAndStore(ctx, allChunks); err != nil {
			// Sections/chunks are already persisted (and FTS-indexed) at this
			// point. Return the real counts alongside the error so soft callers
			// can tell an embedding failure (rows exist, searchable via FTS)
			// from a persistence failure (counts are 0).
			return len(built), len(allChunks), fmt.Errorf("embed chunks: %w", err)
		}
	}
	return len(built), len(allChunks), nil
}

// TotalChunks counts the chunks across a BuildSections result.
func TotalChunks(built []SectionChunk) int {
	n := 0
	for i := range built {
		n += len(built[i].Chunks)
	}
	return n
}

// embedderUsable reports whether emb is a real, callable embedder. It guards
// the typed-nil-in-interface footgun: a caller passing a nil concrete pointer
// (e.g. a nil *llm.EmbeddingService, common in tests/dry runs) yields a
// non-nil interface wrapping a nil pointer, which would panic on call.
func embedderUsable(emb SectionEmbedder) bool {
	if emb == nil {
		return false
	}
	if v := reflect.ValueOf(emb); v.Kind() == reflect.Ptr && v.IsNil() {
		return false
	}
	return true
}

// clearPrior removes a document's existing chunks then sections (chunks first
// so the FTS delete triggers fire before the rows vanish).
func clearPrior(ctx context.Context, st SectionStore, contentID string) error {
	if err := st.DeleteChunksByContentID(ctx, contentID); err != nil {
		return fmt.Errorf("clear chunks: %w", err)
	}
	if err := st.DeleteSectionsByContentID(ctx, contentID); err != nil {
		return fmt.Errorf("clear sections: %w", err)
	}
	return nil
}
