package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// MemoryHandler handles memory-related API endpoints.
type MemoryHandler struct {
	store    *store.DB
	logger   zerolog.Logger
	tool     *tools.MemoryStoreTool
	toolSrch *tools.MemorySearchTool
}

// NewMemoryHandler creates a new MemoryHandler.
func NewMemoryHandler(store *store.DB, logger zerolog.Logger) *MemoryHandler {
	return &MemoryHandler{
		store:  store,
		logger: logger.With().Str("handler", "memory").Logger(),
	}
}

// SetTools sets the memory tools for the handler.
func (h *MemoryHandler) SetTools(storeTool *tools.MemoryStoreTool, searchTool *tools.MemorySearchTool) {
	h.tool = storeTool
	h.toolSrch = searchTool
}

// StoreRequest represents the request body for POST /memory/store.
type StoreRequest struct {
	MemoryType string `json:"type"`
	Content    string `json:"content"`
	ContextID  string `json:"context_id,omitempty"`
	ExpiresIn  int    `json:"expires_in,omitempty"` // minutes, 0 = never expire
}

// StoreResponse represents the response for POST /memory/store.
type StoreResponse struct {
	MemoryID  string `json:"memory_id"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// Store handles POST /memory/store - store a new memory.
func (h *MemoryHandler) Store(w http.ResponseWriter, r *http.Request) {
	var req StoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.Content == "" {
		writeMemoryError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content is required")
		return
	}

	if req.MemoryType == "" {
		req.MemoryType = string(store.MemoryFact)
	}

	// Validate memory type
	switch req.MemoryType {
	case "fact", "action", "preference":
		// OK
	default:
		writeMemoryError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid memory type")
		return
	}

	// Use the store tool to save the memory
	memoryID, err := h.tool.Store(req.Content, req.MemoryType, req.ContextID)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to store memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to store memory")
		return
	}

	resp := StoreResponse{
		MemoryID:  memoryID,
		Type:      req.MemoryType,
		Content:   req.Content,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	writeMemoryJSON(w, http.StatusCreated, resp)
}

// MemorySearchRequest represents the request body for GET /memory/search.
type MemorySearchRequest struct {
	Query      string  `json:"query,omitempty"`
	MaxResults int     `json:"max_results,omitempty"`
	MinScore   float64 `json:"min_score,omitempty"`
}

// MemorySearchResult represents a single memory search result.
type MemorySearchResult struct {
	MemoryID  string  `json:"memory_id"`
	Type      string  `json:"type"`
	Content   string  `json:"content"`
	Score     float64 `json:"score"`
	ContextID string  `json:"context_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// MemorySearchResponse wraps the list of memory search results.
type MemorySearchResponse struct {
	Memories []MemorySearchResult `json:"memories"`
	Total    int                  `json:"total"`
}

// MemorySearch handles GET /memory/search - search for memories by query.
func (h *MemoryHandler) MemorySearch(w http.ResponseWriter, r *http.Request) {
	// Parse query from URL parameters
	query := r.URL.Query().Get("q")
	if query == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "q parameter is required")
		return
	}

	results, err := h.toolSrch.Search(query, 10, 0)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to search memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to search memories")
		return
	}

	// Convert to response format
	var searchResults []MemorySearchResult
	for _, r := range results {
		searchResults = append(searchResults, MemorySearchResult{
			MemoryID:  r.MemoryID,
			Type:      string(r.Type),
			Content:   r.Content,
			Score:     r.Score,
			ContextID: r.ContextID,
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}

	writeMemoryJSON(w, http.StatusOK, MemorySearchResponse{
		Memories: searchResults,
		Total:    len(searchResults),
	})
}

// SyncRequest represents the request body for GET /memory/sync.
type SyncRequest struct {
	LastSync string `json:"last_sync"` // ISO 8601 timestamp
}

// SyncResponse represents the response for GET /memory/sync.
type SyncResponse struct {
	Changes []StoreResponse `json:"changes"`
}

// Sync handles GET /memory/sync - sync new/updated memories since last sync.
func (h *MemoryHandler) Sync(w http.ResponseWriter, r *http.Request) {
	// Parse last sync time
	var lastSync time.Time
	if ts := r.URL.Query().Get("last_sync"); ts != "" {
		// Try to parse as RFC3339
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			lastSync = t
		}
	}

	// Get all memories created after lastSync
	memories, err := h.store.ListMemoriesAfter(r.Context(), lastSync)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list memories after sync")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memories")
		return
	}

	var changes []StoreResponse
	for _, m := range memories {
		changes = append(changes, StoreResponse{
			MemoryID:  m.MemoryID,
			Type:      string(m.Type),
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}

	writeMemoryJSON(w, http.StatusOK, SyncResponse{Changes: changes})
}

// ListResponse wraps the memory list. The frontend uses this to power the
// MemoriesView; results are ordered most-recent-first.
type ListResponse struct {
	Memories []StoreResponse `json:"memories"`
	Total    int             `json:"total"`
}

// List handles GET /memory/list — returns every stored memory. Use the
// existing /memory/sync endpoint when you only want recent changes.
func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	memories, err := h.store.ListMemoriesAfter(r.Context(), time.Time{})
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list memories")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list memories")
		return
	}
	out := make([]StoreResponse, 0, len(memories))
	for _, m := range memories {
		out = append(out, StoreResponse{
			MemoryID:  m.MemoryID,
			Type:      string(m.Type),
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}
	writeMemoryJSON(w, http.StatusOK, ListResponse{Memories: out, Total: len(out)})
}

// Delete handles DELETE /memory/{memory_id}.
func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "memory_id")
	if memoryID == "" {
		writeMemoryError(w, http.StatusBadRequest, "BAD_REQUEST", "memory_id is required")
		return
	}
	if err := h.store.DeleteMemory(r.Context(), memoryID); err != nil {
		h.logger.Error().Err(err).Str("memory_id", memoryID).Msg("failed to delete memory")
		writeMemoryError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete memory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeMemoryJSON writes a JSON response with the given status code.
func writeMemoryJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeMemoryError writes a JSON error response.
func writeMemoryError(w http.ResponseWriter, status int, code, message string) {
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

// RouteMemoryEndpoints registers the memory API endpoints.
func RouteMemoryRoutes(router *chi.Mux, handler *MemoryHandler) {
	router.Post("/memory/store", handler.Store)
	router.Get("/memory/search", handler.MemorySearch)
	router.Get("/memory/sync", handler.Sync)
}
