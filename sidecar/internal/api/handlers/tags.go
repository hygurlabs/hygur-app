// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TagHandler handles tag-related API endpoints.
type TagHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewTagHandler creates a new TagHandler.
func NewTagHandler(store *store.DB, logger zerolog.Logger) *TagHandler {
	return &TagHandler{
		store:  store,
		logger: logger.With().Str("handler", "tags").Logger(),
	}
}

// CreateTagRequest represents the request body for POST /tags.
type CreateTagRequest struct {
	Name     string `json:"name"`
	Color    string `json:"color,omitempty"`
	AutoRule string `json:"auto_rule,omitempty"`
}

// UpdateTagRequest represents the request body for PUT /tags/{id}.
type UpdateTagRequest struct {
	Name     *string `json:"name,omitempty"`
	Color    *string `json:"color,omitempty"`
	AutoRule *string `json:"auto_rule,omitempty"`
}

// TagResponse represents a tag in API responses.
type TagResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	AutoRule   string `json:"auto_rule,omitempty"`
	IsAuto     bool   `json:"is_auto"`
	UsageCount int    `json:"usage_count"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// TagListResponse wraps the list of tags for API responses.
type TagListResponse struct {
	Tags []TagResponse `json:"tags"`
}

// AddTagToItemRequest represents the request body for POST /knowledge/{content_id}/tags.
type AddTagToItemRequest struct {
	TagID   string `json:"tag_id,omitempty"`
	TagName string `json:"tag_name,omitempty"` // Alternative: create tag if doesn't exist
	Color   string `json:"color,omitempty"`    // Used when creating by name
}

// List handles GET /tags - List all tags.
func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list tags")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tags")
		return
	}

	responses := make([]TagResponse, 0, len(tags))
	for _, t := range tags {
		responses = append(responses, tagToResponse(t))
	}

	writeTagJSON(w, http.StatusOK, TagListResponse{Tags: responses})
}

// Create handles POST /tags - Create a new tag.
func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeTagError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Name == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}

	// Check if tag with this name already exists
	existing, err := h.store.GetTagByName(r.Context(), req.Name)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to check existing tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create tag")
		return
	}
	if existing != nil {
		writeTagError(w, http.StatusConflict, "CONFLICT", "tag with this name already exists")
		return
	}

	tag := &store.Tag{
		Name:     req.Name,
		Color:    req.Color,
		AutoRule: req.AutoRule,
		IsAuto:   false, // User-created tags are not auto
	}

	if err := h.store.CreateTag(r.Context(), tag); err != nil {
		h.logger.Error().Err(err).Str("name", req.Name).Msg("failed to create tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create tag")
		return
	}

	writeTagJSON(w, http.StatusCreated, tagToResponse(tag))
}

// Get handles GET /tags/{id} - Get a single tag.
func (h *TagHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	tag, err := h.store.GetTag(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag")
		return
	}

	if tag == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}

	writeTagJSON(w, http.StatusOK, tagToResponse(tag))
}

// Update handles PUT /tags/{id} - Update an existing tag.
func (h *TagHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if tag exists
	tag, err := h.store.GetTag(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag")
		return
	}

	if tag == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}

	var req UpdateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeTagError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Apply updates
	if req.Name != nil {
		if *req.Name == "" {
			writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name cannot be empty")
			return
		}
		// Check if new name conflicts with another tag
		existing, err := h.store.GetTagByName(r.Context(), *req.Name)
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to check existing tag")
			writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update tag")
			return
		}
		if existing != nil && existing.ID != id {
			writeTagError(w, http.StatusConflict, "CONFLICT", "tag with this name already exists")
			return
		}
		tag.Name = *req.Name
	}
	if req.Color != nil {
		tag.Color = *req.Color
	}
	if req.AutoRule != nil {
		tag.AutoRule = *req.AutoRule
	}

	if err := h.store.UpdateTag(r.Context(), tag); err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to update tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update tag")
		return
	}

	// Refresh tag to get updated item count
	tag, _ = h.store.GetTag(r.Context(), id)

	writeTagJSON(w, http.StatusOK, tagToResponse(tag))
}

// Delete handles DELETE /tags/{id} - Delete a tag.
func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if tag exists
	tag, err := h.store.GetTag(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check tag")
		return
	}

	if tag == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}

	if err := h.store.DeleteTag(r.Context(), id); err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to delete tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete tag")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TagItemResponse represents a knowledge item in tag context.
type TagItemResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	SourcePath string `json:"source_path,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// TagItemsResponse wraps the list of items for a tag.
type TagItemsResponse struct {
	TagID string            `json:"tag_id"`
	Items []TagItemResponse `json:"items"`
}

