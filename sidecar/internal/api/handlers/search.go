// Package handlers provides HTTP handlers for the Hygur API.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/hygur/sidecar/internal/intent"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// SearchHandler handles the global search tool endpoint.
type SearchHandler struct {
	tool            *tools.SearchTool
	store           *store.DB
	unifiedSearcher *retrieval.UnifiedSearcher
	logger          zerolog.Logger
}

// NewSearchHandler creates a new SearchHandler.
func NewSearchHandler(tool *tools.SearchTool, store *store.DB, logger zerolog.Logger) *SearchHandler {
	return &SearchHandler{
		tool:   tool,
		store:  store,
		logger: logger.With().Str("handler", "search").Logger(),
	}
}

// NewSearchHandlerWithUnified creates a new SearchHandler with unified search capability.
func NewSearchHandlerWithUnified(tool *tools.SearchTool, store *store.DB, unifiedSearcher *retrieval.UnifiedSearcher, logger zerolog.Logger) *SearchHandler {
	return &SearchHandler{
		tool:            tool,
		store:           store,
		unifiedSearcher: unifiedSearcher,
		logger:          logger.With().Str("handler", "search").Logger(),
	}
}

// SetUnifiedSearcher sets the unified searcher for the handler.
func (h *SearchHandler) SetUnifiedSearcher(us *retrieval.UnifiedSearcher) {
	h.unifiedSearcher = us
}

// ToolSearchResponse represents the response for GET /tools/search.
type ToolSearchResponse struct {
	Results []ToolSearchResultDTO `json:"results"`
	Total   int                   `json:"total"`
}

// ToolSearchResultDTO represents a single search result from the tool.
type ToolSearchResultDTO struct {
	ChunkID   string  `json:"chunk_id"`
	ContentID string  `json:"content_id"`
	Score     float64 `json:"score"`
	Excerpt   string  `json:"excerpt"`
	Title     string  `json:"title"`
	Source    string  `json:"source"` // "fts", "vector", "both"
}

