package handlers

import (
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/rs/zerolog"
)

// ConsolidationHandler exposes the manual trigger for the memory-consolidation pass
// ("Quand Hygur rêve", DREAM_PLAN Phase 1). SHADOW: scoring runs and item_signals is
// written, but nothing is evicted — so a manual run is safe to call any time.
type ConsolidationHandler struct {
	consolidator *scheduler.Consolidator
	logger       zerolog.Logger
}

// NewConsolidationHandler builds the handler. consolidator may be nil (run disabled).
func NewConsolidationHandler(c *scheduler.Consolidator, logger zerolog.Logger) *ConsolidationHandler {
	return &ConsolidationHandler{consolidator: c, logger: logger.With().Str("handler", "consolidation").Logger()}
}

// Run handles POST /consolidation/run — runs one shadow pass synchronously and
// returns its metrics (vector footprint, scored, hot/would-evict, reclaimable). It
// evicts nothing; it just scores and writes item_signals.
func (h *ConsolidationHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.consolidator == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "consolidator not configured")
		return
	}
	res, err := h.consolidator.RunOnce(r.Context(), time.Now())
	if err != nil {
		h.logger.Warn().Err(err).Msg("manual consolidation run failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "consolidation pass failed")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, res)
}
