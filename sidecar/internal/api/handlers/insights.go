package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/interactions"
	"github.com/rs/zerolog"
)

// InsightsHandler exposes the learning-progress payload to the macOS status
// bar. Read-only; the calculator does no writes.
type InsightsHandler struct {
	logger     zerolog.Logger
	calculator *interactions.LearningCalculator
}

// NewInsightsHandler wires the calculator + logger.
func NewInsightsHandler(calc *interactions.LearningCalculator, logger zerolog.Logger) *InsightsHandler {
	return &InsightsHandler{
		calculator: calc,
		logger:     logger.With().Str("handler", "insights").Logger(),
	}
}

// LearningProgress handles GET /insights/learning-progress.
func (h *InsightsHandler) LearningProgress(w http.ResponseWriter, r *http.Request) {
	if h.calculator == nil {
		writeInsightsError(w, http.StatusServiceUnavailable, "learning calculator not configured")
		return
	}
	progress, err := h.calculator.Compute(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("compute learning progress")
		writeInsightsError(w, http.StatusInternalServerError, "failed to compute learning progress")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(progress)
}

func writeInsightsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
