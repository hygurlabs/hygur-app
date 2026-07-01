package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// IdentifierLookup handles GET /engrams/lookup?entity=X&type=national_number — the
// deterministic (entity, identifier-type) → value lookup with a confidence tier + sources.
func (h *EngramHandler) IdentifierLookup(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	entity := strings.TrimSpace(r.URL.Query().Get("entity"))
	idType := strings.TrimSpace(r.URL.Query().Get("type"))
	if entity == "" || idType == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "entity and type are required")
		return
	}
	res, err := fact.LookupIdentifier(r.Context(), h.store, contradict.NormKey(entity), idType, time.Now())
	if err != nil {
		h.logger.Warn().Err(err).Msg("identifier lookup failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL", "lookup failed")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, res)
}

// EngramHandler serves the Engram dossier — a subject's consolidated memory (network,
// strength-ordered timeline, live/dead decisions/contradictions), assembled
// deterministically from the entity index + Hebbian graph + consolidation signals.
// Read-only; no LLM.
type EngramHandler struct {
	store        *store.DB
	ownerExclude []string // normalized owner-identity norms, excluded from the subject list
	logger       zerolog.Logger
}

// NewEngramHandler builds the handler. store may be nil (the endpoint then returns 503).
// ownerNames are the owner's own name/email variants (excluded from the subject list).
func NewEngramHandler(db *store.DB, ownerNames []string, logger zerolog.Logger) *EngramHandler {
	ex := make([]string, 0, len(ownerNames))
	for _, n := range ownerNames {
		if k := contradict.NormKey(n); k != "" {
			ex = append(ex, k)
		}
	}
	return &EngramHandler{store: db, ownerExclude: ex, logger: logger.With().Str("handler", "engram").Logger()}
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

// List handles GET /engrams — the discovered named subjects ranked by centrality
// (?limit=, default 50). Read-only.
func (h *EngramHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	subjects, err := h.store.TopSubjects(r.Context(), limit, h.ownerExclude)
	if err != nil {
		h.logger.Warn().Err(err).Msg("top subjects failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "top subjects failed")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"subjects": subjects})
}
