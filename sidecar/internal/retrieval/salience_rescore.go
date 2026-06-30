package retrieval

import (
	"context"
	"log"
	"sort"
)

// Salience rescore — the recycle of the composite importance signal into SAIT
// ranking. Where attention rescore boosts by access alone, this boosts by the full
// item_signals.salience (access + structural links + flag + canonical recency +
// surprise + graph connectivity). Bounded, boost-only (never demotes), fail-open,
// no-op unless enabled or until the consolidation pass has scored items. Runs after
// the other nudges so it tweaks within the band, not overrides — and we MEASURE
// whether "importance to you" actually improves the constructed context.
const salienceMaxBoost = 0.20 // a maximally-salient item gets at most +20%

func (us *UnifiedSearcher) applySalienceRescore(ctx context.Context, results []UnifiedResult) {
	if !us.useSalienceRerank || len(results) == 0 || us.store == nil {
		return
	}
	ids := make([]string, 0, len(results))
	for i := range results {
		ids = append(ids, results[i].ContentID)
	}
	sal, err := us.store.ItemSaliences(ctx, ids)
	if err != nil {
		log.Printf("[salience] WARN: item_signals read failed — order unchanged (fail-open): %v", err)
		return
	}
	if len(sal) == 0 {
		return // nothing scored yet — silent no-op
	}
	var boosted int
	for i := range results {
		s, ok := sal[results[i].ContentID]
		if !ok || s <= 0 {
			continue
		}
		if s > 1 {
			s = 1
		}
		results[i].Score *= 1.0 + salienceMaxBoost*s
		boosted++
	}
	if boosted > 0 {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		log.Printf("[salience] rescore: boosted=%d of=%d", boosted, len(results))
	}
}
