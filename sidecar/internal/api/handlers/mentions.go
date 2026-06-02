package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// MentionsHandler powers the WebUI composer's "@" typeahead: it returns
// matching projects and knowledge items (notes / mails / documents) so the user
// can attach them as context to a question.
type MentionsHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewMentionsHandler builds a MentionsHandler.
func NewMentionsHandler(db *store.DB, logger zerolog.Logger) *MentionsHandler {
	return &MentionsHandler{store: db, logger: logger.With().Str("handler", "mentions").Logger()}
}

// MentionDTO is one autocomplete entry. Type is one of: project, note, mail,
// document. For projects the ID is a project_id (→ chat focus_scope); for the
// others it is a content_id (→ document attachment).
type MentionDTO struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// Search handles GET /mentions?q=&limit=.
func (h *MentionsHandler) Search(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeMentionsJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not configured"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 8
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	ql := strings.ToLower(q)

	out := make([]MentionDTO, 0, limit*2)

	// Projects — usually few; filter client-side on name.
	if projects, err := h.store.ListProjects(r.Context()); err == nil {
		for _, p := range projects {
			if q == "" || strings.Contains(strings.ToLower(p.Name), ql) {
				out = append(out, MentionDTO{ID: p.ProjectID, Type: "project", Title: p.Name})
			}
		}
	}

	// Tags — also few; reference a tag to scope the chat to its tagged items.
	if tags, err := h.store.ListTags(r.Context()); err == nil {
		for _, t := range tags {
			if q == "" || strings.Contains(strings.ToLower(t.Name), ql) {
				out = append(out, MentionDTO{ID: t.ID, Type: "tag", Title: t.Name})
			}
		}
	}

	// Knowledge items: title search when the user typed a query, otherwise the
	// most recent items so typing just "@" lets them browse the library.
	var items []*store.KnowledgeItem
	if q != "" {
		items, _ = h.store.SearchKnowledgeItemsByTitle(r.Context(), q, limit*3)
	} else {
		items, _ = h.store.ListKnowledgeItems(r.Context(), limit*3, 0)
	}
	for _, it := range items {
		t := mentionType(it.SourceType)
		if t == "" {
			continue // briefs etc. aren't mentionable
		}
		out = append(out, MentionDTO{ID: it.ContentID, Type: t, Title: it.Title})
	}

	max := limit * 2
	if len(out) > max {
		out = out[:max]
	}
	writeMentionsJSON(w, http.StatusOK, map[string]any{"mentions": out})
}

// mentionType maps a knowledge_items source_type to a mention display type.
// Returns "" for types that shouldn't appear in mentions (generated briefs).
func mentionType(sourceType string) string {
	switch sourceType {
	case "note":
		return "note"
	case "email", "thread", "mail":
		return "mail"
	case "brief", "meeting_brief":
		return ""
	default:
		return "document"
	}
}

func writeMentionsJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
