package scheduler

import (
	"context"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// ExtractClaims runs W6 claim extraction on an item's text (on-demand preview /
// eval surface for stage 1). Returns nil when the LLM isn't configured. Stamps
// source_id + asserted_at from the item so the output is already pipeline-shaped.
func (d *DailyBrief) ExtractClaims(ctx context.Context, item *store.KnowledgeItem) ([]contradict.Claim, error) {
	if d == nil || d.llm == nil || item == nil {
		return nil, nil
	}
	// Read the display text (raw when available; falls back to normalized_text for
	// pre-raw_text items), matching the ingest-time claim extraction.
	claims, err := contradict.ExtractClaims(ctx, d.llm, item.DisplayText())
	if err != nil {
		return nil, err
	}
	at := item.CreatedAt.UTC().Format(time.RFC3339)
	for i := range claims {
		claims[i].SourceID = item.ContentID
		claims[i].AssertedAt = at
	}
	return claims, nil
}
