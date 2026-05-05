package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/rs/zerolog"
)

// TimelineHandler serves POST /timeline/query.
type TimelineHandler struct {
	builder *retrieval.TimelineBuilder
	logger  zerolog.Logger
}

// NewTimelineHandler wires a builder. The builder is required — callers
// that don't want timelines should simply not call SetTimelineHandler.
func NewTimelineHandler(builder *retrieval.TimelineBuilder, logger zerolog.Logger) *TimelineHandler {
	return &TimelineHandler{
		builder: builder,
		logger:  logger.With().Str("handler", "timeline").Logger(),
	}
}

// TimelineFocusScopeDTO mirrors retrieval.FocusScope on the wire.
type TimelineFocusScopeDTO struct {
	ProjectIDs []string `json:"project_ids,omitempty"`
	TagIDs     []string `json:"tag_ids,omitempty"`
}

// TimelineQueryRequestDTO is the JSON body shape.
type TimelineQueryRequestDTO struct {
	Query      string                 `json:"query"`
	FocusScope *TimelineFocusScopeDTO `json:"focus_scope,omitempty"`
	RangeDays  int                    `json:"range_days,omitempty"`
	TopDocs    int                    `json:"top_docs,omitempty"`
}

// TimelineEventDTO is the wire shape of a single event.
type TimelineEventDTO struct {
	Date       string `json:"date"`
	ContentID  string `json:"content_id"`
	SourceType string `json:"source_type,omitempty"`
	Title      string `json:"title,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	Context    string `json:"context,omitempty"`
}

// TimelineChapterDTO is the wire shape of a chapter.
type TimelineChapterDTO struct {
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	TimeStart        string             `json:"time_start"`
	TimeEnd          string             `json:"time_end"`
	DominantEntities []string           `json:"dominant_entities"`
	EventCount       int                `json:"event_count"`
	Events           []TimelineEventDTO `json:"events"`
}

// TimelineResponseDTO is the full response envelope.
type TimelineResponseDTO struct {
	Chapters []TimelineChapterDTO `json:"chapters"`
	Query    string               `json:"query"`
	Total    int                  `json:"total_events"`
}

// Query handles POST /timeline/query.
func (h *TimelineHandler) Query(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.builder == nil {
		writeTimelineError(w, http.StatusServiceUnavailable, "timeline builder not configured")
		return
	}

	var req TimelineQueryRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeTimelineError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Query == "" {
		writeTimelineError(w, http.StatusBadRequest, "query is required")
		return
	}

	q := retrieval.TimelineQuery{
		Query:     req.Query,
		RangeDays: req.RangeDays,
		TopDocs:   req.TopDocs,
	}
	if req.FocusScope != nil && (len(req.FocusScope.ProjectIDs) > 0 || len(req.FocusScope.TagIDs) > 0) {
		q.FocusScope = &retrieval.FocusScope{
			ProjectIDs: req.FocusScope.ProjectIDs,
			TagIDs:     req.FocusScope.TagIDs,
		}
	}

	resp, err := h.builder.Build(r.Context(), q)
	if err != nil {
		h.logger.Error().Err(err).Str("query", req.Query).Msg("timeline build failed")
		writeTimelineError(w, http.StatusInternalServerError, "timeline build failed")
		return
	}

	dto := TimelineResponseDTO{
		Query: resp.Query,
		Total: resp.Total,
	}
	dto.Chapters = make([]TimelineChapterDTO, 0, len(resp.Chapters))
	for _, ch := range resp.Chapters {
		evts := make([]TimelineEventDTO, 0, len(ch.Events))
		for _, e := range ch.Events {
			evts = append(evts, TimelineEventDTO{
				Date:       e.DateString,
				ContentID:  e.ContentID,
				SourceType: e.SourceType,
				Title:      e.Title,
				Snippet:    e.Snippet,
				Context:    e.Context,
			})
		}
		dto.Chapters = append(dto.Chapters, TimelineChapterDTO{
			ID:               ch.ID,
			Title:            ch.Title,
			TimeStart:        ch.TimeStart.Format(time.RFC3339),
			TimeEnd:          ch.TimeEnd.Format(time.RFC3339),
			DominantEntities: ch.DominantEntities,
			EventCount:       ch.EventCount,
			Events:           evts,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(dto)
}

func writeTimelineError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message},
	})
}
