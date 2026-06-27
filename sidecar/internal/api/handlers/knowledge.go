// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// contentIDParam returns the {content_id} route param, percent-decoded. chi
// returns the raw (still-escaped) path segment, so a content_id containing '@'
// (e.g. a mail "imap:<msgid>@host") arrives as "...%40..." and would never
// match the stored id — the item read 404s and its tags/project panel stays
// blank. PathUnescape is a no-op for already-clean ids (notes, UUIDs).
func contentIDParam(r *http.Request) string {
	raw := chi.URLParam(r, "content_id")
	if dec, err := url.PathUnescape(raw); err == nil {
		return dec
	}
	return raw
}

// KnowledgeHandler handles knowledge-related API endpoints.
type KnowledgeHandler struct {
	store            *store.DB
	ingestor         *ingest.Ingestor
	searcher         *retrieval.HybridSearcher
	embeddingService *llm.EmbeddingService
	uploadDir        string
	logger           zerolog.Logger
}

// SetUploadDir sets the directory where files received via POST /knowledge/upload
// are saved before ingestion. When empty, a temp directory is used.
func (h *KnowledgeHandler) SetUploadDir(dir string) { h.uploadDir = dir }

// NewKnowledgeHandler creates a new KnowledgeHandler.
func NewKnowledgeHandler(store *store.DB, ingestor *ingest.Ingestor, searcher *retrieval.HybridSearcher, logger zerolog.Logger) *KnowledgeHandler {
	return &KnowledgeHandler{
		store:    store,
		ingestor: ingestor,
		searcher: searcher,
		logger:   logger.With().Str("handler", "knowledge").Logger(),
	}
}

// IngestRequest represents the request body for POST /knowledge/ingest.
type IngestRequest struct {
	Path      string   `json:"path"`       // Absolute path to the file
	ProjectID *string  `json:"project_id"` // Optional project association
	Tags      []string `json:"tags"`       // Optional tags
}

// IngestResponse represents the response for POST /knowledge/ingest.
type IngestResponse struct {
	ContentID  string `json:"content_id"`
	Status     string `json:"status"` // "indexed", "duplicate", "near_duplicate"
	ChunkCount int    `json:"chunk_count"`
	Title      string `json:"title"`
}

// IngestFolderRequest represents the request body for POST /knowledge/ingest-folder.
type IngestFolderRequest struct {
	Path           string   `json:"path"`            // Absolute path to the folder
	ProjectID      *string  `json:"project_id"`      // Optional project association
	Tags           []string `json:"tags"`            // Optional tags for all files
	MaxDepth       *int     `json:"max_depth"`       // Optional max recursion depth (nil = unlimited)
	Extensions     []string `json:"extensions"`      // Optional filter by extensions (e.g., [".md", ".txt"])
	IgnorePatterns []string `json:"ignore_patterns"` // Optional patterns to ignore (e.g., ["node_modules", ".git"])
}

// IngestFolderResponse represents the response for POST /knowledge/ingest-folder.
type IngestFolderResponse struct {
	Processed   int                  `json:"processed"`    // Number of files successfully ingested
	Skipped     int                  `json:"skipped"`      // Number of files skipped (unsupported type)
	Failed      int                  `json:"failed"`       // Number of files that failed
	TotalChunks int                  `json:"total_chunks"` // Total chunks created
	Results     []IngestFolderResult `json:"results"`      // Details per file
	Errors      []IngestFolderError  `json:"errors"`       // Error details
}

// IngestFolderResult represents a single file ingestion result.
type IngestFolderResult struct {
	Path       string `json:"path"`
	ContentID  string `json:"content_id"`
	Status     string `json:"status"`
	ChunkCount int    `json:"chunk_count"`
}

// IngestFolderError represents a file ingestion error.
type IngestFolderError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SearchRequest represents the request body for POST /knowledge/search.
type SearchRequest struct {
	Query     string   `json:"query"`
	ProjectID *string  `json:"project_id"` // Optional project filter
	TopK      int      `json:"top_k"`      // Default 10, max 100
	Mode      string   `json:"mode"`       // "" or "semantic" (default); "vector"/"hybrid" accepted as deprecated synonyms; "fts" is rejected.
	DateFrom  *string  `json:"date_from,omitempty"`
	DateTo    *string  `json:"date_to,omitempty"`
	Sources   []string `json:"sources,omitempty"`
}