// Search handles GET /tools/search?q=...&top_k=...
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	// Parse query parameter
	query := r.URL.Query().Get("q")
	if query == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "q parameter is required")
		return
	}

	// Parse top_k parameter (default: 10)
	topK := 10
	if topKStr := r.URL.Query().Get("top_k"); topKStr != "" {
		parsed, err := strconv.Atoi(topKStr)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "top_k must be an integer")
			return
		}
		if parsed <= 0 {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "top_k must be positive")
			return
		}
		if parsed > 100 {
			parsed = 100 // Cap at 100
		}
		topK = parsed
	}

	// Check if tool is available
	if h.tool == nil {
		h.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "search tool not configured")
		return
	}

	// Perform global search
	results, err := h.tool.Run(r.Context(), query, topK)
	if err != nil {
		h.logger.Error().Err(err).Str("query", query).Msg("search failed")

		// Handle LLM client required error
		if err == retrieval.ErrLLMClientRequired {
			h.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "LLM client required for search")
			return
		}

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "search failed")
		return
	}

	// Convert results to DTOs and enrich with titles
	dtos := make([]ToolSearchResultDTO, 0, len(results))
	for _, res := range results {
		dto := ToolSearchResultDTO{
			ChunkID:   res.ChunkID,
			ContentID: res.ContentID,
			Score:     res.Score,
			Excerpt:   res.Excerpt,
			Source:    res.Source,
			Title:     "",
		}

		// Fetch title from knowledge item
		if h.store != nil && res.ContentID != "" {
			item, err := h.store.GetKnowledgeItem(r.Context(), res.ContentID)
			if err == nil && item != nil {
				dto.Title = item.Title
			}
		}

		dtos = append(dtos, dto)
	}

	resp := ToolSearchResponse{
		Results: dtos,
		Total:   len(dtos),
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// writeJSON writes a JSON response with the given status code.
func (h *SearchHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError writes a JSON error response.
func (h *SearchHandler) writeError(w http.ResponseWriter, status int, code, message string) {
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

// UnifiedSearchRequestDTO represents the JSON request body for POST /search.
type UnifiedSearchRequestDTO struct {
	Query    string             `json:"query"`
	TopK     int                `json:"top_k,omitempty"`
	Sources  []string           `json:"sources,omitempty"` // "knowledge", "mail", "all"
	Weights  map[string]float64 `json:"weights,omitempty"` // "knowledge": 0.6, "mail": 0.4
	DateFrom *string            `json:"date_from,omitempty"`
	DateTo   *string            `json:"date_to,omitempty"`
}

// UnifiedSearchResultDTO represents a single result in the unified search response.
type UnifiedSearchResultDTO struct {
	ChunkID     string         `json:"chunk_id"`
	ContentID   string         `json:"content_id"`
	SourceType  string         `json:"source_type"`
	Score       float64        `json:"score"`
	Excerpt     string         `json:"excerpt"`
	Title       string         `json:"title"`
	Date        string         `json:"date,omitempty"` // Document/email date (ISO8601)
	Metadata    map[string]any `json:"metadata,omitempty"`
	MailFrom    string         `json:"mail_from,omitempty"`
	MailDate    string         `json:"mail_date,omitempty"`
	MailSubject string         `json:"mail_subject,omitempty"`
}

// IntentDTO represents the detected intent in the response.
type IntentDTO struct {
	Query          string             `json:"query"`
	Sources        []string           `json:"sources"`
	Weights        map[string]float64 `json:"weights"`
	Confidence     float64            `json:"confidence"`
	TemporalMode   string             `json:"temporal_mode,omitempty"`
	TemporalWeight float64            `json:"temporal_weight,omitempty"`
}

// UnifiedSearchStatsDTO represents search statistics.
type UnifiedSearchStatsDTO struct {
	TotalResults     int   `json:"total_results"`
	KnowledgeResults int   `json:"knowledge_results"`
	MailResults      int   `json:"mail_results"`
	SearchDurationMs int64 `json:"search_duration_ms"`
}

// UnifiedSearchResponseDTO represents the JSON response for POST /search.
type UnifiedSearchResponseDTO struct {
	Results     []UnifiedSearchResultDTO `json:"results"`
	Intent      *IntentDTO               `json:"intent,omitempty"`
	SearchStats UnifiedSearchStatsDTO    `json:"search_stats"`
}

// UnifiedSearch handles POST /search for unified search across knowledge and mail.
func (h *SearchHandler) UnifiedSearch(w http.ResponseWriter, r *http.Request) {
	// Check if unified searcher is available
	if h.unifiedSearcher == nil {
		h.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "unified search not configured")
		return
	}

	// Parse request body
	var req UnifiedSearchRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body: "+err.Error())
		return
	}

	// Validate query
	if req.Query == "" {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "query is required")
		return
	}

	// Convert sources to intent.SourceType
	var sources []intent.SourceType
	for _, s := range req.Sources {
		switch s {
		case "knowledge":
			sources = append(sources, intent.SourceKnowledge)
		case "mail":
			sources = append(sources, intent.SourceMail)
		case "all":
			sources = append(sources, intent.SourceAll)
		default:
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid source: "+s)
			return
		}
	}

	// Convert weights to intent.SourceType keys
	var weights map[intent.SourceType]float64
	if len(req.Weights) > 0 {
		weights = make(map[intent.SourceType]float64)
		for k, v := range req.Weights {
			switch k {
			case "knowledge":
				weights[intent.SourceKnowledge] = v
			case "mail":
				weights[intent.SourceMail] = v
			default:
				h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid weight key: "+k)
				return
			}
		}
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

	// Build search request
	searchReq := retrieval.UnifiedSearchRequest{
		Query:    req.Query,
		TopK:     req.TopK,
		Sources:  sources,
		Weights:  weights,
		DateFrom: dateFrom,
		DateTo:   dateTo,
	}

	// Perform search
	result, err := h.unifiedSearcher.Search(r.Context(), searchReq)
	if err != nil {
		h.logger.Error().Err(err).Str("query", req.Query).Msg("unified search failed")

		// Handle specific errors
		if err == retrieval.ErrLLMClientRequired {
			h.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "LLM client required for search")
			return
		}

		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "search failed")
		return
	}

	// Convert results to DTOs
	dtos := make([]UnifiedSearchResultDTO, len(result.Results))
	for i, r := range result.Results {
		dtos[i] = UnifiedSearchResultDTO{
			ChunkID:     r.ChunkID,
			ContentID:   r.ContentID,
			SourceType:  r.SourceType,
			Score:       r.Score,
			Excerpt:     r.Excerpt,
			Title:       r.Title,
			Date:        r.Date,
			Metadata:    r.Metadata,
			MailFrom:    r.MailFrom,
			MailDate:    r.MailDate,
			MailSubject: r.MailSubject,
		}
	}

	// Convert intent to DTO if present
	var intentDTO *IntentDTO
	if result.Intent != nil {
		intentSources := make([]string, len(result.Intent.Sources))
		for i, s := range result.Intent.Sources {
			intentSources[i] = string(s)
		}
		intentWeights := make(map[string]float64)
		for k, v := range result.Intent.Weights {
			intentWeights[string(k)] = v
		}
		intentDTO = &IntentDTO{
			Query:          result.Intent.Query,
			Sources:        intentSources,
			Weights:        intentWeights,
			Confidence:     result.Intent.Confidence,
			TemporalMode:   string(result.Intent.TemporalMode),
			TemporalWeight: result.Intent.TemporalWeight,
		}
	}

	resp := UnifiedSearchResponseDTO{
		Results: dtos,
		Intent:  intentDTO,
		SearchStats: UnifiedSearchStatsDTO{
			TotalResults:     result.SearchStats.TotalResults,
			KnowledgeResults: result.SearchStats.KnowledgeResults,
			MailResults:      result.SearchStats.MailResults,
			SearchDurationMs: result.SearchStats.SearchDuration.Milliseconds(),
		},
	}

	h.writeJSON(w, http.StatusOK, resp)
}
