package handlers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// ProjectTimeline returns a project's items as a date-sorted exchange timeline
// (date + interlocutor + subject), newest first, for the per-project Follow-up
// view (W7). GET /knowledge/project-timeline?project_id=
func (h *KnowledgeHandler) ProjectTimeline(w http.ResponseWriter, r *http.Request) {
	pid := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if h.store == nil || pid == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "project_id required")
		return
	}
	items, err := h.store.GetItemsForProject(r.Context(), pid)
	if err != nil {
		h.logger.Error().Err(err).Msg("project timeline: list items failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load project items")
		return
	}

	type row struct {
		ContentID  string `json:"content_id"`
		Title      string `json:"title"`
		SourceType string `json:"source_type"`
		From       string `json:"from"`
		Date       string `json:"date"` // RFC3339; canonical date, else created_at
	}
	out := make([]row, 0, len(items))
	for _, it := range items {
		date := it.CreatedAt.UTC().Format(time.RFC3339)
		if cd := store.GetCanonicalDate(it); !cd.IsZero() {
			date = cd.UTC().Format(time.RFC3339)
		}
		from := ""
		if it.Metadata != nil {
			if s, _ := it.Metadata["mail_from"].(string); strings.TrimSpace(s) != "" {
				from = s
			} else if s, _ := it.Metadata["from"].(string); strings.TrimSpace(s) != "" {
				from = s
			}
		}
		out = append(out, row{it.ContentID, strings.TrimSpace(it.Title), it.SourceType, from, date})
	}
	// Newest first. RFC3339 (UTC) sorts chronologically as a string.
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })

	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"items": out})
}
