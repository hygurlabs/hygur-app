// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// NotesHandler handles note-related API endpoints.
type NotesHandler struct {
	tool             *tools.CreateNoteTool
	store            *store.DB
	embeddingService *llm.EmbeddingService
	logger           zerolog.Logger
}

// NewNotesHandler creates a new NotesHandler.
func NewNotesHandler(tool *tools.CreateNoteTool, logger zerolog.Logger) *NotesHandler {
	return &NotesHandler{
		tool:   tool,
		logger: logger.With().Str("handler", "notes").Logger(),
	}
}

// SetStore sets the store for listing/getting notes.
func (h *NotesHandler) SetStore(db *store.DB) {
	h.store = db
}

// SetEmbeddingService sets the embedding service for re-embedding on updates.
func (h *NotesHandler) SetEmbeddingService(svc *llm.EmbeddingService) {
	h.embeddingService = svc
}

// CreateNoteRequest represents the request body for POST /notes.
type CreateNoteRequest struct {
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	ProjectID *string  `json:"project_id,omitempty"`
	TagIDs    []string `json:"tag_ids,omitempty"`
}

// CreateNoteResponse represents the response for POST /notes.
type CreateNoteResponse struct {
	ContentID  string `json:"content_id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	CreatedAt  string `json:"created_at"`
}

// Create handles POST /notes.
func (h *NotesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeNotesError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate required fields
	if req.Title == "" {
		writeNotesError(w, http.StatusBadRequest, "VALIDATION_ERROR", "title is required")
		return
	}
	if req.Content == "" {
		writeNotesError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content is required")
		return
	}

	// Check if tool is available
	if h.tool == nil {
		writeNotesError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notes tool not configured")
		return
	}

	// Create the note using the tool
	toolReq := tools.CreateNoteRequest{
		Title:     req.Title,
		Content:   req.Content,
		ProjectID: req.ProjectID,
		Tags:      req.TagIDs,
	}

	result, err := h.tool.Run(r.Context(), toolReq)
	if err != nil {
		h.logger.Error().Err(err).Str("title", req.Title).Msg("failed to create note")

		// Check for specific error types
		errMsg := err.Error()
		switch {
		case contains(errMsg, "project not found"):
			writeNotesError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		case contains(errMsg, "validation error"):
			writeNotesError(w, http.StatusBadRequest, "VALIDATION_ERROR", errMsg)
		default:
			writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create note")
		}
		return
	}

	h.logger.Info().
		Str("content_id", result.ContentID).
		Str("title", req.Title).
		Msg("note created")

	// Return full note response with tags and project
	if h.store != nil {
		item, err := h.store.GetKnowledgeItem(r.Context(), result.ContentID)
		if err == nil && item != nil {
			note, err := h.itemToNoteResponse(r.Context(), item)
			if err == nil {
				writeNotesJSON(w, http.StatusCreated, note)
				return
			}
		}
	}

	// Fallback to minimal response if store unavailable
	resp := NoteResponse{
		ID:        result.ContentID,
		Title:     result.Title,
		Content:   req.Content,
		ProjectID: req.ProjectID,
		Tags:      []TagResponse{},
		CreatedAt: result.CreatedAt.Format(time.RFC3339),
		UpdatedAt: result.CreatedAt.Format(time.RFC3339),
	}
	writeNotesJSON(w, http.StatusCreated, resp)
}

// NoteResponse represents a note in API responses.
type NoteResponse struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Content   string        `json:"content"`
	ProjectID *string       `json:"project_id,omitempty"`
	Tags      []TagResponse `json:"tags"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

// NoteListResponse wraps the list of notes for API responses.
type NoteListResponse struct {
	Notes []NoteResponse `json:"notes"`
}

// List handles GET /notes - List all notes.
func (h *NotesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeNotesError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notes store not configured")
		return
	}

	items, err := h.store.ListKnowledgeItemsBySourceType(r.Context(), "note", 1000, 0)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list notes")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list notes")
		return
	}

	notes := make([]NoteResponse, 0, len(items))
	for _, item := range items {
		note, err := h.itemToNoteResponse(r.Context(), item)
		if err != nil {
			h.logger.Warn().Err(err).Str("content_id", item.ContentID).Msg("failed to convert note")
			continue
		}
		notes = append(notes, note)
	}

	writeNotesJSON(w, http.StatusOK, NoteListResponse{Notes: notes})
}