// SearchResponse represents the response for POST /knowledge/search.
type SearchResponse struct {
	Results []SearchResultDTO `json:"results"`
	Total   int               `json:"total"`
}

// SearchResultDTO represents a single search result.
type SearchResultDTO struct {
	ChunkID    string  `json:"chunk_id"`
	ContentID  string  `json:"content_id"`
	Score      float64 `json:"score"`
	Excerpt    string  `json:"excerpt"`
	Title      string  `json:"title"`          // From knowledge_item
	Source     string  `json:"source"`         // Always "vector"
	SourceType string  `json:"source_type"`    // "knowledge" (legacy field)
	Date       string  `json:"date,omitempty"` // Document/email date (ISO8601)
}

// KnowledgeItemResponse represents the response for GET /knowledge/{content_id}.
type KnowledgeItemResponse struct {
	ContentID      string         `json:"content_id"`
	SourceType     string         `json:"source_type"`
	SourcePath     *string        `json:"source_path"`
	Title          string         `json:"title"`
	NormalizedText string         `json:"normalized_text"`
	Metadata       map[string]any `json:"metadata"`
	ChunkCount     int            `json:"chunk_count"`
	Tags           []TagSummary   `json:"tags"`
	ProjectID      *string        `json:"project_id"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	Date           string         `json:"date,omitempty"` // Document/email date from metadata
}

// TagSummary represents a minimal tag info for embedding in responses.
type TagSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Ingest handles POST /knowledge/ingest.
func (h *KnowledgeHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate path is not empty
	if req.Path == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path is required")
		return
	}

	// Validate path is absolute
	if !filepath.IsAbs(req.Path) {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path must be absolute")
		return
	}

	// Check if file exists
	info, err := os.Stat(req.Path)
	if os.IsNotExist(err) {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}
	if err != nil {
		h.logger.Error().Err(err).Str("path", req.Path).Msg("failed to stat file")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to access file")
		return
	}

	// Check if it's a directory
	if info.IsDir() {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path is a directory, not a file")
		return
	}

	// Check if ingestor is available
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}

	// Perform ingestion
	opts := ingest.IngestOptions{
		ProjectID: req.ProjectID,
		Tags:      req.Tags,
	}

	result, err := h.ingestor.Ingest(r.Context(), req.Path, opts)
	if err != nil {
		h.logger.Error().Err(err).Str("path", req.Path).Msg("ingestion failed")

		// Handle specific errors
		switch {
		case os.IsNotExist(err):
			writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		case err == ingest.ErrNotAbsolutePath:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path must be absolute")
		case err == ingest.ErrPathTraversal:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid path")
		case err == ingest.ErrSymlinkNotAllowed:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "symlinks are not allowed")
		case err == ingest.ErrFileTooLarge:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file exceeds maximum size")
		case err == ingest.ErrNoParser:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "unsupported file type")
		case err == ingest.ErrEmptyContent:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file content is empty")
		default:
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "ingestion failed")
		}
		return
	}

	// Generate title from filename
	title := filepath.Base(req.Path)

	resp := IngestResponse{
		ContentID:  result.ContentID,
		Status:     result.Status,
		ChunkCount: result.ChunkCount,
		Title:      title,
	}

	writeKnowledgeJSON(w, http.StatusCreated, resp)
}

// Upload handles POST /knowledge/upload — accepts a multipart file, saves it to
// the uploads directory, and ingests it into the knowledge base. Drives the
// WebUI composer's paperclip: an uploaded document becomes searchable and is
// referenced as an attachment in the question.
func (h *KnowledgeHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file is required")
		return
	}
	defer file.Close()

	dir := h.uploadDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "hygur-uploads")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to prepare upload dir")
		return
	}
	name := filepath.Base(header.Filename)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "upload"
	}
	dst := filepath.Join(dir, strconv.FormatInt(time.Now().UnixNano(), 10)+"-"+name)
	out, err := os.Create(dst)
	if err != nil {
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save upload")
		return
	}
	if _, err := out.ReadFrom(file); err != nil {
		_ = out.Close()
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write upload")
		return
	}
	_ = out.Close()

	var projectID *string
	if pid := r.FormValue("project_id"); pid != "" {
		projectID = &pid
	}
	var tags []string
	if t := r.FormValue("tags"); t != "" {
		tags = strings.Split(t, ",")
	}

	result, err := h.ingestor.Ingest(r.Context(), dst, ingest.IngestOptions{ProjectID: projectID, Tags: tags})
	if err != nil {
		h.logger.Error().Err(err).Str("file", name).Msg("upload ingestion failed")
		switch {
		case err == ingest.ErrNoParser:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "unsupported file type")
		case err == ingest.ErrFileTooLarge:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file exceeds maximum size")
		case err == ingest.ErrEmptyContent:
			writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file content is empty")
		default:
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "ingestion failed")
		}
		return
	}

	writeKnowledgeJSON(w, http.StatusCreated, IngestResponse{
		ContentID:  result.ContentID,
		Status:     result.Status,
		ChunkCount: result.ChunkCount,
		Title:      name,
	})
}

// IngestFolder handles POST /knowledge/ingest-folder.
func (h *KnowledgeHandler) IngestFolder(w http.ResponseWriter, r *http.Request) {
	var req IngestFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate path is not empty
	if req.Path == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path is required")
		return
	}

	// Validate path is absolute
	if !filepath.IsAbs(req.Path) {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path must be absolute")
		return
	}

	// Check if path exists and is a directory
	info, err := os.Stat(req.Path)
	if os.IsNotExist(err) {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "folder not found")
		return
	}
	if err != nil {
		h.logger.Error().Err(err).Str("path", req.Path).Msg("failed to stat folder")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to access folder")
		return
	}
	if !info.IsDir() {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "path is not a directory")
		return
	}

	// Check if ingestor is available
	if h.ingestor == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "ingestor not configured")
		return
	}

	// Build set of allowed extensions (lowercase, with leading dot)
	allowedExts := make(map[string]bool)
	if len(req.Extensions) > 0 {
		for _, ext := range req.Extensions {
			ext = strings.ToLower(ext)
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			allowedExts[ext] = true
		}
	} else {
		// Default: all supported extensions
		allowedExts[".md"] = true
		allowedExts[".txt"] = true
		allowedExts[".pdf"] = true
		allowedExts[".docx"] = true
	}

	// Build set of ignore patterns (lowercase)
	ignorePatterns := make(map[string]bool)
	defaultIgnore := []string{".git", ".svn", "node_modules", "__pycache__", ".DS_Store", "Thumbs.db"}
	for _, p := range defaultIgnore {
		ignorePatterns[strings.ToLower(p)] = true
	}
	for _, p := range req.IgnorePatterns {
		ignorePatterns[strings.ToLower(p)] = true
	}

	// Collect files to process
	var filesToProcess []string
	maxDepth := -1 // unlimited
	if req.MaxDepth != nil {
		maxDepth = *req.MaxDepth
	}
	baseDepth := strings.Count(req.Path, string(filepath.Separator))

	err = filepath.WalkDir(req.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Check depth limit
		if maxDepth >= 0 {
			currentDepth := strings.Count(path, string(filepath.Separator)) - baseDepth
			if currentDepth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Check ignore patterns
		name := strings.ToLower(d.Name())
		if ignorePatterns[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Check extension
		ext := strings.ToLower(filepath.Ext(path))
		if !allowedExts[ext] {
			return nil
		}

		filesToProcess = append(filesToProcess, path)
		return nil
	})

	if err != nil {
		h.logger.Error().Err(err).Str("path", req.Path).Msg("failed to walk directory")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to scan folder")
		return
	}

	// Process each file
	opts := ingest.IngestOptions{
		ProjectID: req.ProjectID,
		Tags:      req.Tags,
	}

	resp := IngestFolderResponse{
		Results: make([]IngestFolderResult, 0),
		Errors:  make([]IngestFolderError, 0),
	}

	for _, filePath := range filesToProcess {
		result, err := h.ingestor.Ingest(r.Context(), filePath, opts)
		if err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, IngestFolderError{
				Path:    filePath,
				Message: err.Error(),
			})
			h.logger.Debug().Err(err).Str("path", filePath).Msg("file ingestion failed")
			continue
		}

		resp.Processed++
		resp.TotalChunks += result.ChunkCount
		resp.Results = append(resp.Results, IngestFolderResult{
			Path:       filePath,
			ContentID:  result.ContentID,
			Status:     result.Status,
			ChunkCount: result.ChunkCount,
		})
	}

	// Calculate skipped (files that weren't in our extension list were already filtered out)
	resp.Skipped = 0 // We pre-filter, so skipped stays 0

	h.logger.Info().
		Int("processed", resp.Processed).
		Int("failed", resp.Failed).
		Int("total_chunks", resp.TotalChunks).
		Str("path", req.Path).
		Msg("folder ingestion complete")

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// Search handles POST /knowledge/search.
func (h *KnowledgeHandler) Search(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// Validate query is not empty
	if req.Query == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "query is required")
		return
	}

	// Apply defaults and bounds for topK
	topK := req.TopK
	if topK <= 0 {
		topK = 10 // Default
	}
	if topK > 100 {
		topK = 100 // Max
	}

	// Validate mode: "fts" is no longer supported; "hybrid" and "vector" are aliases for semantic.
	switch req.Mode {
	case "fts":
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "mode 'fts' is no longer supported; use '' or 'semantic'")
		return
	case "", "semantic", "vector", "hybrid":
		// All non-fts modes are treated as semantic search.
	default:
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "mode must be 'semantic' (or omitted)")
		return
	}

	// Check if searcher is available
	if h.searcher == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "searcher not configured")
		return
	}

	// Parse date range filters
	var dateFrom, dateTo *time.Time
	if req.DateFrom != nil && *req.DateFrom != "" {
		if t, err := time.Parse("2006-01-02", *req.DateFrom); err == nil {
			dateFrom = &t
		}
	}
	if req.DateTo != nil && *req.DateTo != "" {
		if t, err := time.Parse("2006-01-02", *req.DateTo); err == nil {
			dateTo = &t
		}
	}

	// Perform search
	opts := retrieval.SearchOptions{
		TopK:      topK,
		ProjectID: req.ProjectID,
	}

	results, err := h.searcher.Search(r.Context(), req.Query, opts)
	if err != nil {
		h.logger.Error().Err(err).Str("query", req.Query).Msg("search failed")

		// Handle LLM client required error
		if err == retrieval.ErrLLMClientRequired {
			writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "LLM client required for vector search")
			return
		}

		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "search failed")
		return
	}

	// Convert results to DTOs and enrich with titles
	dtos := make([]SearchResultDTO, 0, len(results))
	for _, res := range results {
		dto := SearchResultDTO{
			ChunkID:    res.ChunkID,
			ContentID:  res.ContentID,
			Score:      res.Score,
			Excerpt:    res.Excerpt,
			Source:     res.Source,
			Title:      "", // Will be enriched below
			SourceType: "knowledge",
		}

		// Fetch title and date from knowledge item
		if h.store != nil && res.ContentID != "" {
			item, err := h.store.GetKnowledgeItem(r.Context(), res.ContentID)
			if err == nil && item != nil {
				dto.Title = item.Title
				// Include document date if available
				if !item.CreatedAt.IsZero() {
					dto.Date = item.CreatedAt.UTC().Format(time.RFC3339)
				} else if !item.UpdatedAt.IsZero() {
					dto.Date = item.UpdatedAt.UTC().Format(time.RFC3339)
				}
			}
		}

		dtos = append(dtos, dto)
	}

	// Apply date range filter
	if dateFrom != nil || dateTo != nil {
		filtered := make([]SearchResultDTO, 0, len(dtos))
		for _, dto := range dtos {
			if !shouldIncludeByDate(dto.Date, dateFrom, dateTo) {
				continue
			}
			filtered = append(filtered, dto)
		}
		dtos = filtered
	}

	resp := SearchResponse{
		Results: dtos,
		Total:   len(dtos),
	}

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// Get handles GET /knowledge/{content_id}.
func (h *KnowledgeHandler) Get(w http.ResponseWriter, r *http.Request) {
	contentID := contentIDParam(r)
	if contentID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Retrieve knowledge item
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to retrieve item")
		return
	}

	if item == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	// Count chunks
	chunks, err := h.store.GetChunksByContentID(r.Context(), contentID)
	chunkCount := 0
	if err == nil {
		chunkCount = len(chunks)
	}

	// Fetch tags for this item
	tags, err := h.store.GetTagsForItem(r.Context(), contentID)
	tagSummaries := make([]TagSummary, 0, len(tags))
	if err == nil {
		for _, t := range tags {
			tagSummaries = append(tagSummaries, TagSummary{
				ID:    t.ID,
				Name:  t.Name,
				Color: t.Color,
			})
		}
	}

	// Fetch project ID for this item
	projectID, _ := h.store.GetProjectIDForItem(r.Context(), contentID)

	resp := KnowledgeItemResponse{
		ContentID:      item.ContentID,
		SourceType:     item.SourceType,
		SourcePath:     item.SourcePath,
		Title:          item.Title,
		NormalizedText: item.NormalizedText,
		Metadata:       item.Metadata,
		ChunkCount:     chunkCount,
		Tags:           tagSummaries,
		ProjectID:      projectID,
		CreatedAt:      item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      item.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// KnowledgeListResponse represents the response for GET /knowledge/items.
type KnowledgeListResponse struct {
	Items  []KnowledgeItemResponse `json:"items"`
	Total  int                     `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

// List handles GET /knowledge/items.
// Query params: limit (default 50, max 200), offset (default 0), q (title filter).
func (h *KnowledgeHandler) List(w http.ResponseWriter, r *http.Request) {
	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Parse pagination params
	limit := 50
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	titleFilter := r.URL.Query().Get("q")
	sourceTypeFilter := r.URL.Query().Get("source_type")
	// Comma-separated source types to hide (e.g. "event" — the Library browse omits
	// calendar events, which have their own view). Ignored when source_type is set.
	var excludeSourceTypes []string
	if ex := strings.TrimSpace(r.URL.Query().Get("exclude_source_type")); ex != "" && sourceTypeFilter == "" {
		for _, s := range strings.Split(ex, ",") {
			if s = strings.TrimSpace(s); s != "" {
				excludeSourceTypes = append(excludeSourceTypes, s)
			}
		}
	}

	// Title-filter path: bypass pagination and return matching items directly.
	if titleFilter != "" {
		items, err := h.store.SearchKnowledgeItemsByTitle(r.Context(), titleFilter, limit)
		if err != nil {
			h.logger.Error().Err(err).Str("q", titleFilter).Msg("title filter search failed")
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "title search failed")
			return
		}
		dtos := make([]KnowledgeItemResponse, 0, len(items))
		for _, item := range items {
			chunks, _ := h.store.GetChunksByContentID(r.Context(), item.ContentID)
			rawTags, _ := h.store.GetTagsForItem(r.Context(), item.ContentID)
			tagSummaries := make([]TagSummary, 0, len(rawTags))
			for _, t := range rawTags {
				tagSummaries = append(tagSummaries, TagSummary{ID: t.ID, Name: t.Name, Color: t.Color})
			}
			dto := KnowledgeItemResponse{
				ContentID:  item.ContentID,
				SourceType: item.SourceType,
				Title:      item.Title,
				Metadata:   item.Metadata,
				CreatedAt:  item.CreatedAt.Format(time.RFC3339),
				UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
				ChunkCount: len(chunks),
				Tags:       tagSummaries,
			}
			dtos = append(dtos, dto)
		}
		writeKnowledgeJSON(w, http.StatusOK, KnowledgeListResponse{Items: dtos, Total: len(dtos), Limit: limit, Offset: 0})
		return
	}

	// Get total count for pagination metadata (scoped to the source_type filter
	// when one is supplied).
	var (
		total int
		err   error
	)
	switch {
	case sourceTypeFilter != "":
		total, err = h.store.CountKnowledgeItemsBySourceTypes(r.Context(), []string{sourceTypeFilter})
	case len(excludeSourceTypes) > 0:
		// total(all) − total(excluded) reuses the existing counters.
		var all, ex int
		if all, err = h.store.CountKnowledgeItems(r.Context()); err == nil {
			ex, err = h.store.CountKnowledgeItemsBySourceTypes(r.Context(), excludeSourceTypes)
		}
		total = all - ex
	default:
		total, err = h.store.CountKnowledgeItems(r.Context())
	}
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to count knowledge items")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count items")
		return
	}

	// Retrieve knowledge items (optionally filtered/excluded by source_type).
	var items []*store.KnowledgeItem
	switch {
	case sourceTypeFilter != "":
		items, err = h.store.ListKnowledgeItemsBySourceType(r.Context(), sourceTypeFilter, limit, offset)
	case len(excludeSourceTypes) > 0:
		items, err = h.store.ListKnowledgeItemsExcluding(r.Context(), excludeSourceTypes, limit, offset)
	default:
		items, err = h.store.ListKnowledgeItems(r.Context(), limit, offset)
	}
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to list knowledge items")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list items")
		return
	}

	// Convert to response DTOs
	dtos := make([]KnowledgeItemResponse, 0, len(items))
	for _, item := range items {
		// Count chunks for this item
		chunks, err := h.store.GetChunksByContentID(r.Context(), item.ContentID)
		chunkCount := 0
		if err == nil {
			chunkCount = len(chunks)
		}

		// Fetch tags for this item
		tags, err := h.store.GetTagsForItem(r.Context(), item.ContentID)
		tagSummaries := make([]TagSummary, 0, len(tags))
		if err == nil {
			for _, t := range tags {
				tagSummaries = append(tagSummaries, TagSummary{
					ID:    t.ID,
					Name:  t.Name,
					Color: t.Color,
				})
			}
		}

		// Fetch project ID for this item
		projectID, _ := h.store.GetProjectIDForItem(r.Context(), item.ContentID)

		// Extract date from metadata (mail_date for emails, file_date for files)
		var dateStr string
		if item.Metadata != nil {
			h.logger.Debug().Str("content_id", item.ContentID).Interface("metadata", item.Metadata).Msg("DEBUG: checking metadata")
			if md, ok := item.Metadata["mail_date"].(string); ok && md != "" {
				h.logger.Debug().Str("mail_date", md).Msg("DEBUG: found mail_date")
				dateStr = md
			} else if md, ok := item.Metadata["file_date"].(string); ok && md != "" {
				h.logger.Debug().Str("file_date", md).Msg("DEBUG: found file_date")
				dateStr = md
			} else if md, ok := item.Metadata["canonical_date"].(string); ok && md != "" {
				dateStr = md
			} else if md, ok := item.Metadata["start"].(string); ok && md != "" {
				dateStr = md
			} else {
				h.logger.Debug().Msg("DEBUG: no date found in metadata, checking other keys")
				for k, v := range item.Metadata {
					h.logger.Debug().Str("key", k).Interface("value", v).Msg("DEBUG: metadata key")
				}
			}
		}

		dto := KnowledgeItemResponse{
			ContentID:      item.ContentID,
			SourceType:     item.SourceType,
			SourcePath:     item.SourcePath,
			Title:          item.Title,
			NormalizedText: item.NormalizedText,
			Metadata:       item.Metadata,
			ChunkCount:     chunkCount,
			Tags:           tagSummaries,
			ProjectID:      projectID,
			CreatedAt:      item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:      item.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Date:           dateStr,
		}
		h.logger.Info().
			Str("content_id", item.ContentID).
			Str("date", dateStr).
			Msg("DEBUG: sending date to client")
		dtos = append(dtos, dto)
	}

	resp := KnowledgeListResponse{
		Items:  dtos,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}

	// DEBUG: Log response
	h.logger.Info().
		Int("items_count", len(resp.Items)).
		Int("total", resp.Total).
		Msg("DEBUG: sending response")

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /knowledge/{content_id}.
func (h *KnowledgeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	contentID := contentIDParam(r)
	if contentID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Check if item exists before deleting
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to check knowledge item")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check item")
		return
	}

	if item == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	// Delete the item (cascade deletes chunks)
	err = h.store.DeleteKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to delete knowledge item")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete item")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Reset handles DELETE /knowledge/reset.
