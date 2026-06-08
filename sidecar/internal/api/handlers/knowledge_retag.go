package handlers

import (
	"context"
	"net/http"
	"sync/atomic"
)

// retagInFlight guards against overlapping retag runs (the job is long: one LLM
// call per mail that still needs topic extraction).
var retagInFlight atomic.Bool

// Retag rebuilds mail auto-tags over the whole corpus (purge stale auto-tags →
// mailbox-folder + Tier-2 topic tags). POST /knowledge/retag. The job runs in the
// background and is reported via logs; the request returns immediately. Watch the
// result by polling GET /tags. Idempotent — cached topics are reused on re-runs.
func (h *KnowledgeHandler) Retag(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if !retagInFlight.CompareAndSwap(false, true) {
		writeKnowledgeJSON(w, http.StatusOK, map[string]string{"status": "already_running"})
		return
	}
	go func() {
		defer retagInFlight.Store(false)
		// Detached from the request: the backfill outlives the HTTP call.
		n, err := h.ingestor.RetagItems(context.Background())
		if err != nil {
			h.logger.Error().Err(err).Int("processed", n).Msg("mail retag failed")
			return
		}
		h.logger.Info().Int("processed", n).Msg("mail retag complete")
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
