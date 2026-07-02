package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// MeetingInput describes an upcoming event or deadline that may warrant a
// briefing. Kind is "calendar" (timed, native-initiated) or "mail" (a
// date-only deadline extracted from a message, scheduler-initiated).
type MeetingInput struct {
	Kind      string
	Key       string // stable dedup key (event id / source content_id)
	Title     string
	Attendees []string
	Notes     string
	Location  string
	When      time.Time
}

// MeetingResult is returned to the HTTP caller (and used internally).
type MeetingResult struct {
	Relevant  bool     `json:"relevant"`
	ContentID string   `json:"content_id,omitempty"`
	Brief     string   `json:"brief,omitempty"`
	Bullets   []string `json:"bullets,omitempty"`
	Sources   int      `json:"sources"`
}

// MeetingBriefer generates a short RAG briefing for an event/deadline, persists
// it as a knowledge_item (source_type="meeting_brief"), and emits a
// meeting_briefing SSE event — the single notification trigger consumed by the
// macOS NotificationsService. "Only if relevant data found": when the KB has no
// context for the event, nothing is persisted or emitted (Relevant=false).
type MeetingBriefer struct {
	store    *store.DB
	llm      *llm.Client
	searcher *retrieval.UnifiedSearcher
	broker   *events.Broker
	logger   zerolog.Logger
}

// NewMeetingBriefer builds a MeetingBriefer. Returns nil if any dependency is
// missing (feature disabled).
func NewMeetingBriefer(db *store.DB, llmClient *llm.Client, searcher *retrieval.UnifiedSearcher, broker *events.Broker, logger zerolog.Logger) *MeetingBriefer {
	if db == nil || llmClient == nil || searcher == nil || broker == nil {
		return nil
	}
	return &MeetingBriefer{
		store:    db,
		llm:      llmClient,
		searcher: searcher,
		broker:   broker,
		logger:   logger.With().Str("component", "meeting_brief").Logger(),
	}
}

// Generate produces a briefing for the input. When the KB yields relevant
// context it persists the brief and emits the SSE event; otherwise it returns
// Relevant=false and does nothing else.
func (m *MeetingBriefer) Generate(ctx context.Context, in MeetingInput) (MeetingResult, error) {
	if m == nil {
		return MeetingResult{}, fmt.Errorf("meeting briefer not configured")
	}
	query := buildMeetingQuery(in)
	res, err := m.searcher.Search(ctx, retrieval.UnifiedSearchRequest{Query: query, TopK: 8})
	if err != nil {
		return MeetingResult{}, fmt.Errorf("meeting brief search: %w", err)
	}
	if res == nil || len(res.Results) == 0 {
		m.logger.Debug().Str("title", in.Title).Msg("no relevant context — skipping meeting brief")
		return MeetingResult{Relevant: false}, nil
	}

	prompt := buildMeetingPrompt(in, res.Results)
	resp, err := m.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You prepare a short briefing ahead of a meeting or deadline. Rely ONLY on the provided context, invent nothing. Keep internal reasoning brief. Reply in Markdown, in English, in 3 to 6 bullets maximum."},
			{Role: "user", Content: prompt},
		},
		Temperature: llm.Temp(0.3),
		MaxTokens:   2048,
	})
	briefText := ""
	if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		briefText = stripReasoningTags(resp.Choices[0].Message.Content)
	}
	if briefText == "" {
		m.logger.Warn().Err(err).Str("title", in.Title).Msg("meeting brief LLM returned empty — skipping")
		return MeetingResult{Relevant: false}, nil
	}

	contentID := meetingContentID(in)
	if err := m.persist(ctx, contentID, meetingTitle(in), briefText, in); err != nil {
		return MeetingResult{}, err
	}

	bullets := firstBullets(briefText, 4)
	m.broker.Publish(events.NewMeetingBriefingEvent(events.MeetingBriefingPayload{
		Kind:      in.Kind,
		ContentID: contentID,
		Title:     in.Title,
		WhenISO:   in.When.Format(time.RFC3339),
		Bullets:   bullets,
	}))
	m.logger.Info().Str("content_id", contentID).Str("kind", in.Kind).Int("sources", len(res.Results)).Msg("meeting brief published")
	return MeetingResult{Relevant: true, ContentID: contentID, Brief: briefText, Bullets: bullets, Sources: len(res.Results)}, nil
}

