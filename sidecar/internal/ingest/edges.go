package ingest

import (
	"context"
	"log"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// Hebbian co-occurrence wiring (DREAM Phase D, docs/DREAM_PLAN_ADDENDUM.md §3). At
// ingestion, the entities mentioned in an item strengthen their pairwise edges; a
// one-shot backfill seeds the graph from the existing corpus. Best-effort — never
// blocks ingestion.

// edgeDate returns the item's content date (canonical), falling back to ingestion
// time, so an edge's recency reflects when the entities actually co-occurred.
func edgeDate(item *store.KnowledgeItem) string {
	if d := store.GetCanonicalDate(item); !d.IsZero() {
		return d.UTC().Format(time.RFC3339)
	}
	return item.CreatedAt.UTC().Format(time.RFC3339)
}

func normsFromMentions(mentions []store.EntityMention) []string {
	norms := make([]string, 0, len(mentions))
	for _, m := range mentions {
		if m.EntityNorm != "" {
			norms = append(norms, m.EntityNorm)
		}
	}
	return norms
}

// stampCoOccurrence strengthens the Hebbian edges from an item's fresh mentions.
// Best-effort: any error is logged and swallowed.
func (i *Ingestor) stampCoOccurrence(ctx context.Context, item *store.KnowledgeItem, mentions []store.EntityMention) {
	if i.store == nil || item == nil || len(mentions) < 2 {
		return
	}
	if err := i.store.UpsertCoOccurrences(ctx, normsFromMentions(mentions), edgeDate(item)); err != nil {
		log.Printf("[ingest] hebbian co-occurrence failed for %s: %v", item.ContentID, err)
	}
}

// BackfillEntityEdges rebuilds the Hebbian graph from each item's cached claims AND
// its Tier-2 NER entities — deterministic, no LLM. Idempotent: it clears the table
// first, so the resulting graph is one co-occurrence count per item regardless of how
// often it is run. Mirrors BackfillEntityIndex (same claim+NER union, so a person and
// the orgs/topics they co-occur with become neighbors); seeds the graph on an existing
// corpus. Returns items scanned.
func (i *Ingestor) BackfillEntityEdges(ctx context.Context) (int, error) {
	if i.store == nil {
		return 0, nil
	}
	if err := i.store.ClearEntityEdges(ctx); err != nil {
		return 0, err
	}
	var processed int
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote, store.SourceTypeDecision) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := i.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return processed, err
			}
			for _, it := range page {
				if ctx.Err() != nil {
					return processed, ctx.Err()
				}
				mentions := entityMentionsFromClaims(contradict.ClaimsFromMetadata(it.Metadata))
				mentions = append(mentions, nerEntityMentions(it)...)
				i.stampCoOccurrence(ctx, it, mentions)
				processed++
			}
			if len(page) < batch {
				break
			}
		}
	}
	return processed, nil
}
