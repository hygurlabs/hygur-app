// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// ProjectHandler handles project-related API endpoints.
type ProjectHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewProjectHandler creates a new ProjectHandler.
func NewProjectHandler(store *store.DB, logger zerolog.Logger) *ProjectHandler {
	return &ProjectHandler{
		store:  store,
		logger: logger.With().Str("handler", "projects").Logger(),
	}
}

// CreateProjectRequest represents the request body for POST /projects.
type CreateProjectRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// UpdateProjectRequest represents the request body for PUT /projects/{id}.
type UpdateProjectRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Archived    *bool     `json:"archived,omitempty"`
}

// ProjectResponse represents a project in API responses.
type ProjectResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ItemCount   int      `json:"item_count"`
	Archived    bool     `json:"archived"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// List handles GET /projects - List all projects.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list projects")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list projects")
		return
	}

	// Enrich with item_count
	responses := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		count, _ := h.store.CountProjectItems(r.Context(), p.ProjectID)
		responses = append(responses, ProjectResponse{
			ID:          p.ProjectID,
			Name:        p.Name,
			Description: stringValue(p.Description),
			Tags:        p.Tags,
			ItemCount:   count,
			Archived:    p.Archived,
			CreatedAt:   p.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   p.UpdatedAt.Format(time.RFC3339),
		})
	}

	writeProjectJSON(w, http.StatusOK, responses)
}

// Create handles POST /projects - Create a new project.
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeProjectError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Name == "" {
		writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}

	now := time.Now()
	project := &store.Project{
		ProjectID:   uuid.New().String(),
		Name:        req.Name,
		Description: stringPtr(req.Description),
		Tags:        req.Tags,
		Archived:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.store.InsertProject(r.Context(), project); err != nil {
		h.logger.Error().Err(err).Str("name", req.Name).Msg("failed to insert project")
		// Check for unique constraint violation
		writeProjectError(w, http.StatusConflict, "CONFLICT", "project with this name already exists")
		return
	}

	resp := ProjectResponse{
		ID:          project.ProjectID,
		Name:        project.Name,
		Description: req.Description,
		Tags:        project.Tags,
		ItemCount:   0,
		Archived:    false,
		CreatedAt:   project.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   project.UpdatedAt.Format(time.RFC3339),
	}

	writeProjectJSON(w, http.StatusCreated, resp)
}

// Get handles GET /projects/{id} - Get a single project.
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get project")
		return
	}

	if project == nil {
		writeProjectError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}

	count, _ := h.store.CountProjectItems(r.Context(), id)

	resp := ProjectResponse{
		ID:          project.ProjectID,
		Name:        project.Name,
		Description: stringValue(project.Description),
		Tags:        project.Tags,
		ItemCount:   count,
		Archived:    project.Archived,
		CreatedAt:   project.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   project.UpdatedAt.Format(time.RFC3339),
	}

	writeProjectJSON(w, http.StatusOK, resp)
}

// ProjectItemResponse represents a knowledge item in project context.
type ProjectItemResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	SourcePath string `json:"source_path,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ProjectItemsResponse wraps the list of items for a project.
type ProjectItemsResponse struct {
	ProjectID string                `json:"project_id"`
	Items     []ProjectItemResponse `json:"items"`
}

// ListItems handles GET /projects/{id}/items - List all items in a project.
func (h *ProjectHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if project exists
	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get project")
		return
	}

	if project == nil {
		writeProjectError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}

	items, err := h.store.GetItemsForProject(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get project items")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get project items")
		return
	}

	itemResponses := make([]ProjectItemResponse, 0, len(items))
	for _, item := range items {
		resp := ProjectItemResponse{
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

	resp := ProjectItemsResponse{
		ProjectID: id,
		Items:     itemResponses,
	}

	writeProjectJSON(w, http.StatusOK, resp)
}

// Update handles PUT /projects/{id} - Update an existing project.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if project exists
	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get project")
		return
	}

	if project == nil {
		writeProjectError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}

	var req UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeProjectError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Apply updates
	if req.Name != nil {
		if *req.Name == "" {
			writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name cannot be empty")
			return
		}
		project.Name = *req.Name
	}
	if req.Description != nil {
		project.Description = req.Description
	}
	if req.Tags != nil {
		project.Tags = *req.Tags
	}
	if req.Archived != nil {
		project.Archived = *req.Archived
	}

	if err := h.store.UpdateProject(r.Context(), project); err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to update project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update project")
		return
	}

	count, _ := h.store.CountProjectItems(r.Context(), id)

	resp := ProjectResponse{
		ID:          project.ProjectID,
		Name:        project.Name,
		Description: stringValue(project.Description),
		Tags:        project.Tags,
		ItemCount:   count,
		Archived:    project.Archived,
		CreatedAt:   project.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   project.UpdatedAt.Format(time.RFC3339),
	}

	writeProjectJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /projects/{id} - Delete a project.
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeProjectError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id is required")
		return
	}

	// Check if project exists
	project, err := h.store.GetProject(r.Context(), id)
	if err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to get project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check project")
		return
	}

	if project == nil {
		writeProjectError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}

	if err := h.store.DeleteProject(r.Context(), id); err != nil {
		h.logger.Error().Err(err).Str("id", id).Msg("failed to delete project")
		writeProjectError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete project")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Helper functions

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// writeProjectJSON writes a JSON response with the given status code.
func writeProjectJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeProjectError writes a JSON error response.
func writeProjectError(w http.ResponseWriter, status int, code, message string) {
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
