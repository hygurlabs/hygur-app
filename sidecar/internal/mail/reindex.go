package mail

import (
	"context"
	"fmt"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// ReindexStats summarizes a Tier 1 reindexing run.
type ReindexStats struct {
	Total      int
	Updated    int
	HighPrio   int
	WithIBAN   int
	WithAmount int
	WithComm   int
	Skipped    int // already had identical extracted_* fields
	Errors     int
}

// ReindexEntitiesTier1 walks all email knowledge items and re-runs Tier 1
// regex extraction, writing the results into metadata. The chunk text and
// embeddings are untouched. Idempotent: re-running on already-enriched
// items only writes when the regex output differs.
//
// Pass batchSize > 0 to limit memory usage on large mailboxes.
func ReindexEntitiesTier1(ctx context.Context, db *store.DB, logger zerolog.Logger, batchSize int) (*ReindexStats, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	stats := &ReindexStats{}
	// Walk every mail variant ("mail" / "email") so the reindex isn't a silent
	// no-op depending on the ingestion path.
	for _, sourceType := range store.MailSourceTypes {
		if err := reindexSourceType(ctx, db, logger, sourceType, batchSize, stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func reindexSourceType(ctx context.Context, db *store.DB, logger zerolog.Logger, sourceType string, batchSize int, stats *ReindexStats) error {
	offset := 0
	for {
		items, err := db.ListKnowledgeItemsBySourceType(ctx, sourceType, batchSize, offset)
		if err != nil {
			return fmt.Errorf("list %s offset=%d: %w", sourceType, offset, err)
		}
		if len(items) == 0 {
			break
		}
		stats.Total += len(items)

		for _, item := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			subject := item.Title
			body := item.NormalizedText
			fromAddr := ""
			if item.Metadata != nil {
				if s, ok := item.Metadata["mail_from"].(string); ok {
					fromAddr = s
				}
			}

			before := snapshotExtracted(item.Metadata)
			if item.Metadata == nil {
				item.Metadata = map[string]any{}
			}
			enrichMetadataWithTier1(item.Metadata, subject, body, fromAddr)
			after := snapshotExtracted(item.Metadata)

			if before == after {
				stats.Skipped++
				continue
			}

			if err := db.UpdateKnowledgeItem(ctx, item); err != nil {
				logger.Error().Err(err).Str("content_id", item.ContentID).Msg("update failed")
				stats.Errors++
				continue
			}
			stats.Updated++

			if hp, ok := item.Metadata["high_priority"].(bool); ok && hp {
				stats.HighPrio++
			}
			if _, ok := item.Metadata["extracted_iban"]; ok {
				stats.WithIBAN++
			}
			if _, ok := item.Metadata["extracted_amounts"]; ok {
				stats.WithAmount++
			}
			if _, ok := item.Metadata["extracted_structured_comm"]; ok {
				stats.WithComm++
			}
		}

		if len(items) < batchSize {
			break
		}
		offset += len(items)
	}
	return nil
}

// snapshotExtracted returns a stable string representation of the extracted_*
// fields so we can detect whether reindexing produced any change. Avoiding
// pulling in reflect.DeepEqual keeps the dependency graph minimal.
func snapshotExtracted(m map[string]any) string {
	if m == nil {
		return ""
	}
	keys := []string{
		"extracted_iban", "extracted_amounts", "extracted_structured_comm",
		"extracted_vat_numbers", "extracted_phones", "extracted_urls",
		"high_priority", "accounting_keywords",
	}
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%v;", k, m[k])
	}
	return out
}