func (h *KnowledgeHandler) Reset(w http.ResponseWriter, r *http.Request) {
	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Perform reset
	err := h.store.ResetKnowledge(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to reset knowledge base")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to reset knowledge base")
		return
	}

	h.logger.Info().Msg("knowledge base reset successfully")

	resp := map[string]string{
		"status":  "ok",
		"message": "Knowledge base has been reset",
	}
	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// DiagnosticResponse represents the response for GET /knowledge/diagnostic.
type DiagnosticResponse struct {
	TotalItems          int            `json:"total_items"`
	ItemsWithChunks     int            `json:"items_with_chunks"`
	ItemsWithEmbeddings int            `json:"items_with_embeddings"`
	TotalChunks         int            `json:"total_chunks"`
	TotalChunkVectors   int            `json:"total_chunk_vectors"`
	OrphanChunks        int            `json:"orphan_chunks"`
	SourceTypeCounts    map[string]int `json:"source_type_counts"`
	MissingChunks       []string       `json:"missing_chunks"`
	MissingEmbeddings   []string       `json:"missing_embeddings"`
}

// Diagnostic handles GET /knowledge/diagnostic.
// It returns statistics about the knowledge base health.
func (h *KnowledgeHandler) Diagnostic(w http.ResponseWriter, r *http.Request) {
	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	stats, err := h.store.GetDiagnosticStats(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to get diagnostic stats")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get diagnostic stats")
		return
	}

	resp := DiagnosticResponse{
		TotalItems:          stats.TotalItems,
		ItemsWithChunks:     stats.ItemsWithChunks,
		ItemsWithEmbeddings: stats.ItemsWithEmbeddings,
		TotalChunks:         stats.TotalChunks,
		TotalChunkVectors:   stats.TotalChunkVectors,
		OrphanChunks:        stats.OrphanChunks,
		SourceTypeCounts:    stats.SourceTypeCounts,
		MissingChunks:       stats.MissingChunks,
		MissingEmbeddings:   stats.MissingEmbeddings,
	}

	h.logger.Info().
		Int("total_items", resp.TotalItems).
		Int("items_with_chunks", resp.ItemsWithChunks).
		Int("items_with_embeddings", resp.ItemsWithEmbeddings).
		Int("total_chunks", resp.TotalChunks).
		Int("total_chunk_vectors", resp.TotalChunkVectors).
		Int("orphan_chunks", resp.OrphanChunks).
		Int("missing_chunks", len(resp.MissingChunks)).
		Int("missing_embeddings", len(resp.MissingEmbeddings)).
		Msg("diagnostic completed")

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// ReindexRequest represents the request body for POST /knowledge/reindex.
type ReindexRequest struct {
	ContentID *string `json:"content_id"` // Optional: if not provided, reindexes all items missing chunks
}

