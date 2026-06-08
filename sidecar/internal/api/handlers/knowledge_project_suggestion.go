package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// DismissProjectSuggestion clears the cached project suggestion for an item so it
// stops being offered in the detail panel. DELETE
// /knowledge/{content_id}/project-suggestion. (Accepting a suggestion just links
// the project — the chip then hides because the item has a project.)
func (h *KnowledgeHandler) DismissProjectSuggestion(w http.ResponseWriter, r *http.Request) {
	contentID := chi.URLParam(r, "content_id")
	if h.store == nil || contentID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id required")
		return
	}
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil || item == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "item not found")
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata["suggested_project_id"] = "" // empty = classified, no suggestion
	if err := h.store.UpdateKnowledgeItem(r.Context(), item); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to dismiss suggestion")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
