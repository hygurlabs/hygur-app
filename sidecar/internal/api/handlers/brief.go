package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// BriefHandler exposes on-demand triggers for the daily brief task plus the
// unified briefings listing.
type BriefHandler struct {
	brief  *scheduler.DailyBrief
	store  *store.DB
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

// BriefingDTO is one entry in the unified briefings list (daily + meeting).
type BriefingDTO struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`               // "brief" | "meeting_brief"
	SubKind   string `json:"sub_kind,omitempty"` // meeting_brief origin: "mail" (deadline) | "calendar"
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
			Content:   it.DisplayText(),
			CreatedAt: it.CreatedAt.Format(time.RFC3339),
		}
		if when, ok := it.Metadata["when"].(string); ok {
			dto.When = when
		}
		// A meeting_brief is either a calendar meeting or a mail deadline; the badge
		// must reflect which (a deadline is not a "meeting"). Carried in metadata.kind.
		if k, ok := it.Metadata["kind"].(string); ok {
			dto.SubKind = k
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
		out = append(out, evt{it.ContentID, it.SourceType, it.Title, it.DisplayText(), it.Metadata, date})
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

// Claims handles GET /knowledge/{content_id}/claims — W6 stage-1 preview: runs
// LLM claim extraction (verbatim-quote gated) on the item and returns the claims,
// so claim quality can be eyeballed on real data before the cached backfill.
func (h *BriefHandler) Claims(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil || h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	item, err := h.store.GetKnowledgeItem(r.Context(), contentIDParam(r))
	if err != nil || item == nil {
		writeBriefError(w, http.StatusNotFound, "NOT_FOUND", "item not found")
		return
	}
	claims, err := h.brief.ExtractClaims(r.Context(), item)
	if err != nil {
		h.logger.Warn().Err(err).Msg("claim extraction failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to extract claims")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"claims": claims, "count": len(claims)})
}

// ClaimContradictions handles GET /knowledge/claim-contradictions?project_id= —
// the W6 REDUCE surface: cross-source claim divergences reconciled by the LLM into
// conflict / supersedes, each cited. Cached ~1h per scope.
func (h *BriefHandler) ClaimContradictions(w http.ResponseWriter, r *http.Request) {
	if h.brief == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	conflicts, scanned, err := h.brief.SemanticContradictions(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		h.logger.Warn().Err(err).Msg("semantic contradictions failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to reconcile contradictions")
		return
	}
	// Apply the user's dismissals fresh each request (not baked into the ~1h
	// reconciliation cache, so a dismiss takes effect immediately). Default hides
	// dismissed ones; ?include_dismissed=1 returns them flagged (the manage view).
	includeDismissed := r.URL.Query().Get("include_dismissed") == "1"
	var dismissed map[string]bool
	if h.store != nil {
		if d, derr := h.store.DismissedContradictions(r.Context()); derr == nil {
			dismissed = d
		}
	}
	out := make([]contradict.ReconciledConflict, 0, len(conflicts))
	for _, c := range conflicts {
		isDismissed := dismissed[c.Key]
		if isDismissed && !includeDismissed {
			continue
		}
		c.Dismissed = isDismissed
		out = append(out, c)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"contradictions": out, "scanned": scanned})
}

// Digest handles GET /digest — the daily "state of your world" surface
// (Direction C). It ASSEMBLES already-computed signals into one proactive view:
// where things stand (the life synopsis), open contradictions, decisions awaiting
// confirmation, and tasks due soon. Cheap reads + the durable contradiction cache
// — no mega-prompt; this is composition, not generation.
func (h *BriefHandler) Digest(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	start := time.Now()
	ctx := r.Context()
	const (
		maxContradictions = 5
		maxDecisions      = 8
		maxTasks          = 10
	)

	// This is composition of PRECOMPUTED state, never on-request generation: every
	// step below is a cheap read (or a cache serve) and makes ZERO LLM calls. The
	// steps are independent, so run them concurrently — WP19's WAL makes concurrent
	// SQLite reads safe. Each degrades on its own, so a step error is logged inside
	// the goroutine, not propagated (no sibling cancellation).
	var (
		synopsis       string
		positions      string
		contradictions = make([]contradict.ReconciledConflict, 0, maxContradictions)
		proposed       = []*store.Decision{}
		dueTasks       = []*store.Task{}
		upcoming       []scheduler.Upcoming
	)
	var g errgroup.Group

	// Where things stand: the rolling life synopsis (compact, grounded).
	g.Go(func() error {
		if ch, err := h.store.GetChronicleChapter(ctx, "life"); err == nil && ch != nil {
			synopsis = ch.Synopsis
		}
		return nil
	})

	// Open contradictions (non-dismissed), served from precomputed state
	// (stale-while-revalidate) — never an on-request LLM reconcile.
	g.Go(func() error {
		if h.brief == nil {
			return nil
		}
		conflicts, _ := h.brief.SemanticContradictionsCached(ctx, "")
		dismissed, _ := h.store.DismissedContradictions(ctx)
		for _, c := range conflicts {
			if dismissed[c.Key] {
				continue
			}
			contradictions = append(contradictions, c)
			if len(contradictions) >= maxContradictions {
				break
			}
		}
		return nil
	})

	// Decisions awaiting confirmation.
	g.Go(func() error {
		if ds, err := h.store.ListDecisions(ctx, "", "proposed"); err == nil {
			if len(ds) > maxDecisions {
				ds = ds[:maxDecisions]
			}
			proposed = ds
		}
		return nil
	})

	// Tasks due within the next week (open, dated, soonest first).
	g.Go(func() error {
		cutoff := time.Now().AddDate(0, 0, 7).UTC().Format(time.RFC3339)
		if ts, err := h.store.TasksDueBefore(ctx, cutoff); err == nil {
			if len(ts) > maxTasks {
				ts = ts[:maxTasks]
			}
			dueTasks = ts
		}
		return nil
	})

	// Where the user stands: a grounded summary of their confirmed decisions
	// (Angle A-2b) — served from the fingerprint-addressed cache; a miss schedules
	// an async regeneration (see PositionsSynopsisCached). No on-request LLM call.
	g.Go(func() error {
		positions = h.brief.PositionsSynopsisCached(ctx)
		return nil
	})

	// Coming up: deterministic prospection, process-cached (no corpus reload on a hit).
	g.Go(func() error {
		upcoming = h.brief.UpcomingItems(ctx, 45)
		return nil
	})

	_ = g.Wait()

	h.logger.Info().Int64("digest_ms", time.Since(start).Milliseconds()).Msg("digest served")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"synopsis":           synopsis,
		"positions":          positions,
		"contradictions":     contradictions,
		"proposed_decisions": proposed,
		"due_tasks":          dueTasks,
		"upcoming":           upcoming,
	})
}

// dismissContradictionRequest is the body of POST /knowledge/contradictions/dismiss.
type dismissContradictionRequest struct {
	Key  string `json:"key"`
	Undo bool   `json:"undo"` // true → restore a previously dismissed contradiction
}

// DismissContradiction handles POST /knowledge/contradictions/dismiss — records
// (or, with undo, clears) a dismissed contradiction key. 204 on success.
func (h *BriefHandler) DismissContradiction(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeBriefError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "not configured")
		return
	}
	var req dismissContradictionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeBriefError(w, http.StatusBadRequest, "BAD_REQUEST", "a contradiction key is required")
		return
	}
	var err error
	if req.Undo {
		err = h.store.UndismissContradiction(r.Context(), req.Key)
	} else {
		err = h.store.DismissContradiction(r.Context(), req.Key)
	}
	if err != nil {
		h.logger.Warn().Err(err).Msg("dismiss contradiction failed")
		writeBriefError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update dismissal")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeBriefError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
