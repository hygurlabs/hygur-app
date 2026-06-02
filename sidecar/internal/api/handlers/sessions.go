package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// SessionsHandler exposes the persisted chat transcripts (chat_sessions /
// chat_messages) so the WebUI can list past conversations and reopen one.
type SessionsHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewSessionsHandler builds a SessionsHandler.
func NewSessionsHandler(db *store.DB, logger zerolog.Logger) *SessionsHandler {
	return &SessionsHandler{store: db, logger: logger.With().Str("handler", "sessions").Logger()}
}

// SessionSummaryDTO is one row in the sessions list.
type SessionSummaryDTO struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	ProjectID    *string `json:"project_id,omitempty"`
	MessageCount int     `json:"message_count"`
	LastMessage  string  `json:"last_message,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// ChatMessageDTO is one turn in a session detail.
type ChatMessageDTO struct {
	ID        string      `json:"id"`
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	Sources   []RAGSource `json:"sources,omitempty"`
	CreatedAt string      `json:"created_at"`
}

// SessionDetailDTO is a full conversation with its turns.
type SessionDetailDTO struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	ProjectID *string          `json:"project_id,omitempty"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
	Messages  []ChatMessageDTO `json:"messages"`
}

// List handles GET /sessions.
func (h *SessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeSessionsError(w, http.StatusServiceUnavailable, "sessions store not configured")
		return
	}
	sessions, err := h.store.ListChatSessions(r.Context(), 200)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list sessions")
		writeSessionsError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	out := make([]SessionSummaryDTO, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, SessionSummaryDTO{
			ID:           s.SessionID,
			Title:        s.Title,
			ProjectID:    s.ProjectID,
			MessageCount: s.MessageCount,
			LastMessage:  truncatePreview(s.LastMessage, 120),
			CreatedAt:    s.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    s.UpdatedAt.Format(time.RFC3339),
		})
	}
	writeSessionsJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// Get handles GET /sessions/{id} — returns the session with its messages.
func (h *SessionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeSessionsError(w, http.StatusServiceUnavailable, "sessions store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	session, err := h.store.GetChatSession(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get session")
		writeSessionsError(w, http.StatusInternalServerError, "failed to get session")
		return
	}
	if session == nil {
		writeSessionsError(w, http.StatusNotFound, "session not found")
		return
	}
	msgs, err := h.store.ListChatMessages(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to list session messages")
		writeSessionsError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	out := SessionDetailDTO{
		ID:        session.SessionID,
		Title:     session.Title,
		ProjectID: session.ProjectID,
		CreatedAt: session.CreatedAt.Format(time.RFC3339),
		UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
		Messages:  make([]ChatMessageDTO, 0, len(msgs)),
	}
	for _, m := range msgs {
		dto := ChatMessageDTO{
			ID:        m.MessageID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		}
		if m.Sources != "" {
			var srcs []RAGSource
			if err := json.Unmarshal([]byte(m.Sources), &srcs); err == nil {
				dto.Sources = srcs
			}
		}
		out.Messages = append(out.Messages, dto)
	}
	writeSessionsJSON(w, http.StatusOK, out)
}

// UpdateSessionRequest patches title and/or project link.
type UpdateSessionRequest struct {
	Title     *string `json:"title,omitempty"`
	ProjectID *string `json:"project_id,omitempty"`
}

// Update handles PUT /sessions/{id}.
func (h *SessionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeSessionsError(w, http.StatusServiceUnavailable, "sessions store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	var req UpdateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSessionsError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Title == nil && req.ProjectID == nil {
		writeSessionsError(w, http.StatusBadRequest, "at least one field required")
		return
	}
	if err := h.store.UpdateChatSession(r.Context(), id, req.Title, req.ProjectID); err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to update session")
		writeSessionsError(w, http.StatusInternalServerError, "failed to update session")
		return
	}
	h.Get(w, r)
}

// Delete handles DELETE /sessions/{id}.
func (h *SessionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeSessionsError(w, http.StatusServiceUnavailable, "sessions store not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteChatSession(r.Context(), id); err != nil {
		writeSessionsError(w, http.StatusNotFound, "session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeSessionsJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeSessionsError(w http.ResponseWriter, status int, message string) {
	writeSessionsJSON(w, status, map[string]string{"error": message})
}

// truncatePreview shortens a message preview for the sessions list, collapsing
// to a single line.
func truncatePreview(s string, max int) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
