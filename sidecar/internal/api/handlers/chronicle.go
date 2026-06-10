package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// ChronicleHandler serves the Chronicle — Hygur's grounded narrative of the user's
// life, read as a book. Acts are written nightly by the ChronicleWriter.
type ChronicleHandler struct {
	store  *store.DB
	writer *scheduler.ChronicleWriter
	logger zerolog.Logger
}

// NewChronicleHandler builds the handler. writer may be nil (manual run disabled).
func NewChronicleHandler(db *store.DB, writer *scheduler.ChronicleWriter, logger zerolog.Logger) *ChronicleHandler {
	return &ChronicleHandler{store: db, writer: writer, logger: logger.With().Str("handler", "chronicle").Logger()}
}

type chronicleChapterDTO struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	ActCount int    `json:"act_count"`
	LastDate string `json:"last_date,omitempty"`
}

type chronicleActDTO struct {
	Date     string `json:"date"`  // YYYY-MM-DD
	Title    string `json:"title"` // e.g. "12 June 2026"
	Markdown string `json:"markdown"`
}

// actDate extracts the YYYY-MM-DD from an act content_id ("chronicle:<chap>:<date>").
func actDate(contentID string) string {
	if i := strings.LastIndexByte(contentID, ':'); i >= 0 && i+1 < len(contentID) {
		return contentID[i+1:]
	}
	return ""
}

// List handles GET /chronicle — the chapters with their act counts.
func (h *ChronicleHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	chapters, err := h.store.ListChronicleChapters(r.Context())
	if err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list chapters")
		return
	}
	out := make([]chronicleChapterDTO, 0, len(chapters))
	for _, c := range chapters {
		acts, _ := h.store.ListChronicleActs(r.Context(), c.ID)
		last := ""
		if len(acts) > 0 {
			last = actDate(acts[len(acts)-1].ContentID)
		}
		out = append(out, chronicleChapterDTO{ID: c.ID, Title: c.Title, Status: c.Status, ActCount: len(acts), LastDate: last})
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"chapters": out})
}

// Get handles GET /chronicle/{id} — a chapter's acts, oldest first.
func (h *ChronicleHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	id := chi.URLParam(r, "id")
	chap, err := h.store.GetChronicleChapter(r.Context(), id)
	if err != nil || chap == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "chapter not found")
		return
	}
	acts, err := h.store.ListChronicleActs(r.Context(), id)
	if err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list acts")
		return
	}
	dtos := make([]chronicleActDTO, 0, len(acts))
	for _, a := range acts {
		dtos = append(dtos, chronicleActDTO{Date: actDate(a.ContentID), Title: a.Title, Markdown: a.NormalizedText})
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{
		"id": chap.ID, "title": chap.Title, "status": chap.Status, "acts": dtos,
	})
}

// Run handles POST /chronicle/run — generate today's act now (manual trigger, force).
func (h *ChronicleHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.writer == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "chronicle writer not configured")
		return
	}
	act, err := h.writer.WriteLifeChapter(r.Context(), time.Now(), true)
	if err != nil {
		h.logger.Warn().Err(err).Msg("manual chronicle run failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write chronicle")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"written": strings.TrimSpace(act) != "", "act": act})
}
