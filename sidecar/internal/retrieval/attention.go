package retrieval

import (
	"context"
	"log"
	"sort"
	"time"
)

// P-2 (Conséquence → Précision) — attention modulation. The attention bus
// (item_access) records which items were actually USED to answer the user; this
// is its first consumer. An often- and recently-cited item earns a small, BOUNDED
// boost, so what the user keeps coming back to surfaces a little higher. Boost-only
// (never demotes — an un-accessed item is never penalized) and capped well below the
// authority weights, so attention nudges relevance, never overrides it. Off → no-op,
// and a no-op anyway until the bus has data (it starts empty).

const (
	attentionMaxBoost    = 0.15 // a maximally-hot item gets at most +15%
	attentionFreqHalf    = 3.0  // hit_count at which the frequency term reaches 0.5
	attentionRecencyDays = 30.0 // recency decays linearly to 0 over this window
)

// attentionMultiplier maps an item's access signal to a score multiplier in
// [1, 1+attentionMaxBoost]. Frequency saturates (diminishing returns); recency
// modulates it (older accesses count for less) but never zeroes a frequent item.
// Never-accessed (hitCount<=0) → 1.0 (identity). Pure + deterministic given now.
func attentionMultiplier(hitCount int, lastAccessed, now time.Time) float64 {
	if hitCount <= 0 {
		return 1.0
	}
	freq := float64(hitCount) / (float64(hitCount) + attentionFreqHalf) // (0,1)
	rec := 0.0
	if !lastAccessed.IsZero() {
		ageDays := now.Sub(lastAccessed).Hours() / 24.0
		if ageDays < attentionRecencyDays {
			rec = 1.0 - ageDays/attentionRecencyDays
			if rec < 0 {
				rec = 0
			}
		}
	}
	attention := freq * (0.5 + 0.5*rec) // recency halves-to-fully the frequency weight
	return 1.0 + attentionMaxBoost*attention
}

// applyAttentionRescore boosts results by their attention signal and re-sorts. Reads
// item_access in one batch query; fail-open (a store error leaves the order intact).
// No-op unless enabled. Runs AFTER the authority re-score, so authority dominates and
// attention only breaks near-ties / nudges within a band.
func (us *UnifiedSearcher) applyAttentionRescore(ctx context.Context, results []UnifiedResult) {
	if !us.useAttentionRerank || len(results) == 0 || us.store == nil {
		return
	}
	ids := make([]string, 0, len(results))
	for i := range results {
		ids = append(ids, results[i].ContentID)
	}
	acc, err := us.store.ItemAccessByIDs(ctx, ids)
	if err != nil {
		log.Printf("[attention] WARN: item_access read failed — order unchanged (fail-open): %v", err)
		return
	}
	if len(acc) == 0 {
		return // nothing accessed yet — silent no-op
	}
	now := time.Now()
	var boosted int
	for i := range results {
		a, ok := acc[results[i].ContentID]
		if !ok {
			continue
		}
		if m := attentionMultiplier(a.HitCount, a.LastAccessedAt, now); m > 1.0 {
			results[i].Score *= m
			boosted++
		}
	}
	if boosted > 0 {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		log.Printf("[attention] rescore: boosted=%d of=%d", boosted, len(results))
	}
}
