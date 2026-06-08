package handlers

import (
	"net/http"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// Contradictions scans mail items for deterministic contradictions — the same
// structured value (amount, due date, IBAN, VAT, structured communication)
// diverging across distinct items in one thread — and returns them with
// citations. GET /knowledge/contradictions.
//
// Runs on Tier-1 metadata already extracted at ingest, so it costs no LLM call.
// Every reported value is a verbatim citation; Hygur signals, it does not assert.
func (h *KnowledgeHandler) Contradictions(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	ctx := r.Context()

	var items []*store.KnowledgeItem
	const batch = 500
	for offset := 0; ; offset += batch {
		page, err := h.store.ListKnowledgeItemsBySourceType(ctx, "mail", batch, offset)
		if err != nil {
			h.logger.Error().Err(err).Msg("contradictions: list mail items failed")
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list items")
			return
		}
		items = append(items, page...)
		if len(page) < batch {
			break
		}
	}

	conflicts := contradict.Detect(items)
	if conflicts == nil {
		conflicts = []contradict.Conflict{}
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{
		"conflicts": conflicts,
		"scanned":   len(items),
	})
}