// Get handles GET /notes/{id} - Get a single note.
func (h *NotesHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeNotesError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notes store not configured")
		return
	}

	noteID := chi.URLParam(r, "id")
	if noteID == "" {
		writeNotesError(w, http.StatusBadRequest, "BAD_REQUEST", "note id is required")
		return
	}

	item, err := h.store.GetKnowledgeItem(r.Context(), noteID)
	if err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to get note")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get note")
		return
	}
	if item == nil || item.SourceType != "note" {
		writeNotesError(w, http.StatusNotFound, "NOT_FOUND", "note not found")
		return
	}

	note, err := h.itemToNoteResponse(r.Context(), item)
	if err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to convert note")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get note")
		return
	}

	writeNotesJSON(w, http.StatusOK, note)
}

// Delete handles DELETE /notes/{id} - Delete a note.
func (h *NotesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeNotesError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notes store not configured")
		return
	}

	noteID := chi.URLParam(r, "id")
	if noteID == "" {
		writeNotesError(w, http.StatusBadRequest, "BAD_REQUEST", "note id is required")
		return
	}

	// Verify it's a note
	item, err := h.store.GetKnowledgeItem(r.Context(), noteID)
	if err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to get note for deletion")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete note")
		return
	}
	if item == nil || item.SourceType != "note" {
		writeNotesError(w, http.StatusNotFound, "NOT_FOUND", "note not found")
		return
	}

	if err := h.store.DeleteKnowledgeItem(r.Context(), noteID); err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to delete note")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete note")
		return
	}

	h.logger.Info().Str("id", noteID).Msg("note deleted")
	w.WriteHeader(http.StatusNoContent)
}

// UpdateNoteRequest represents the request body for PUT /notes/{id}.
type UpdateNoteRequest struct {
	Title     *string  `json:"title,omitempty"`
	Content   *string  `json:"content,omitempty"`
	ProjectID *string  `json:"project_id,omitempty"`
	TagIDs    []string `json:"tag_ids,omitempty"`
}

