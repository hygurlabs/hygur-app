package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TaskHandler serves the local task list (lightweight to-dos, optionally linked
// to a project and to the knowledge item they were created from).
type TaskHandler struct {
	store  *store.DB
	logger zerolog.Logger
}

// NewTaskHandler creates a TaskHandler.
func NewTaskHandler(store *store.DB, logger zerolog.Logger) *TaskHandler {
	return &TaskHandler{store: store, logger: logger.With().Str("handler", "tasks").Logger()}
}

// List handles GET /tasks?project_id=&status=
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.store.ListTasks(r.Context(), r.URL.Query().Get("project_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.logger.Error().Err(err).Msg("list tasks failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tasks")
		return
	}
	if tasks == nil {
		tasks = []*store.Task{}
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

type createTaskRequest struct {
	Title           string `json:"title"`
	DueDate         string `json:"due_date"`
	ProjectID       string `json:"project_id"`
	SourceContentID string `json:"source_content_id"`
}

// Create handles POST /tasks
func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "title required")
		return
	}
	t := &store.Task{
		Title:           strings.TrimSpace(req.Title),
		DueDate:         strings.TrimSpace(req.DueDate),
		ProjectID:       req.ProjectID,
		SourceContentID: req.SourceContentID,
	}
	if err := h.store.CreateTask(r.Context(), t); err != nil {
		h.logger.Error().Err(err).Msg("create task failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create task")
		return
	}
	writeKnowledgeJSON(w, http.StatusCreated, t)
}

type patchTaskRequest struct {
	Title   *string `json:"title"`
	Status  *string `json:"status"`
	DueDate *string `json:"due_date"`
}

// Patch handles PATCH /tasks/{id}
func (h *TaskHandler) Patch(w http.ResponseWriter, r *http.Request) {
	t, err := h.store.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil || t == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	var req patchTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.Title != nil {
		t.Title = strings.TrimSpace(*req.Title)
	}
	if req.Status != nil {
		t.Status = *req.Status
	}
	if req.DueDate != nil {
		t.DueDate = strings.TrimSpace(*req.DueDate)
	}
	if err := h.store.UpdateTask(r.Context(), t); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update task")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, t)
}

// Delete handles DELETE /tasks/{id}
func (h *TaskHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteTask(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete task")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