// ReindexResponse represents the response for POST /knowledge/reindex.
type ReindexResponse struct {
	Reindexed         int      `json:"reindexed"`          // Number of items reindexed
	ChunksCreated     int      `json:"chunks_created"`     // Total chunks created
	EmbeddingsCreated int      `json:"embeddings_created"` // Total embeddings created
	Errors            []string `json:"errors,omitempty"`   // Any errors encountered
}

// writeKnowledgeJSON writes a JSON response with the given status code.
func writeKnowledgeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeKnowledgeError writes a JSON error response.
func writeKnowledgeError(w http.ResponseWriter, status int, code, message string) {
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

// LinkProjectRequest represents the request body for POST /knowledge/{content_id}/project.
type LinkProjectRequest struct {
	ProjectID string `json:"project_id"`
}

// LinkProjectResponse represents the response for POST /knowledge/{content_id}/project.
type LinkProjectResponse struct {
	ContentID string `json:"content_id"`
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
}

// LinkProject handles POST /knowledge/{content_id}/project.
// It links a knowledge item to a project.
func (h *KnowledgeHandler) LinkProject(w http.ResponseWriter, r *http.Request) {
	contentID := contentIDParam(r)
	if contentID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Check if item exists
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get item")
		return
	}
	if item == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	var req LinkProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug().Err(err).Msg("failed to parse request body")
		writeKnowledgeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if req.ProjectID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "project_id is required")
		return
	}

	// Check if project exists
	project, err := h.store.GetProject(r.Context(), req.ProjectID)
	if err != nil {
		h.logger.Error().Err(err).Str("project_id", req.ProjectID).Msg("failed to get project")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get project")
		return
	}
	if project == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "project not found")
		return
	}

	// Link item to project
	if err := h.store.LinkToProject(r.Context(), contentID, req.ProjectID); err != nil {
		h.logger.Error().Err(err).
			Str("content_id", contentID).
			Str("project_id", req.ProjectID).
			Msg("failed to link item to project")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to link to project")
		return
	}

	resp := LinkProjectResponse{
		ContentID: contentID,
		ProjectID: req.ProjectID,
		Status:    "linked",
	}

	writeKnowledgeJSON(w, http.StatusOK, resp)
}