// Update handles PUT /notes/{id} - Update an existing note.
func (h *NotesHandler) Update(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeNotesError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notes store not configured")
		return
	}

	noteID := chi.URLParam(r, "id")
	if noteID == "" {
		writeNotesError(w, http.StatusBadRequest, "BAD_REQUEST", "note id is required")
		return
	}

	// Parse request
	var req UpdateNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse update request body")
		writeNotesError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate - at least one field must be provided
	if req.Title == nil && req.Content == nil && req.ProjectID == nil && len(req.TagIDs) == 0 {
		writeNotesError(w, http.StatusBadRequest, "VALIDATION_ERROR", "at least one field must be provided")
		return
	}

	// Get existing note
	item, err := h.store.GetKnowledgeItem(r.Context(), noteID)
	if err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to get note for update")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get note")
		return
	}
	if item == nil || item.SourceType != "note" {
		writeNotesError(w, http.StatusNotFound, "NOT_FOUND", "note not found")
		return
	}

	// Track if content changed (requires re-chunking)
	contentChanged := false

	// Apply updates
	if req.Title != nil && *req.Title != "" {
		item.Title = *req.Title
	}
	if req.Content != nil {
		if *req.Content == "" {
			writeNotesError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content cannot be empty")
			return
		}
		item.NormalizedText = ingest.NormalizeText(*req.Content)
		contentChanged = true
	}

	// Update version ID for change tracking
	item.VersionID = uuid.New().String()

	// If content changed, we need to re-chunk and re-embed
	if contentChanged {
		// Delete old chunks (cascade deletes vectors too via trigger)
		if err := h.store.DeleteChunksByContentID(r.Context(), noteID); err != nil {
			h.logger.Error().Err(err).Str("id", noteID).Msg("failed to delete old chunks")
			writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update note")
			return
		}

		// Create new chunks
		chunks := ingest.ChunkText(item.NormalizedText, ingest.DefaultChunkOptions())
		now := time.Now()

		var storeChunks []store.Chunk
		for _, chunk := range chunks {
			chunkID := uuid.New().String()

			storeChunk := &store.Chunk{
				ChunkID:   chunkID,
				ContentID: noteID,
				ChunkHash: chunkID[:16], // Simple hash for notes
				Text:      chunk.Content,
				Metadata: map[string]any{
					"index":        chunk.Index,
					"start_offset": chunk.StartOffset,
					"end_offset":   chunk.EndOffset,
				},
				CreatedAt: now,
			}

			if err := h.store.InsertChunk(r.Context(), storeChunk); err != nil {
				h.logger.Error().Err(err).Str("id", noteID).Int("chunk", chunk.Index).Msg("failed to insert chunk")
				writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update note")
				return
			}

			storeChunks = append(storeChunks, *storeChunk)
		}

		// Re-embed if service available
		if h.embeddingService != nil && len(storeChunks) > 0 {
			if err := h.embeddingService.BatchEmbedAndStore(r.Context(), storeChunks); err != nil {
				h.logger.Warn().Err(err).Str("id", noteID).Msg("failed to generate embeddings")
				// Don't fail - note is still searchable via FTS
			}
		}
	}

	// Update the knowledge item
	if err := h.store.UpdateKnowledgeItem(r.Context(), item); err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to update note")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update note")
		return
	}

	// Handle project link update
	if req.ProjectID != nil {
		// First, unlink from any existing project
		if err := h.store.UnlinkFromProject(r.Context(), noteID); err != nil {
			h.logger.Warn().Err(err).Str("id", noteID).Msg("failed to unlink from project")
		}

		// Link to new project if specified
		if *req.ProjectID != "" {
			// Verify project exists
			project, err := h.store.GetProject(r.Context(), *req.ProjectID)
			if err != nil {
				h.logger.Error().Err(err).Str("project_id", *req.ProjectID).Msg("failed to check project")
				writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update note")
				return
			}
			if project == nil {
				writeNotesError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
				return
			}

			if err := h.store.LinkToProject(r.Context(), noteID, *req.ProjectID); err != nil {
				h.logger.Error().Err(err).Str("id", noteID).Str("project_id", *req.ProjectID).Msg("failed to link to project")
				writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update note")
				return
			}
		}
	}

	// Handle tag updates
	if len(req.TagIDs) > 0 {
		// Clear existing tags
		if err := h.store.RemoveAllTagsFromItem(r.Context(), noteID); err != nil {
			h.logger.Warn().Err(err).Str("id", noteID).Msg("failed to clear tags")
		}

		// Add new tags
		for _, tagID := range req.TagIDs {
			if err := h.store.AddTagToItem(r.Context(), noteID, tagID); err != nil {
				h.logger.Warn().Err(err).Str("id", noteID).Str("tag_id", tagID).Msg("failed to add tag")
			}
		}
	}

	h.logger.Info().Str("id", noteID).Bool("content_changed", contentChanged).Msg("note updated")

	// Return updated note
	note, err := h.itemToNoteResponse(r.Context(), item)
	if err != nil {
		h.logger.Error().Err(err).Str("id", noteID).Msg("failed to convert note response")
		writeNotesError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get updated note")
		return
	}

	writeNotesJSON(w, http.StatusOK, note)
}

// itemToNoteResponse converts a KnowledgeItem to NoteResponse with tags and project.
func (h *NotesHandler) itemToNoteResponse(ctx context.Context, item *store.KnowledgeItem) (NoteResponse, error) {
	// Get project ID
	projectID, err := h.store.GetProjectIDForItem(ctx, item.ContentID)
	if err != nil {
		h.logger.Warn().Err(err).Str("content_id", item.ContentID).Msg("failed to get project for note")
	}

	// Get tags
	tags, err := h.store.GetTagsForItem(ctx, item.ContentID)
	if err != nil {
		h.logger.Warn().Err(err).Str("content_id", item.ContentID).Msg("failed to get tags for note")
		tags = []*store.Tag{}
	}

	tagResponses := make([]TagResponse, 0, len(tags))
	for _, t := range tags {
		tagResponses = append(tagResponses, TagResponse{
			ID:         t.ID,
			Name:       t.Name,
			Color:      t.Color,
			AutoRule:   t.AutoRule,
			IsAuto:     t.IsAuto,
			UsageCount: t.ItemCount,
			CreatedAt:  t.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  t.UpdatedAt.Format(time.RFC3339),
		})
	}

	return NoteResponse{
		ID:        item.ContentID,
		Title:     item.Title,
		Content:   item.NormalizedText,
		ProjectID: projectID,
		Tags:      tagResponses,
		CreatedAt: item.CreatedAt.Format(time.RFC3339),
		UpdatedAt: item.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// writeNotesJSON writes a JSON response with the given status code.
func writeNotesJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeNotesError writes a JSON error response.
func writeNotesError(w http.ResponseWriter, status int, code, message string) {
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

// contains checks if substr is in s (helper to avoid importing strings).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
