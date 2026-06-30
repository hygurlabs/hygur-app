package handlers

import (
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// ConsolidationHandler exposes the memory-consolidation pass ("Quand Hygur rêve",
// DREAM_PLAN). SHADOW: scoring runs and item_signals is written, but nothing is
// evicted — so both the manual run and the read are safe to call any time.
type ConsolidationHandler struct {
	store        *store.DB
	consolidator *scheduler.Consolidator
	logger       zerolog.Logger
}

// NewConsolidationHandler builds the handler. consolidator/store may be nil (the
// corresponding endpoint then returns 503).
func NewConsolidationHandler(db *store.DB, c *scheduler.Consolidator, logger zerolog.Logger) *ConsolidationHandler {
	return &ConsolidationHandler{store: db, consolidator: c, logger: logger.With().Str("handler", "consolidation").Logger()}
}

// Run handles POST /consolidation/run — runs one shadow pass synchronously and
// returns its metrics. It evicts nothing; it just scores and writes item_signals.
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

// Signals handles GET /consolidation/signals — the shadow scoring distribution
// (salience/strength/surprise histograms, tier counts, vector footprint, top-N) for
// calibration. Read-only.
func (h *ConsolidationHandler) Signals(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	summary, err := h.store.ItemSignalsSummary(r.Context())
	if err != nil {
		h.logger.Warn().Err(err).Msg("signals summary failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "signals summary failed")
		return
	}
	if h.consolidator != nil {
		summary.Vector.BudgetBytes = h.consolidator.BudgetBytes()
	}
	writeKnowledgeJSON(w, http.StatusOK, summary)
}

// InteractionStats handles GET /consolidation/interaction-stats — per-kind counts of
// the append-only interaction_log with item-level ref coverage and time span. Used to
// gauge whether there is enough behavioral data (document_opened, memory_accepted, …)
// to build a held-out ground-truth evaluation of salience. Read-only.
func (h *ConsolidationHandler) InteractionStats(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	stats, err := h.store.InteractionStats(r.Context())
	if err != nil {
		h.logger.Warn().Err(err).Msg("interaction stats failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "interaction stats failed")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"by_kind": stats})
}
