package extract

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// BackfillStats summarizes a backfill run across the knowledge_items corpus.
type BackfillStats struct {
	Total        int // candidates considered
	UpdatedTier1 int
	UpdatedTier2 int
	SkippedV2    int // already at the current Tier 2 schema version
	Errors       int
}

// BackfillOptions tunes the backfill loop.
type BackfillOptions struct {
	// BatchSize is the number of items pulled from the store per page.
	BatchSize int
	// DryRun, when true, runs Tier 1 + Tier 2 but does not persist.
	DryRun bool
	// SkipTier2 disables the LLM pass entirely (Tier 1 only). Useful for an
	// initial fast pass before running the slower Tier 2 backfill.
	SkipTier2 bool
	// ProgressEvery emits a stderr-style progress callback every N items
	// processed. Set to 0 to disable.
	ProgressEvery int
	// ProgressFn receives (processed, total stats so far). Optional.
	ProgressFn func(processed int, stats BackfillStats)
	// PreserveTimestamp writes only the metadata, leaving updated_at untouched. A
	// re-extraction changes derived metadata, not content, so it must not mark every
	// item "recently modified" (which would flood updated_at-based recency queries).
	PreserveTimestamp bool
}

func (o *BackfillOptions) defaults() {
	if o.BatchSize <= 0 {
		o.BatchSize = 100
	}
}

// Backfill walks all knowledge items and re-extracts Tier 1 (regex) and
// Tier 2 (LLM NER) into their metadata. Idempotent: items already stamped
// with the current Tier2Version are skipped unless DryRun is set.
//
// llmClient may be nil to run Tier 1 only (equivalent to SkipTier2=true).
func Backfill(ctx context.Context, db *store.DB, llmClient *llm.Client, opts BackfillOptions) (*BackfillStats, error) {
	if db == nil {
		return nil, fmt.Errorf("backfill: nil store")
	}
	opts.defaults()
	if llmClient == nil {
		opts.SkipTier2 = true
	}

	stats := &BackfillStats{}
	offset := 0

	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		items, err := db.ListKnowledgeItems(ctx, opts.BatchSize, offset)
		if err != nil {
			return stats, fmt.Errorf("list items at offset=%d: %w", offset, err)
		}
		if len(items) == 0 {
			break
		}

		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			stats.Total++

			if item.Metadata == nil {
				item.Metadata = map[string]any{}
			}

			// Tier 1: cheap, always re-run. The result is deterministic so
			// re-running on already-enriched items is harmless.
			before := snapshotTier1Keys(item.Metadata)
			EnrichMetadataWithTier1(item.Metadata, item.NormalizedText)
			tier1Changed := snapshotTier1Keys(item.Metadata) != before

			// Tier 2: skip if already at current version.
			tier2Changed := false
			if !opts.SkipTier2 && !hasCurrentTier2(item.Metadata) {
				tier2, err := ExtractTier2(ctx, llmClient, item.NormalizedText)
				if err != nil {
					stats.Errors++
					fmt.Fprintf(os.Stderr, "tier2 error on content_id=%s: %v\n", item.ContentID, err)
					if opts.ProgressFn != nil && opts.ProgressEvery > 0 && stats.Total%opts.ProgressEvery == 0 {
						opts.ProgressFn(stats.Total, *stats)
					}
					continue
				}
				MergeTier2IntoMetadata(item.Metadata, tier2)
				tier2Changed = true
			} else if !opts.SkipTier2 {
				stats.SkippedV2++
			}

			if !opts.DryRun && (tier1Changed || tier2Changed) {
				var uerr error
				if opts.PreserveTimestamp {
					uerr = db.UpdateKnowledgeItemMetadata(ctx, item.ContentID, item.Metadata)
				} else {
					uerr = db.UpdateKnowledgeItem(ctx, item)
				}
				if uerr != nil {
					stats.Errors++
					fmt.Fprintf(os.Stderr, "store update error on content_id=%s: %v\n", item.ContentID, uerr)
					continue
				}
			}
			if tier1Changed {
				stats.UpdatedTier1++
			}
			if tier2Changed {
				stats.UpdatedTier2++
			}

			if opts.ProgressFn != nil && opts.ProgressEvery > 0 && stats.Total%opts.ProgressEvery == 0 {
				opts.ProgressFn(stats.Total, *stats)
			}
		}

		if len(items) < opts.BatchSize {
			break
		}
		offset += len(items)
	}

	return stats, nil
}

// hasCurrentTier2 returns true if the item has been processed by the current
// Tier 2 schema version (matching Tier2Version). Items processed by an older
// schema version are re-extracted.
func hasCurrentTier2(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	v, ok := metadata["extracted_v2_version"].(string)
	if !ok || v != Tier2Version {
		return false
	}
	// Sanity check: the timestamp should also be present and parseable.
	ts, ok := metadata["extracted_v2_at"].(string)
	if !ok {
		return false
	}
	_, err := time.Parse(time.RFC3339, ts)
	return err == nil
}

// snapshotTier1Keys returns a stable string of Tier 1 extracted_* keys so we
// can detect whether re-running Tier 1 on a doc changed anything. Used only
// to bump the right counter; not needed for correctness.
func snapshotTier1Keys(m map[string]any) string {
	if m == nil {
		return ""
	}
	keys := []string{
		"extracted_iban", "extracted_amounts", "extracted_structured_comm",
		"extracted_vat_numbers", "extracted_phones", "extracted_urls", "extracted_due_dates",
	}
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%v;", k, m[k])
	}
	return out
}
