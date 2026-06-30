package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// EngramHandler serves the Engram dossier — a subject's consolidated memory (network,
// strength-ordered timeline, live/dead decisions/contradictions), assembled
// deterministically from the entity index + Hebbian graph + consolidation signals.
// Read-only; no LLM.
type EngramHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewEngramHandler builds the handler. store may be nil (the endpoint then returns 503).
func NewEngramHandler(db *store.DB, logger zerolog.Logger) *EngramHandler {
	return &EngramHandler{store: db, logger: logger.With().Str("handler", "engram").Logger()}
}

// Dossier handles GET /engrams/{norm} — the consolidated dossier for one subject. The
// {norm} segment is normalized server-side, so a raw subject ("Acme") works too.
func (h *EngramHandler) Dossier(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	norm := chi.URLParam(r, "norm")
	if strings.TrimSpace(norm) == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "subject is required")
		return
	}
	eng, err := retrieval.AssembleEngram(r.Context(), h.store, norm, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Str("subject", norm).Msg("engram assembly failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "engram assembly failed")
		return
	}
	if eng == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "no such subject")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, eng)
}