func (m *MeetingBriefer) persist(ctx context.Context, contentID, title, content string, in MeetingInput) error {
	hash := sha256.Sum256([]byte(content))
	versionID := hex.EncodeToString(hash[:])[:16]
	metadata := map[string]any{
		"kind":        in.Kind,
		"when":        in.When.Format(time.RFC3339),
		"event_title": in.Title,
	}
	if in.Key != "" {
		metadata["source_key"] = in.Key
	}
	if len(in.Attendees) > 0 {
		metadata["attendees"] = in.Attendees
	}
	now := time.Now()
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "meeting_brief",
		Title:          title,
		NormalizedText: content,
		Metadata:       metadata,
		VersionID:      versionID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if existing, err := m.store.GetKnowledgeItem(ctx, contentID); err == nil && existing != nil {
		if err := m.store.DeleteKnowledgeItem(ctx, contentID); err != nil {
			return fmt.Errorf("meeting brief: delete existing: %w", err)
		}
	}
	if err := m.store.InsertKnowledgeItem(ctx, item); err != nil {
		return fmt.Errorf("meeting brief: insert: %w", err)
	}
	return nil
}

func buildMeetingQuery(in MeetingInput) string {
	var b strings.Builder
	b.WriteString(in.Title)
	if len(in.Attendees) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(in.Attendees, " "))
	}
	if in.Notes != "" {
		b.WriteString(" ")
		b.WriteString(in.Notes)
	}
	return strings.TrimSpace(b.String())
}

func buildMeetingPrompt(in MeetingInput, results []retrieval.UnifiedResult) string {
	var sb strings.Builder
	if in.Kind == "mail" {
		sb.WriteString("Deadline to prepare: \"")
	} else {
		sb.WriteString("Meeting to prepare: \"")
	}
	sb.WriteString(in.Title)
	sb.WriteString("\"")
	if !in.When.IsZero() {
		sb.WriteString(" (")
		sb.WriteString(in.When.Format("02/01 15:04"))
		sb.WriteString(")")
	}
	sb.WriteString(".\n")
	if len(in.Attendees) > 0 {
		sb.WriteString("Attendees: ")
		sb.WriteString(strings.Join(in.Attendees, ", "))
		sb.WriteString(".\n")
	}
	if in.Location != "" {
		sb.WriteString("Location: ")
		sb.WriteString(in.Location)
		sb.WriteString(".\n")
	}
	if in.Notes != "" {
		sb.WriteString("Notes: ")
		sb.WriteString(in.Notes)
		sb.WriteString("\n")
	}
	sb.WriteString("\nGenerate a short briefing (3-6 bullets): what to know/recall, decisions or pending points, and the action to take. End with a \"Context:\" bullet listing the items used.\n\n")
	sb.WriteString("Context from the personal knowledge base:\n")
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("- [%s] %s", r.SourceType, r.Title))
		if r.MailFrom != "" {
			sb.WriteString(" — de ")
			sb.WriteString(r.MailFrom)
		}
		excerpt := r.Excerpt
		if len(excerpt) > 500 {
			excerpt = excerpt[:500] + "…"
		}
		sb.WriteString("\n  > ")
		sb.WriteString(strings.ReplaceAll(excerpt, "\n", " "))
		sb.WriteByte('\n')
		if i >= 7 {
			break
		}
	}
	return sb.String()
}

// meetingContentID derives a stable id so re-briefing the same event/deadline
// overwrites (persist deletes-then-inserts) rather than piling up duplicates. The
// mail path must NOT fold in the wall-clock time: its When is set to now on every
// tick, so hashing it would mint a fresh id each run and accumulate a new brief every
// cycle. Keying mail on (Kind, Key=source content_id) makes a re-brief overwrite.
// Calendar keeps When (the event start) so distinct occurrences stay distinct.
func meetingContentID(in MeetingInput) string {
	key := in.Kind + "|" + in.Key
	if in.Kind != "mail" {
		key += "|" + in.When.Format(time.RFC3339)
	}
	h := sha256.Sum256([]byte(key))
	return "meeting_brief:" + in.Kind + ":" + hex.EncodeToString(h[:])[:16]
}

func meetingTitle(in MeetingInput) string {
	prefix := "Briefing"
	when := ""
	if !in.When.IsZero() {
		when = " — " + in.When.Format("02/01 15:04")
	}
	return prefix + " : " + in.Title + when
}

// MeetingBriefScheduler periodically briefs (a) upcoming calendar events — the
// server-side path that covers the cloud, where there's no macOS app to call
// POST /brief/meeting — and (b) date-only mail deadlines due today. Calendar
// events are briefed whenever they fall within the lookahead window (any hour);
// mail deadlines fire once per day after the configured morning hour. Dedup is
// in-memory (briefed map) — restarts may re-brief, acceptable for a notification.
type MeetingBriefScheduler struct {
	briefer     *MeetingBriefer
	store       *store.DB
	extractor   *agenda.Extractor
	interval    time.Duration
	morningHour int
	logger      zerolog.Logger
	briefed     map[string]struct{}
}

