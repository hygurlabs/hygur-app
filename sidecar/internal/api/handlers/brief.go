package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// BriefHandler exposes on-demand triggers for the daily brief task plus the
// meeting-briefing endpoint and the unified briefings listing.
type BriefHandler struct {
	brief   *scheduler.DailyBrief
	meeting *scheduler.MeetingBriefer
	store   *store.DB
	logger  zerolog.Logger
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

// SetMeetingBriefer wires the meeting briefer used by POST /brief/meeting.
func (h *BriefHandler) SetMeetingBriefer(m *scheduler.MeetingBriefer) { h.meeting = m }

// SetStore wires the store used by GET /briefings to list stored briefs.
func (h *BriefHandler) SetStore(db *store.DB) { h.store = db }

// briefRunRequest is the JSON body accepted by POST /brief/run. All fields
// are optional. An empty body runs the default daily brief immediately.
type briefRunRequest struct {
	ProjectID     string `json:"project_id,omitempty"`
	LookbackHours int    `json:"lookback_hours,omitempty"`
	// On-demand contextual brief (WebUI "New briefing"): scope to these
	// projects/items and steer with free-text instructions.
	ProjectIDs   []string `json:"project_ids,omitempty"`
	ContentIDs   []string `json:"content_ids,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
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
		ProjectIDs:    req.ProjectIDs,
		ContentIDs:    req.ContentIDs,
		Instructions:  req.Instructions,
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

// meetingBriefRequest is the JSON body for POST /brief/meeting, sent by the
// macOS app ~30 min before a calendar event.
type meetingBriefRequest struct {
	EventID   string   `json:"event_id"`
	Title     string   `json:"title"`
	Attendees []string `json:"attendees,omitempty"`
	Notes     string   `json:"notes,omitempty"`
	Location  string   `json:"location,omitempty"`
	Start     string   `json:"start"` // RFC3339
}

// Meeting handles POST /brief/meeting — generate a briefing for one calendar
// event synchronously and return it. When the KB has no relevant context the
// response carries relevant=false and no notification is emitted.
func (h *BriefHandler) Meeting(w http.ResponseWriter, r *http.Request) {
	if h.meeting == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "MEETING_BRIEF_DISABLED", "Meeting briefing is not configured.")
		return
	}
	var req meetingBriefRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBriefError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if req.Title == "" {
		writeBriefError(w, http.StatusBadRequest, "VALIDATION_ERROR", "title is required")
		return
	}
	when := time.Now()
	if req.Start != "" {
		if t, err := time.Parse(time.RFC3339, req.Start); err == nil {
			when = t
		}
	}
	key := req.EventID
	if key == "" {
		key = req.Title
	}
	result, err := h.meeting.Generate(r.Context(), scheduler.MeetingInput{
		Kind:      "calendar",
		Key:       key,
		Title:     req.Title,
		Attendees: req.Attendees,
		Notes:     req.Notes,
		Location:  req.Location,
		When:      when,
	})
	if err != nil {
		h.logger.Warn().Err(err).Str("title", req.Title).Msg("meeting brief failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate briefing")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

// BriefingDTO is one entry in the unified briefings list (daily + meeting).
type BriefingDTO struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"` // "brief" | "meeting_brief"
	Content   string `json:"content"`
	When      string `json:"when,omitempty"`
	CreatedAt string `json:"created_at"`
}

// List handles GET /briefings — returns daily briefs and meeting briefings,
// most recent first.
func (h *BriefHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "briefings store not configured")
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var items []*store.KnowledgeItem
	for _, st := range []string{"brief", "meeting_brief"} {
		got, err := h.store.ListKnowledgeItemsBySourceType(r.Context(), st, limit, 0)
		if err != nil {
			h.logger.Warn().Err(err).Str("source_type", st).Msg("list briefings failed")
			continue
		}
		items = append(items, got...)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]BriefingDTO, 0, len(items))
	for _, it := range items {
		dto := BriefingDTO{
			ContentID: it.ContentID,
			Title:     it.Title,
			Kind:      it.SourceType,
			Content:   it.NormalizedText,
			CreatedAt: it.CreatedAt.Format(time.RFC3339),
		}
		if when, ok := it.Metadata["when"].(string); ok {
			dto.When = when
		}
		out = append(out, dto)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"briefings": out})
}

