package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TaskHandler serves the task list. A task is a note-like knowledge_item
// (source_type="task"): a Markdown body, tags and a project link like a note,
// plus task state (status, due_date) in task_attrs. Bodies are indexed, so tasks
// are searchable and usable by the assistant like notes.
type TaskHandler struct {
	store            *store.DB
	embeddingService *llm.EmbeddingService
	logger           zerolog.Logger
}

// NewTaskHandler creates a TaskHandler.
func NewTaskHandler(store *store.DB, logger zerolog.Logger) *TaskHandler {
	return &TaskHandler{store: store, logger: logger.With().Str("handler", "tasks").Logger()}
}

// SetEmbeddingService wires the embedding service so task bodies are indexed
// (chunked + embedded) like notes. Optional; without it bodies still persist and
// stay searchable via FTS.
func (h *TaskHandler) SetEmbeddingService(svc *llm.EmbeddingService) { h.embeddingService = svc }

// TaskResponse is a task with its note-like properties (body, tags, project)
// hydrated alongside the task state.
type TaskResponse struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Body      string        `json:"body"`
	Status    string        `json:"status"`
	DueDate   string        `json:"due_date,omitempty"`
	ProjectID *string       `json:"project_id,omitempty"`
	Tags      []TagResponse `json:"tags"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

// toResponse hydrates a task's project + tags for the API.
func (h *TaskHandler) toResponse(ctx context.Context, t *store.Task) TaskResponse {
	projectID, err := h.store.GetProjectIDForItem(ctx, t.ID)
	if err != nil {
		h.logger.Warn().Err(err).Str("content_id", t.ID).Msg("get project for task")
	}
	tags, err := h.store.GetTagsForItem(ctx, t.ID)
	if err != nil {
		tags = []*store.Tag{}
	}
	tagResponses := make([]TagResponse, 0, len(tags))
	for _, tag := range tags {
		tagResponses = append(tagResponses, TagResponse{
			ID: tag.ID, Name: tag.Name, Color: tag.Color, AutoRule: tag.AutoRule,
			IsAuto: tag.IsAuto, UsageCount: tag.ItemCount,
			CreatedAt: tag.CreatedAt.Format(time.RFC3339), UpdatedAt: tag.UpdatedAt.Format(time.RFC3339),
		})
	}
	return TaskResponse{
		ID: t.ID, Title: t.Title, Body: t.Body, Status: t.Status, DueDate: t.DueDate,
		ProjectID: projectID, Tags: tagResponses, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

// List handles GET /tasks?project_id=&status=
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.store.ListTasks(r.Context(), r.URL.Query().Get("project_id"), r.URL.Query().Get("status"))
	if err != nil {
		h.logger.Error().Err(err).Msg("list tasks failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tasks")
		return
	}
	out := make([]TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, h.toResponse(r.Context(), t))
	}
	writeKnowledgeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// Get handles GET /tasks/{id}
func (h *TaskHandler) Get(w http.ResponseWriter, r *http.Request) {
	t, err := h.store.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil || t == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, h.toResponse(r.Context(), t))
}

type createTaskRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Status    string   `json:"status"`
	DueDate   string   `json:"due_date"`
	ProjectID *string  `json:"project_id"`
	TagIDs    []string `json:"tag_ids"`
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

	contentID := "task:" + uuid.New().String()
	now := time.Now()
	normalized := ingest.NormalizeText(req.Body)
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     store.SourceTypeTask,
		Title:          strings.TrimSpace(req.Title),
		NormalizedText: normalized,
		Metadata:       map[string]any{"created_from": "tool", "canonical_date": now.UTC().Format(time.RFC3339)},
		VersionID:      uuid.New().String(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.store.InsertKnowledgeItem(r.Context(), item); err != nil {
		h.logger.Error().Err(err).Msg("create task failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create task")
		return
	}
	// Index the body (searchable + RAG) when there is one. An empty body is fine
	// for a quick to-do — nothing to chunk.
	if strings.TrimSpace(normalized) != "" {
		if _, _, idxErr := ingest.IndexSections(r.Context(), h.store, h.embeddingService, contentID, normalized, ingest.DefaultChunkTokenBudget, now); idxErr != nil {
			h.logger.Warn().Err(idxErr).Str("id", contentID).Msg("index task body; still searchable via FTS")
		}
	}
	if err := h.store.UpsertTaskAttrs(r.Context(), contentID, req.Status, strings.TrimSpace(req.DueDate)); err != nil {
		h.logger.Error().Err(err).Msg("write task attrs failed")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create task")
		return
	}
	h.applyProjectAndTags(r.Context(), contentID, req.ProjectID, req.TagIDs)

	t, _ := h.store.GetTask(r.Context(), contentID)
	if t == nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load task")
		return
	}
	writeKnowledgeJSON(w, http.StatusCreated, h.toResponse(r.Context(), t))
}

type patchTaskRequest struct {
	Title     *string  `json:"title"`
	Body      *string  `json:"body"`
	Status    *string  `json:"status"`
	DueDate   *string  `json:"due_date"`
	ProjectID *string  `json:"project_id"`
	TagIDs    []string `json:"tag_ids"`
}

// Patch handles PATCH /tasks/{id}
func (h *TaskHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, err := h.store.GetKnowledgeItem(r.Context(), id)
	if err != nil || item == nil || item.SourceType != store.SourceTypeTask {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	var req patchTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	itemChanged, bodyChanged := false, false
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		item.Title = strings.TrimSpace(*req.Title)
		itemChanged = true
	}
	if req.Body != nil {
		item.NormalizedText = ingest.NormalizeText(*req.Body)
		itemChanged, bodyChanged = true, true
	}
	if itemChanged {
		item.VersionID = uuid.New().String()
		item.UpdatedAt = time.Now()
		if bodyChanged {
			if _, _, idxErr := ingest.IndexSections(r.Context(), h.store, h.embeddingService, id, item.NormalizedText, ingest.DefaultChunkTokenBudget, time.Now()); idxErr != nil {
				h.logger.Warn().Err(idxErr).Str("id", id).Msg("re-index task body; still searchable via FTS")
			}
		}
		if err := h.store.UpdateKnowledgeItem(r.Context(), item); err != nil {
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update task")
			return
		}
	}

	// Task state: read current, apply provided fields, upsert.
	cur, _ := h.store.GetTask(r.Context(), id)
	status, due := "open", ""
	if cur != nil {
		status, due = cur.Status, cur.DueDate
	}
	if req.Status != nil {
		status = *req.Status
	}
	if req.DueDate != nil {
		due = strings.TrimSpace(*req.DueDate)
	}
	if req.Status != nil || req.DueDate != nil {
		if err := h.store.UpsertTaskAttrs(r.Context(), id, status, due); err != nil {
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update task")
			return
		}
	}

	if req.ProjectID != nil {
		_ = h.store.UnlinkFromProject(r.Context(), id)
		if *req.ProjectID != "" {
			if err := h.store.LinkToProject(r.Context(), id, *req.ProjectID); err != nil {
				h.logger.Warn().Err(err).Str("id", id).Msg("link task to project")
			}
		}
	}
	if req.TagIDs != nil {
		_ = h.store.RemoveAllTagsFromItem(r.Context(), id)
		for _, tagID := range req.TagIDs {
			if err := h.store.AddTagToItem(r.Context(), id, tagID); err != nil {
				h.logger.Warn().Err(err).Str("id", id).Str("tag_id", tagID).Msg("add tag to task")
			}
		}
	}

	t, _ := h.store.GetTask(r.Context(), id)
	if t == nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load task")
		return
	}
	writeKnowledgeJSON(w, http.StatusOK, h.toResponse(r.Context(), t))
}

// Delete handles DELETE /tasks/{id} — removes the knowledge_item (task_attrs,
// project_links and item_tags are cleaned up with it).
func (h *TaskHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = h.store.RemoveAllTagsFromItem(r.Context(), id)
	_ = h.store.UnlinkFromProject(r.Context(), id)
	if err := h.store.DeleteKnowledgeItem(r.Context(), id); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete task")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyProjectAndTags links a freshly created task to a project and its tags,
// best-effort (a bad project/tag id is logged, not fatal).
func (h *TaskHandler) applyProjectAndTags(ctx context.Context, contentID string, projectID *string, tagIDs []string) {
	if projectID != nil && *projectID != "" {
		if err := h.store.LinkToProject(ctx, contentID, *projectID); err != nil {
			h.logger.Warn().Err(err).Str("id", contentID).Msg("link task to project")
		}
	}
	for _, tagID := range tagIDs {
		if err := h.store.AddTagToItem(ctx, contentID, tagID); err != nil {
			h.logger.Warn().Err(err).Str("id", contentID).Str("tag_id", tagID).Msg("add tag to task")
		}
	}
}
