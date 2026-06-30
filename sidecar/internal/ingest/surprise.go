package ingest

import (
	"context"
	"log"
	"strings"

	"github.com/hygur/sidecar/internal/store"
)

// Surprise / novelty signal (DREAM Phase C, docs/DREAM_PLAN_ADDENDUM.md §2). Computed
// at ingestion from how much of an item is NEW relative to what the entity index
// already knew — reusing the claims the ingestor already extracted (no extra LLM
// call). Stamped into item_surprise; the nightly consolidation pass reads it to nudge
// salience. The orchestration (stampSurprise) is best-effort: any failure is logged
// and swallowed — it NEVER affects the entity index or the rest of ingestion.

const (
	surpriseNoveltyWeight = 0.60
	surpriseDriftWeight   = 0.40
)

// NoveltyDrift derives the two surprise inputs from an item's entity norms and
// "norm\x00attribute" pairs versus what the index knew before (EntityPriorMentions):
//   - novelRatio: fraction of the item's entities never seen before.
//   - drift: a KNOWN entity gained a new (entity, attribute) — a relational shift.
//
// A brand-new entity counts as novelty, never drift. Pure and deterministic.
func NoveltyDrift(newNorms, newPairs []string, knownNorms, knownPairs map[string]struct{}) (novelRatio float64, drift bool) {
	if len(newNorms) == 0 {
		return 0, false
	}
	novel := 0
	for _, n := range newNorms {
		if _, ok := knownNorms[n]; !ok {
			novel++
		}
	}
	novelRatio = float64(novel) / float64(len(newNorms))
	for _, p := range newPairs {
		norm := p
		if i := strings.IndexByte(p, 0); i >= 0 {
			norm = p[:i]
		}
		if _, known := knownNorms[norm]; !known {
			continue // a brand-new entity is novelty, not drift
		}
		if _, seen := knownPairs[p]; !seen {
			drift = true
			break
		}
	}
	return novelRatio, drift
}

// ComputeSurprise blends novelty and drift into a [0,1] score (addendum §2).
func ComputeSurprise(novelRatio float64, drift bool) float64 {
	s := surpriseNoveltyWeight * surpriseClamp(novelRatio)
	if drift {
		s += surpriseDriftWeight
	}
	return surpriseClamp(s)
}

func surpriseClamp(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	default:
		return x
	}
}

// stampSurprise computes and stores the item's surprise from its fresh mentions,
// BEFORE they are written to the index (else the item's own entities read as known).
// Best-effort — never returns an error and never blocks ingestion.
func (i *Ingestor) stampSurprise(ctx context.Context, contentID string, mentions []store.EntityMention) {
	if i.store == nil || len(mentions) == 0 {
		return
	}
	normsSet := map[string]struct{}{}
	pairsSet := map[string]struct{}{}
	for _, m := range mentions {
		if m.EntityNorm == "" {
			continue
		}
		normsSet[m.EntityNorm] = struct{}{}
		pairsSet[m.EntityNorm+"\x00"+m.Attribute] = struct{}{}
	}
	if len(normsSet) == 0 {
		return
	}
	newNorms := make([]string, 0, len(normsSet))
	for n := range normsSet {
		newNorms = append(newNorms, n)
	}
	newPairs := make([]string, 0, len(pairsSet))
	for p := range pairsSet {
		newPairs = append(newPairs, p)
	}
	knownNorms, knownPairs, err := i.store.EntityPriorMentions(ctx, contentID, newNorms)
	if err != nil {
		log.Printf("[ingest] surprise: prior-mentions read failed for %s (skipped): %v", contentID, err)
		return
	}
	novelRatio, drift := NoveltyDrift(newNorms, newPairs, knownNorms, knownPairs)
	if err := i.store.UpsertItemSurprise(ctx, contentID, ComputeSurprise(novelRatio, drift)); err != nil {
		log.Printf("[ingest] surprise: stamp failed for %s: %v", contentID, err)
	}
}