// NewMeetingBriefScheduler builds the mail-deadline briefing loop. Returns nil
// when any dependency is missing.
func NewMeetingBriefScheduler(briefer *MeetingBriefer, db *store.DB, extractor *agenda.Extractor, morningHour int, logger zerolog.Logger) *MeetingBriefScheduler {
	if briefer == nil || db == nil || extractor == nil {
		return nil
	}
	if morningHour < 0 || morningHour > 23 {
		morningHour = 8
	}
	return &MeetingBriefScheduler{
		briefer:     briefer,
		store:       db,
		extractor:   extractor,
		interval:    15 * time.Minute,
		morningHour: morningHour,
		logger:      logger.With().Str("component", "meeting_brief_scheduler").Logger(),
		briefed:     map[string]struct{}{},
	}
}

// Start launches the polling loop. No-op when the scheduler is disabled (nil).
func (s *MeetingBriefScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		// Run once after boot so a deadline due today is briefed without waiting a
		// full interval — but deferred (was 90s) to leave the LLM free for the user's
		// first interaction (WP21).
		first := time.NewTimer(5 * time.Minute)
		defer first.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-first.C:
				s.tick(ctx)
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

// tickCalendar briefs calendar events starting within the lookahead window — the
// server-side / cloud equivalent of the macOS app's pre-event brief (which calls
// POST /brief/meeting ~30 min before). Not gated by morningHour: a brief is timed
// to the meeting, not the morning. The briefer itself skips events with no
// relevant KB context (Relevant=false), so quiet events make no noise.
func (s *MeetingBriefScheduler) tickCalendar(ctx context.Context, now time.Time) {
	const (
		lookahead = 45 * time.Minute
		maxEvents = 20
	)
	events, err := s.store.ListEventsInWindow(ctx, now, now.Add(lookahead), maxEvents)
	if err != nil {
		s.logger.Debug().Err(err).Msg("meeting brief tick: list events failed")
		return
	}
	for _, ev := range events {
		dedupKey := "event|" + ev.ContentID
		if _, done := s.briefed[dedupKey]; done {
			continue
		}
		// The CalDAV connector stores the event start as created_at.
		if _, err := s.briefer.Generate(ctx, MeetingInput{
			Kind:  "calendar",
			Key:   ev.ContentID,
			Title: ev.Title,
			When:  ev.CreatedAt,
		}); err != nil {
			s.logger.Debug().Err(err).Str("event", ev.ContentID).Msg("calendar brief failed")
			continue
		}
		s.briefed[dedupKey] = struct{}{}
	}
}

func (s *MeetingBriefScheduler) tick(ctx context.Context) {
	now := time.Now()
	// Calendar events first — timed to the meeting, not gated by the morning hour.
	s.tickCalendar(ctx, now)
	if now.Hour() < s.morningHour {
		return // too early to disturb the user with mail-deadline briefs
	}
	items, err := s.store.ListRecentItems(ctx, 24*14)
	if err != nil {
		s.logger.Debug().Err(err).Msg("meeting brief tick: list items failed")
		return
	}
	// Feed the deadline extractor only real inbound content (mail + notes), never the
	// scheduler's own generated briefs. ListRecentItems has no source-type filter, so
	// it also returns the meeting_brief items this loop just wrote; extracting a
	// deadline from a brief and briefing it again is a self-amplifying loop.
	real := items[:0]
	for _, it := range items {
		if store.IsMailSourceType(it.SourceType) || it.SourceType == store.SourceTypeNote {
			real = append(real, it)
		}
	}
	actions, err := s.extractor.ExtractActions(ctx, real)
	if err != nil {
		s.logger.Debug().Err(err).Msg("meeting brief tick: extract failed")
		return
	}
	today := now.Format("2006-01-02")
	for _, a := range actions {
		if a.DeadlineISO != today {
			continue // only brief deadlines due today
		}
		dedupKey := a.SourceID + "|" + a.DeadlineISO
		if _, done := s.briefed[dedupKey]; done {
			continue
		}
		in := MeetingInput{
			Kind:  "mail",
			Key:   a.SourceID,
			Title: a.What,
			When:  now,
		}
		if _, err := s.briefer.Generate(ctx, in); err != nil {
			s.logger.Debug().Err(err).Str("source", a.SourceID).Msg("meeting brief generate failed")
			continue
		}
		s.briefed[dedupKey] = struct{}{}
	}
}