// UnlinkProject handles DELETE /knowledge/{content_id}/project.
// It removes the link between a knowledge item and its project.
func (h *KnowledgeHandler) UnlinkProject(w http.ResponseWriter, r *http.Request) {
	contentID := contentIDParam(r)
	if contentID == "" {
		writeKnowledgeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "content_id is required")
		return
	}

	// Check if store is available
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}

	// Check if item exists
	item, err := h.store.GetKnowledgeItem(r.Context(), contentID)
	if err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to get knowledge item")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get item")
		return
	}
	if item == nil {
		writeKnowledgeError(w, http.StatusNotFound, "NOT_FOUND", "knowledge item not found")
		return
	}

	// Unlink item from project
	if err := h.store.UnlinkFromProject(r.Context(), contentID); err != nil {
		h.logger.Error().Err(err).Str("content_id", contentID).Msg("failed to unlink item from project")
		writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to unlink from project")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReembedMissing generates embeddings for all chunks that currently lack them.
// POST /knowledge/reembed-missing
// Idempotent: safe to call multiple times.
func (h *KnowledgeHandler) ReembedMissing(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "store not configured")
		return
	}
	if h.embeddingService == nil {
		writeKnowledgeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "embedding service not configured")
		return
	}

	const batchSize = 50
	reembedded := 0
	failed := 0
	var errs []string

	for {
		orphans, err := h.store.ListOrphanChunks(r.Context(), batchSize)
		if err != nil {
			h.logger.Error().Err(err).Msg("ReembedMissing: failed to list orphan chunks")
			writeKnowledgeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		if len(orphans) == 0 {
			break
		}

		if err := h.embeddingService.BatchEmbedAndStore(r.Context(), orphans); err != nil {
			h.logger.Error().Err(err).Int("batch_size", len(orphans)).Msg("ReembedMissing: batch embedding failed")
			failed += len(orphans)
			errs = append(errs, err.Error())
			// Stop on embedding failure (e.g., LLM down) rather than looping forever.
			break
		}
		reembedded += len(orphans)

		if len(orphans) < batchSize {
			break
		}
	}

	writeKnowledgeJSON(w, http.StatusOK, map[string]any{
		"reembedded": reembedded,
		"failed":     failed,
		"errors":     errs,
	})
}

// shouldIncludeByDate checks if a result should be included based on date filters.
func shouldIncludeByDate(dateStr string, dateFrom, dateTo *time.Time) bool {
	if dateStr == "" {
		if dateFrom == nil && dateTo == nil {
			return true
		}
		return false
	}

	parser := time.RFC3339
	t, err := time.Parse(parser, dateStr)
	if err != nil {
		return false
	}

	if dateFrom != nil && t.Before(*dateFrom) {
		return false
	}
	if dateTo != nil && t.After(*dateTo) {
		return false
	}
	return true
}
