package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/rs/zerolog"
)

// BriefHandler exposes on-demand triggers for the daily brief task.
type BriefHandler struct {
	brief  *scheduler.DailyBrief
	logger zerolog.Logger
}

// NewBriefHandler builds a brief handler. The brief argument may be nil
// (e.g. when daily_brief.enabled=false) — in that case the endpoint returns
// 503 so callers can fall back gracefully.
func NewBriefHandler(brief *scheduler.DailyBrief, logger zerolog.Logger) *BriefHandler {
	return &BriefHandler{
		brief:  brief,
		logger: logger.With().Str("handler", "brief").Logger(),
	}
}

// briefRunRequest is the JSON body accepted by POST /brief/run. All fields
// are optional. An empty body runs the default daily brief immediately.
type briefRunRequest struct {
	ProjectID     string `json:"project_id,omitempty"`
	LookbackHours int    `json:"lookback_hours,omitempty"`
}

// briefRunResponse acknowledges that a brief is in flight. The actual
// brief content is delivered asynchronously via the /events SSE stream.
type briefRunResponse struct {
	Status    string `json:"status"`               // "queued"
	ProjectID string `json:"project_id,omitempty"` // echo back when scoped
}

// RunNow handles POST /brief/run.
//
// The brief runs in a detached goroutine because LLM generation can take
// 10-30 seconds with reasoning models — we don't want the HTTP request to
// hold the connection that long. The caller learns the outcome by
// subscribing to /events and watching for a `brief` event.
func (h *BriefHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "BRIEF_DISABLED",
			"Daily brief is not configured. Set daily_brief.enabled=true in config.yaml.")
		return
	}

	var req briefRunRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBriefError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
	}

	opts := scheduler.RunOptions{
		ProjectID:     req.ProjectID,
		LookbackHours: req.LookbackHours,
	}

	go func(opts scheduler.RunOptions) {
		// Detach from the HTTP request context — that one ends as soon as
		// we respond. Use a fresh context with a generous ceiling so a
		// stuck LLM doesn't leak goroutines forever.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := h.brief.RunWith(ctx, opts); err != nil {
			h.logger.Warn().
				Err(err).
				Str("project_id", opts.ProjectID).
				Msg("on-demand brief failed")
		}
	}(opts)

	resp := briefRunResponse{
		Status:    "queued",
		ProjectID: req.ProjectID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeBriefError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
