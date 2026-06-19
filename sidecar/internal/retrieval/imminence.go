package retrieval

import (
	"context"
	"log"
	"sort"
	"time"
)

// P-2 (Conséquence → Précision), imminence half. An item tied to an obligation due
// very soon — a recurring subject whose next occurrence is imminent — earns a small,
// BOUNDED boost, so what the user is about to need surfaces a little higher. The
// counterpart to the attention half: where attention rewards what was used, imminence
// rewards what is about to matter. Boost-only (never demotes), capped well below the
// authority weights, and applied AFTER authority so it only nudges within the band.
// Off → no-op; empty imminent set → no-op.
const (
	imminenceBoost = 0.15          // an imminent item gets +15%
	imminentTTL    = 2 * time.Hour // how long a computed imminent set is reused
)

// applyImminenceRescore boosts results whose content_id is in the cached imminent set
// and re-sorts. Fail-open: a nil/empty set leaves the order intact. Runs after the
// authority (and attention) re-scores, so authority dominates.
func (us *UnifiedSearcher) applyImminenceRescore(ctx context.Context, results []UnifiedResult) {
	if !us.useImminenceRerank || len(results) == 0 {
		return
	}
	imminent := us.imminentSet(ctx)
	if len(imminent) == 0 {
		return
	}
	var boosted int
	for i := range results {
		if _, ok := imminent[results[i].ContentID]; ok {
			results[i].Score *= 1.0 + imminenceBoost
			boosted++
		}
	}
	if boosted > 0 {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		log.Printf("[imminence] rescore: boosted=%d of=%d", boosted, len(results))
	}
}

// imminentSet returns the cached imminent content-id set, refreshing it via the
// provider when the TTL has elapsed. Serialized under a mutex; fail-open — a nil
// provider or a nil result caches an empty set, so the (potentially heavy) provider
// scan is never re-run on every query.
func (us *UnifiedSearcher) imminentSet(ctx context.Context) map[string]struct{} {
	us.imminentMu.Lock()
	defer us.imminentMu.Unlock()
	if us.imminentFn == nil {
		return nil
	}
	if us.imminentCache != nil && time.Now().Before(us.imminentExpires) {
		return us.imminentCache
	}
	set := us.imminentFn(ctx)
	if set == nil {
		set = map[string]struct{}{}
	}
	us.imminentCache = set
	us.imminentExpires = time.Now().Add(imminentTTL)
	return set
}
