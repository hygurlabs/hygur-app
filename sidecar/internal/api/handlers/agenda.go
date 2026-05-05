package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// AgendaActionDTO is the wire shape of a single agenda action.
type AgendaActionDTO struct {
	What        string  `json:"what"`
	DeadlineISO string  `json:"deadline_iso"`
	Priority    string  `json:"priority"`
	SourceID    string  `json:"source_id"`
	Confidence  float64 `json:"confidence"`
}

// AgendaContextResponseDTO is the full response envelope.
type AgendaContextResponseDTO struct {
	Actions     []AgendaActionDTO `json:"actions"`
	GeneratedAt string            `json:"generated_at"`
}

// AgendaHandler serves GET /agenda/context.
type AgendaHandler struct {
	extractor *agenda.Extractor
	db        *store.DB
	logger    zerolog.Logger
}

// NewAgendaHandler creates an AgendaHandler.
func NewAgendaHandler(ext *agenda.Extractor, db *store.DB, logger zerolog.Logger) *AgendaHandler {
	return &AgendaHandler{
		extractor: ext,
		db:        db,
		logger:    logger.With().Str("handler", "agenda").Logger(),
	}
}

// AgendaContext handles GET /agenda/context?range_hours=48.
func (h *AgendaHandler) AgendaContext(w http.ResponseWriter, r *http.Request) {
	rangeHours := 48
	if v := r.URL.Query().Get("range_hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rangeHours = n
		}
	}

	items, err := h.db.ListRecentItems(r.Context(), rangeHours)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list recent items")
		writeAgendaError(w, http.StatusInternalServerError, "failed to list items")
		return
	}

	actions, err := h.extractor.ExtractActions(r.Context(), items)
	if err != nil {
		h.logger.Warn().Err(err).Msg("agenda extraction error")
		actions = nil // fail-soft: return empty list
	}

	dtos := make([]AgendaActionDTO, 0, len(actions))
	for _, a := range actions {
		dtos = append(dtos, AgendaActionDTO{
			What:        a.What,
			DeadlineISO: a.DeadlineISO,
			Priority:    a.Priority,
			SourceID:    a.SourceID,
			Confidence:  a.Confidence,
		})
	}

	// Sort by deadline ASC.
	sort.Slice(dtos, func(i, j int) bool {
		return dtos[i].DeadlineISO < dtos[j].DeadlineISO
	})

	resp := AgendaContextResponseDTO{
		Actions:     dtos,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeAgendaError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
