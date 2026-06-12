package handlers

import (
	"context"
	"encoding/json"
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
	Date     string   `json:"date"`  // YYYY-MM-DD
	Title    string   `json:"title"` // e.g. "12 June 2026"
	Markdown string   `json:"markdown"`
	Sources  []string `json:"sources"` // content_ids; index n-1 ↔ "[n]" anchor in the prose
	Closing  bool     `json:"closing"` // the act that closed the chapter
}

// actDate extracts the YYYY-MM-DD from an act content_id ("chronicle:<chap>:<date>").
func actDate(contentID string) string {
	if i := strings.LastIndexByte(contentID, ':'); i >= 0 && i+1 < len(contentID) {
		return contentID[i+1:]
	}
	return ""
}

// actSources reads the stored source content_ids (a []any after a JSON round-trip).
func actSources(m map[string]any) []string {
	raw, _ := m["sources"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
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
		closing, _ := a.Metadata["closing"].(bool)
		dtos = append(dtos, chronicleActDTO{
			Date: actDate(a.ContentID), Title: a.Title, Markdown: a.NormalizedText,
			Sources: actSources(a.Metadata), Closing: closing,
		})
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{
		"id": chap.ID, "title": chap.Title, "status": chap.Status, "acts": dtos,
	})
}

// Run handles POST /chronicle/run — regenerate today's acts across all chapters
// now (manual trigger, force). The pass runs in the background (it may make several
// LLM calls); the UI refetches shortly after. 202 Accepted.
func (h *ChronicleHandler) Run(w http.ResponseWriter, r *http.Request) {
	if h.writer == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "chronicle writer not configured")
		return
	}
	go func() {
		// Detached context: the pass must outlive this request.
		if _, err := h.writer.RunAll(context.Background(), time.Now(), true); err != nil {
			h.logger.Warn().Err(err).Msg("manual chronicle run failed")
		}
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// Close handles POST /chronicle/{id}/close — write a final, grounded closing act
// from the chapter's synopsis + an optional note, then mark it closed. Async (one
// LLM call); the UI refetches. 202 Accepted. The "life" chapter cannot be closed.
func (h *ChronicleHandler) Close(w http.ResponseWriter, r *http.Request) {
	if h.writer == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "chronicle writer not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "life" {
		writeKnowledgeError(w, http.StatusBadRequest, "INVALID", "the life chapter cannot be closed")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // note is optional; ignore a malformed/empty body
	note := body.Note
	go func() {
		if err := h.writer.CloseChapter(context.Background(), id, note, time.Now()); err != nil {
			h.logger.Warn().Err(err).Str("chapter", id).Msg("chronicle close failed")
		}
	}()
	writeKnowledgeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// Reopen handles POST /chronicle/{id}/reopen — reopen a closed chapter with a required
// free-text reason. The reason is staged (no LLM here); the next pass — nightly, or a
// manual "Write today's entry" — narrates the resumption from it, corroborated by any
// new traces. The "life" chapter cannot be reopened.
func (h *ChronicleHandler) Reopen(w http.ResponseWriter, r *http.Request) {
	if h.writer == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "chronicle writer not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "life" {
		writeKnowledgeError(w, http.StatusBadRequest, "INVALID", "the life chapter is always open")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.Note) == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "INVALID", "a reason for reopening is required")
		return
	}
	if err := h.writer.ReopenChapter(r.Context(), id, body.Note, time.Now()); err != nil {
		h.logger.Warn().Err(err).Str("chapter", id).Msg("chronicle reopen failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to reopen chapter")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"reopened": true})
}
