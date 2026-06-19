package ingest

import (
	"context"
	"log"
)

// entityVectorBackfillCap bounds a single backfill pass; re-run to continue past
// it (a personal corpus has far fewer distinct entities than this).
const entityVectorBackfillCap = 5000

// BackfillEntityVectors embeds the distinct entity_norms that don't yet have a
// vector for the current embedding model, populating entity_vectors for the
// brick-2 synonymy expansion. Idempotent + incremental (only missing norms), so
// it's safe to re-run. Requires the embedding client; a no-op without it.
// Returns the number of entities embedded.
func (i *Ingestor) BackfillEntityVectors(ctx context.Context) (int, error) {
	if i.store == nil || i.llmClient == nil {
		return 0, nil
	}
	model := i.llmClient.GetEmbeddingModel()
	norms, err := i.store.EntityNormsNeedingVector(ctx, model, entityVectorBackfillCap)
	if err != nil {
		return 0, err
	}
	if len(norms) == entityVectorBackfillCap {
		log.Printf("[ingest] entity-vector backfill capped at %d this pass — re-run to continue", entityVectorBackfillCap)
	}

	const sub = 10 // embedding endpoint batch cap
	var embedded int
	for off := 0; off < len(norms); off += sub {
		if ctx.Err() != nil {
			return embedded, ctx.Err()
		}
		end := off + sub
		if end > len(norms) {
			end = len(norms)
		}
		batch := norms[off:end]
		vecs, eerr := i.llmClient.GenerateEmbeddings(ctx, batch)
		if eerr != nil {
			return embedded, eerr
		}
		if len(vecs) != len(batch) {
			log.Printf("[ingest] entity-vector embed count mismatch (%d vecs for %d norms) — skipping batch", len(vecs), len(batch))
			continue
		}
		for k := range batch {
			if rerr := i.store.UpsertEntityVector(ctx, batch[k], vecs[k], model); rerr != nil {
				log.Printf("[ingest] entity-vector upsert failed for %q: %v", batch[k], rerr)
				continue
			}
			embedded++
		}
	}
	return embedded, nil
}