// CalendarSummary handles GET /agenda/calendar-summary — a short LLM synthesis of
// upcoming calendar events for the Calendar view header. Returns an empty summary
// (200) when nothing is upcoming or the brief task isn't configured, so the UI
// degrades gracefully.
func (h *BriefHandler) CalendarSummary(w http.ResponseWriter, r *http.Request) {
	var res scheduler.CalendarSummaryResult
	if h.brief != nil {
		if got, err := h.brief.CalendarSummary(r.Context()); err != nil {
			h.logger.Warn().Err(err).Msg("calendar summary failed")
		} else {
			res = got
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// FollowUp handles GET /knowledge/followup — a grounded LLM digest (salient
// topics + real, cited contradictions) for the Follow-up view. Returns an empty
// digest (200) when there's nothing factual to report or the brief task isn't
// configured, so the UI degrades gracefully.
func (h *BriefHandler) FollowUp(w http.ResponseWriter, r *http.Request) {
	res := scheduler.FollowUpDigest{}
	if h.brief != nil {
		if got, err := h.brief.FollowUp(r.Context(), r.URL.Query().Get("project_id")); err != nil {
			h.logger.Warn().Err(err).Msg("follow-up digest failed")
		} else {
			res = got
		}
	}
	if res.Topics == nil {
		res.Topics = []scheduler.DigestEntry{}
	}
	if res.Contradictions == nil {
		res.Contradictions = []scheduler.DigestEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// FollowUpReport handles GET /knowledge/followup/report — a short, grounded
// natural-language report streamed over SSE (`data: {"delta":"…"}` … then
// `data: {"done":true}`), so the UI can render it as it's written. Cached ~1h.
func (h *BriefHandler) FollowUpReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	flusher, ok := w.(http.Flusher)
	if !ok {
		fmt.Fprintf(w, "data: %s\n\n", `{"error":"streaming not supported"}`)
		return
	}

	if h.brief != nil {
		err := h.brief.StreamFollowUpReport(r.Context(), r.URL.Query().Get("project_id"), func(delta string) error {
			b, mErr := json.Marshal(map[string]string{"delta": delta})
			if mErr != nil {
				return mErr
			}
			if _, wErr := fmt.Fprintf(w, "data: %s\n\n", b); wErr != nil {
				return wErr
			}
			flusher.Flush()
			return nil
		})
		if err != nil && r.Context().Err() == nil {
			h.logger.Warn().Err(err).Msg("follow-up report stream failed")
		}
	}
	fmt.Fprintf(w, "data: %s\n\n", `{"done":true}`)
	flusher.Flush()
}

// AgendaEvents returns calendar events whose date falls in [from,to], ordered
// by date — so the Calendar view shows the actual upcoming events rather than
// the most-recently-synced batch (the generic item list is ordered by
// updated_at, which buries upcoming events behind a recent backfill of old
// ones). GET /agenda/events?from=&to=&limit= (RFC3339; defaults ±1 year).
func (h *BriefHandler) AgendaEvents(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "store not configured")
		return
	}
	now := time.Now()
	from, to := now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0)
	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = t
		}
	}
	limit := 500
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}

	items, err := h.store.ListEventsInWindow(r.Context(), from, to, limit)
	if err != nil {
		h.logger.Warn().Err(err).Msg("agenda events query failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list events")
		return
	}

	type evt struct {
		ContentID      string         `json:"content_id"`
		SourceType     string         `json:"source_type"`
		Title          string         `json:"title"`
		NormalizedText string         `json:"normalized_text"`
		Metadata       map[string]any `json:"metadata,omitempty"`
		Date           string         `json:"date,omitempty"`
	}
	out := make([]evt, 0, len(items))
	for _, it := range items {
		date := ""
		if cd := store.GetCanonicalDate(it); !cd.IsZero() {
			date = cd.UTC().Format(time.RFC3339)
		}
		out = append(out, evt{it.ContentID, it.SourceType, it.Title, it.NormalizedText, it.Metadata, date})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": out})
}

// DraftReply handles POST /knowledge/{content_id}/draft-reply — an on-demand,
// grounded reply draft for a mail item (not cached). Returns {"draft": "..."}.
func (h *BriefHandler) DraftReply(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil || h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	item, err := h.store.GetKnowledgeItem(r.Context(), contentIDParam(r))
	if err != nil || item == nil {
		writeBriefError(w, http.StatusNotFound, "NOT_FOUND", "item not found")
		return
	}
	draft, err := h.brief.DraftReply(r.Context(), item)
	if err != nil {
		h.logger.Warn().Err(err).Msg("draft reply failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to draft reply")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"draft": draft})
}

func writeBriefError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