// ListItems handles GET /tags/{id}/items - List all items with a tag.
func (h *TagHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if tag exists
	tag, err := h.store.GetTag(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag")
		return
	}

	if tag == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}

	items, err := h.store.GetItemsForTag(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get tag items")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag items")
		return
	}

	itemResponses := make([]TagItemResponse, 0, len(items))
	for _, item := range items {
		resp := TagItemResponse{
			ID:         item.ContentID,
			Title:      item.Title,
			SourceType: item.SourceType,
			CreatedAt:  item.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
		}
		if item.SourcePath != nil {
			resp.SourcePath = *item.SourcePath
		}
		itemResponses = append(itemResponses, resp)
	}

	resp := TagItemsResponse{
		TagID: id,
		Items: itemResponses,
	}

	writeTagJSON(w, http.StatusOK, resp)
}

// GetItemTags handles GET /knowledge/{content_id}/tags - Get tags for a knowledge item.
func (h *TagHandler) GetItemTags(w http.ResponseWriter, r *http.Request) {
	contentID := chi.URLParam(r, "content_id")
	if contentID == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if item exists
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get item")
		return
	}

	if item == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	tags, err := h.store.GetTagsForItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get tags for item")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tags")
		return
	}

	responses := make([]TagResponse, 0, len(tags))
	for _, t := range tags {
		responses = append(responses, tagToResponse(t))
	}

	writeTagJSON(w, http.StatusOK, responses)
}

// AddTagToItem handles POST /knowledge/{content_id}/tags - Add a tag to a knowledge item.
func (h *TagHandler) AddTagToItem(w http.ResponseWriter, r *http.Request) {
	contentID := chi.URLParam(r, "content_id")
	if contentID == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if item exists
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get item")
		return
	}

	if item == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	var req AddTagToItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeTagError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	var tag *store.Tag

	if req.TagID != "" {
		// Use existing tag by ID
		tag, err = h.store.GetTag(r.Context(), req.TagID)
		if err != nil {
			h.logger.Error().Err(err).Str("tag_id", req.TagID).Msg("failed to get tag")
			writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag")
			return
		}
		if tag == nil {
			writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
			return
		}
	} else if req.TagName != "" {
		// Get or create tag by name
		tag, err = h.store.GetOrCreateTag(r.Context(), req.TagName, false, "")
		if err != nil {
			h.logger.Error().Err(err).Str("tag_name", req.TagName).Msg("failed to get or create tag")
			writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create tag")
			return
		}
		// Update color if provided
		if req.Color != "" && tag.Color != req.Color {
			tag.Color = req.Color
			_ = h.store.UpdateTag(r.Context(), tag)
		}
	} else {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tag_id or tag_name is required")
		return
	}

	// Add tag to item
	if err := h.store.AddTagToItem(r.Context(), contentID, tag.ID); err != nil {
		h.logger.Error().Err(err).
			Str("content_id", contentID).
			Str("tag_id", tag.ID).
			Msg("failed to add tag to item")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to add tag")
		return
	}

	// Refresh tag to get updated item count
	tag, _ = h.store.GetTag(r.Context(), tag.ID)

	writeTagJSON(w, http.StatusOK, tagToResponse(tag))
}

// RemoveTagFromItem handles DELETE /knowledge/{content_id}/tags/{tag_id} - Remove a tag from an item.
func (h *TagHandler) RemoveTagFromItem(w http.ResponseWriter, r *http.Request) {
	contentID := chi.URLParam(r, "content_id")
	tagID := chi.URLParam(r, "tag_id")

	if contentID == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}
	if tagID == "" {
		writeTagError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tag_id is required")
		return
	}

	// Check if item exists
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get item")
		return
	}

	if item == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	// Check if tag exists
	tag, err := h.store.GetTag(r.Context(), tagID)
	if err != nil {
		h.logger.Error().Err(err).Str("tag_id", tagID).Msg("failed to get tag")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get tag")
		return
	}

	if tag == nil {
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not found")
		return
	}

	// Remove tag from item
	if err := h.store.RemoveTagFromItem(r.Context(), contentID, tagID); err != nil {
		h.logger.Error().Err(err).
			Str("content_id", contentID).
			Str("tag_id", tagID).
			Msg("failed to remove tag from item")
		writeTagError(w, http.StatusNotFound, "NOT_FOUND", "tag not associated with item")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DedupeResponse summarizes a /tags/dedupe run.
type DedupeResponse struct {
	Merges []store.DedupeResult `json:"merges"`
	Count  int                  `json:"count"`
}

// Dedupe handles POST /tags/dedupe — collapses tags whose names normalize
// to the same key (case + accent insensitive) by merging losers into the
// most-used winner per group.
func (h *TagHandler) Dedupe(w http.ResponseWriter, r *http.Request) {
	merges, err := h.store.DedupeTags(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to dedupe tags")
		writeTagError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to dedupe tags")
		return
	}
	writeTagJSON(w, http.StatusOK, DedupeResponse{Merges: merges, Count: len(merges)})
}

// Helper functions

func tagToResponse(t *store.Tag) TagResponse {
	return TagResponse{
		ID:         t.ID,
		Name:       t.Name,
		Color:      t.Color,
		AutoRule:   t.AutoRule,
		IsAuto:     t.IsAuto,
		UsageCount: t.ItemCount,
		CreatedAt:  t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  t.UpdatedAt.Format(time.RFC3339),
	}
}

func writeTagJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeTagError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}
