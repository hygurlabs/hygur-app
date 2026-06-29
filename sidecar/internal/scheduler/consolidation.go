package scheduler

import (
	"context"
	"sort"
	"time"

	"github.com/rs/zerolog"

	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
)

// "Quand Hygur rêve" — the nightly memory-consolidation pass (DREAM_PLAN Phase 1,
// docs/DREAM_PLAN_ADDENDUM.md). It scores every item deterministically (salience +
// forgetting strength), draws the hot/cold line under the per-tenant vector budget,
// writes the decision to item_signals, and LOGS what it would evict.
//
// SHADOW: it drops NOTHING. Eviction + re-hydration is a separate, later switch
// (Phase E). Shadow scores regardless of budget so we can measure the real
// salience/strength/footprint distribution before committing to any threshold.

const (
	// v1 defaults — docs/DREAM_PLAN_ADDENDUM.md §7. Tunable after shadow measurement.
	dreamBudgetBytes int64 = 600 << 20 // 600 MiB of vectors per tenant (provisional)
	dreamGraceImport       = 30.0      // days since ingestion before an item is eviction-eligible
	dreamMaxItems          = 20000     // items scored per pass (bounded; truncation is logged)
	dreamBatch             = 500       // items loaded per page
)

// Consolidator runs one consolidation pass. Deterministic: no LLM, no network.
type Consolidator struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewConsolidator builds the pass; nil when the store is missing.
func NewConsolidator(db *store.DB, logger zerolog.Logger) *Consolidator {
	if db == nil {
		return nil
	}
	return &Consolidator{store: db, logger: logger.With().Str("component", "consolidation").Logger()}
}

type scoredItem struct {
	contentID string
	salience  float64
	strength  float64
	exempt    bool
	vbytes    int64
	ageIngest float64
}

// RunOnce executes one shadow consolidation pass at time `now`. It writes
// item_signals and logs metrics; it never evicts. Idempotent (re-running re-scores).
func (c *Consolidator) RunOnce(ctx context.Context, now time.Time) error {
	if c == nil {
		return nil
	}
	t0 := time.Now()

	vecRows, vecBytes, dim, err := c.store.VectorFootprint(ctx)
	if err != nil {
		return err
	}

	all, err := c.scoreAll(ctx, now)
	if err != nil {
		return err
	}

	// Tier under the vector budget: highest salience stays HOT until the budget is
	// spent; below the line, an item fades to COLD only if its strength has also
	// decayed under the floor AND it is past the import grace. Hard-exempt items are
	// always HOT regardless of the line (addendum §1.6).
	sort.SliceStable(all, func(i, j int) bool { return all[i].salience > all[j].salience })
	var cum, exemptBytes, reclaimable int64
	var hot, cold int
	sigs := make([]store.ItemSignal, len(all))
	for i := range all {
		s := &all[i]
		cum += s.vbytes
		if s.exempt {
			exemptBytes += s.vbytes
		}
		tier := "hot"
		if !s.exempt && cum > dreamBudgetBytes && s.strength < retrieval.EvictionFloor && s.ageIngest > dreamGraceImport {
			tier = "cold"
			cold++
			reclaimable += s.vbytes
		} else {
			hot++
		}
		sigs[i] = store.ItemSignal{
			ContentID: s.contentID,
			Salience:  s.salience,
			Strength:  s.strength,
			Surprise:  0, // Phase C
			Exempt:    s.exempt,
			Tier:      tier,
			ScoredAt:  now,
		}
	}

	if err := c.store.UpsertItemSignals(ctx, sigs); err != nil {
		return err
	}

	ev := c.logger.Info().
		Int64("vec_rows", vecRows).Int64("vec_bytes", vecBytes).Int("dim", dim).
		Int64("budget_bytes", dreamBudgetBytes).
		Int("scored", len(all)).Int("hot", hot).Int("would_evict", cold).
		Int64("would_reclaim_bytes", reclaimable).Int64("exempt_bytes", exemptBytes).
		Dur("dur", time.Since(t0))
	if len(all) >= dreamMaxItems {
		ev = ev.Bool("truncated", true) // corpus exceeds the per-pass cap (no silent truncation)
	}
	ev.Msg("consolidation shadow pass (no eviction)")

	if exemptBytes > dreamBudgetBytes {
		c.logger.Warn().Int64("exempt_bytes", exemptBytes).Int64("budget_bytes", dreamBudgetBytes).
			Msg("hard-exempt set alone exceeds the vector budget — budget too tight (addendum §6)")
	}
	return nil
}

// scoreAll loads items in bounded pages and scores each. The vector footprint and
// link signals are batch-read per page to keep the pass O(corpus) with few queries.
func (c *Consolidator) scoreAll(ctx context.Context, now time.Time) ([]scoredItem, error) {
	all := make([]scoredItem, 0, 4096)
	for offset := 0; offset < dreamMaxItems; offset += dreamBatch {
		items, err := c.store.ListKnowledgeItems(ctx, dreamBatch, offset)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ContentID
		}
		access, err := c.store.ItemAccessByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		vbytes, err := c.store.ItemVectorBytes(ctx, ids)
		if err != nil {
			return nil, err
		}
		links, err := c.store.ItemLinkSignals(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			ls := links[it.ContentID]
			ac := access[it.ContentID]
			sig := retrieval.SalienceSignals{
				HitCount:      ac.HitCount,
				LastAccessed:  ac.LastAccessedAt,
				IngestedAt:    it.CreatedAt,
				LinkCount:     ls.LinkCount,
				Flag:          ls.Exempt(),
				CanonicalDate: store.GetCanonicalDate(it),
				Surprise:      0, // Phase C
				Now:           now,
			}
			sal := retrieval.ComputeSalience(sig)
			all = append(all, scoredItem{
				contentID: it.ContentID,
				salience:  sal,
				strength:  retrieval.ComputeStrength(sal, sig.AccessAgeDays()),
				exempt:    ls.Exempt(),
				vbytes:    vbytes[it.ContentID],
				ageIngest: ageDaysSince(it.CreatedAt, now),
			})
		}
		if len(items) < dreamBatch {
			break
		}
	}
	return all, nil
}

func ageDaysSince(t, now time.Time) float64 {
	if t.IsZero() {
		return 1e9 // unknown ingestion → treat as ancient (grace passes)
	}
	if d := now.Sub(t).Hours() / 24.0; d > 0 {
		return d
	}
	return 0
}

// ConsolidationScheduler runs the consolidation pass once a day at the night hour,
// mirroring the chronicle/decision schedulers.
type ConsolidationScheduler struct {
	c      *Consolidator
	hour   int
	logger zerolog.Logger
}

// NewConsolidationScheduler builds the loop; nil when the consolidator is missing.
func NewConsolidationScheduler(c *Consolidator, nightHour int, logger zerolog.Logger) *ConsolidationScheduler {
	if c == nil {
		return nil
	}
	if nightHour < 0 || nightHour > 23 {
		nightHour = 0
	}
	return &ConsolidationScheduler{c: c, hour: nightHour, logger: logger.With().Str("component", "consolidation_scheduler").Logger()}
}

// Start launches the loop. Hourly tick; runs once at the night hour. The pass
// overwrites item_signals, so a same-hour double-fire is a harmless re-score.
func (s *ConsolidationScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				if now.Hour() != s.hour {
					continue
				}
				if err := s.c.RunOnce(ctx, now); err != nil {
					s.logger.Debug().Err(err).Msg("nightly consolidation failed")
				}
			}
		}
	}()
}
