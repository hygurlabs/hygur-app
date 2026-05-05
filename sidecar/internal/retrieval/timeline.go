package retrieval

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// TimelineSearcher is the subset of *UnifiedSearcher that the timeline
// builder actually consumes. Defining it as an interface lets tests stub
// retrieval out without standing up the full SQLite + LLM stack.
type TimelineSearcher interface {
	Search(ctx context.Context, req UnifiedSearchRequest) (*UnifiedSearchResponse, error)
}

// TimelineLLM is the subset of *llm.Client used to title chapters. nil is
// allowed — title generation falls back to a deterministic label.
type TimelineLLM interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

// TimelineQuery describes a request to build a chaptered timeline.
type TimelineQuery struct {
	Query      string
	FocusScope *FocusScope
	// RangeDays bounds how far in the past events may go. 0 means "no bound".
	RangeDays int
	// TopDocs caps how many search hits feed the flattening pass. Higher =
	// richer timeline, more tokens for chapter titling. Defaults to 80.
	TopDocs int
	// Now is injectable for deterministic tests. Zero falls back to time.Now().
	Now time.Time
}

// TimelineEvent is a single dated point on the frieze.
type TimelineEvent struct {
	Date       time.Time      `json:"-"`
	DateString string         `json:"date"`
	ContentID  string         `json:"content_id"`
	SourceType string         `json:"source_type"`
	Title      string         `json:"title"`
	Snippet    string         `json:"snippet"`
	Context    string         `json:"context,omitempty"`
	// Internal clustering signals — unexported so never serialized.
	persons  []string
	orgs     []string
	projects []string
	topic    string
}

// TimelineChapter groups related events sharing entities or topic within a
// time bucket. Events are sorted by date ascending.
type TimelineChapter struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	TimeStart        time.Time       `json:"time_start"`
	TimeEnd          time.Time       `json:"time_end"`
	DominantEntities []string        `json:"dominant_entities"`
	EventCount       int             `json:"event_count"`
	Events           []TimelineEvent `json:"events"`
}

// TimelineResponse is the full response shape served on /timeline/query.
type TimelineResponse struct {
	Chapters []TimelineChapter `json:"chapters"`
	Query    string            `json:"query"`
	Total    int               `json:"total_events"`
}

// TimelineBuilder owns the 4-pass pipeline. Construct once and reuse —
// the title cache is per-instance.
type TimelineBuilder struct {
	searcher   TimelineSearcher
	llm        TimelineLLM
	titleCache *titleCache
}

// NewTimelineBuilder wires a builder. llm may be nil; titles then come from
// the deterministic fallback only.
func NewTimelineBuilder(s TimelineSearcher, l TimelineLLM) *TimelineBuilder {
	return &TimelineBuilder{
		searcher:   s,
		llm:        l,
		titleCache: newTitleCache(),
	}
}

const (
	timelineDefaultTopDocs    = 80
	timelineMaxChaptersPerBkt = 6
	timelineTitleCacheTTL     = time.Hour
	timelineTitleMaxLen       = 60
)

var errTimelineNoSearcher = errors.New("timeline: searcher not configured")

// Build executes the 4 passes and returns the chaptered timeline.
func (b *TimelineBuilder) Build(ctx context.Context, q TimelineQuery) (*TimelineResponse, error) {
	if b == nil || b.searcher == nil {
		return nil, errTimelineNoSearcher
	}
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	topDocs := q.TopDocs
	if topDocs <= 0 {
		topDocs = timelineDefaultTopDocs
	}

	// Pass 1 — search.
	searchReq := UnifiedSearchRequest{
		Query:      q.Query,
		TopK:       topDocs,
		FocusScope: q.FocusScope,
	}
	resp, err := b.searcher.Search(ctx, searchReq)
	if err != nil {
		return nil, fmt.Errorf("timeline search: %w", err)
	}
	if resp == nil || len(resp.Results) == 0 {
		return &TimelineResponse{Chapters: []TimelineChapter{}, Query: q.Query}, nil
	}

	// Pass 2 — flatten to events.
	events := flattenTimelineEvents(resp.Results, now, q.RangeDays)
	if len(events) == 0 {
		return &TimelineResponse{Chapters: []TimelineChapter{}, Query: q.Query}, nil
	}

	// Pass 3 — cluster.
	chapters := clusterTimelineEvents(events)

	// Pass 4 — title (parallel + cache + fail-soft).
	b.titleAll(ctx, chapters)

	// Sort chapters newest first for UI.
	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].TimeStart.After(chapters[j].TimeStart)
	})

	return &TimelineResponse{
		Chapters: chapters,
		Query:    q.Query,
		Total:    len(events),
	}, nil
}

// flattenTimelineEvents expands each search result into 1+ dated events.
// extracted_event_dates carries the structured date+context pairs from
// Phase 4; when absent we fall back to the doc's own Date field (parsed
// best-effort) so corpora without entity extraction still render something.
func flattenTimelineEvents(results []UnifiedResult, now time.Time, rangeDays int) []TimelineEvent {
	var out []TimelineEvent
	cutoff := time.Time{}
	if rangeDays > 0 {
		cutoff = now.AddDate(0, 0, -rangeDays)
	}

	for _, r := range results {
		persons := lowerStrings(r.Metadata, "extracted_persons")
		orgs := lowerStrings(r.Metadata, "extracted_orgs")
		projects := lowerStrings(r.Metadata, "extracted_projects")
		topics := lowerStrings(r.Metadata, "extracted_topics")
		topic := ""
		if len(topics) > 0 {
			topic = topics[0]
		}
		snippet := clip(strings.TrimSpace(r.Excerpt))

		eventDates := readEventDates(r.Metadata)
		if len(eventDates) == 0 {
			// Fallback: use the doc's date.
			if t, ok := parseTimelineDate(r.Date); ok {
				if !cutoff.IsZero() && t.Before(cutoff) {
					continue
				}
				out = append(out, TimelineEvent{
					Date:       t,
					DateString: t.Format("2006-01-02"),
					ContentID:  r.ContentID,
					SourceType: r.SourceType,
					Title:      r.Title,
					Snippet:    snippet,
					persons:    persons,
					orgs:       orgs,
					projects:   projects,
					topic:      topic,
				})
			}
			continue
		}

		for _, ed := range eventDates {
			t, ok := parseTimelineDate(ed.date)
			if !ok {
				continue
			}
			if !cutoff.IsZero() && t.Before(cutoff) {
				continue
			}
			ctx := strings.TrimSpace(ed.context)
			eventSnippet := snippet
			if ctx != "" {
				eventSnippet = clip(ctx)
			}
			out = append(out, TimelineEvent{
				Date:       t,
				DateString: t.Format("2006-01-02"),
				ContentID:  r.ContentID,
				SourceType: r.SourceType,
				Title:      r.Title,
				Snippet:    eventSnippet,
				Context:    ctx,
				persons:    persons,
				orgs:       orgs,
				projects:   projects,
				topic:      topic,
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

// clusterTimelineEvents groups events by adaptive time buckets, then within
// each bucket by shared entities or topic. Caps at 6 chapters/bucket and
// merges the two smallest if exceeded.
func clusterTimelineEvents(events []TimelineEvent) []TimelineChapter {
	if len(events) == 0 {
		return nil
	}
	bucketKey := pickBucketStrategy(len(events))
	buckets := make(map[string][]TimelineEvent)
	bucketOrder := []string{}
	for _, e := range events {
		k := bucketKey(e.Date)
		if _, seen := buckets[k]; !seen {
			bucketOrder = append(bucketOrder, k)
		}
		buckets[k] = append(buckets[k], e)
	}

	var out []TimelineChapter
	for _, k := range bucketOrder {
		clusters := clusterByEntities(buckets[k])
		// Cap chapters per bucket — merge smallest pairs until ≤ max.
		for len(clusters) > timelineMaxChaptersPerBkt {
			// Find two smallest by event count.
			sort.SliceStable(clusters, func(i, j int) bool {
				return len(clusters[i].Events) < len(clusters[j].Events)
			})
			a := clusters[0]
			b := clusters[1]
			merged := mergeChapters(a, b)
			clusters = append([]TimelineChapter{merged}, clusters[2:]...)
		}
		out = append(out, clusters...)
	}
	return out
}

// pickBucketStrategy returns a function that maps a date to a bucket key,
// adapted to the event count: weekly for ≤30, monthly for ≤100, quarterly
// otherwise.
func pickBucketStrategy(n int) func(time.Time) string {
	switch {
	case n <= 30:
		// Weekly bucket: ISO year-week.
		return func(t time.Time) string {
			y, w := t.ISOWeek()
			return fmt.Sprintf("W:%04d-%02d", y, w)
		}
	case n <= 100:
		return func(t time.Time) string {
			return fmt.Sprintf("M:%04d-%02d", t.Year(), int(t.Month()))
		}
	default:
		return func(t time.Time) string {
			q := (int(t.Month())-1)/3 + 1
			return fmt.Sprintf("Q:%04d-Q%d", t.Year(), q)
		}
	}
}

// clusterByEntities groups events that share at least one dominant entity
// (person/org/project) OR the same topic. Events with no signals end up in
// their own singleton chapter.
func clusterByEntities(events []TimelineEvent) []TimelineChapter {
	type cluster struct {
		keys   map[string]struct{}
		topic  string
		events []TimelineEvent
	}
	var clusters []*cluster

	for _, e := range events {
		eKeys := entityKeys(e)
		assigned := false
		for _, c := range clusters {
			if shareKey(c.keys, eKeys) || (e.topic != "" && e.topic == c.topic) {
				c.events = append(c.events, e)
				for k := range eKeys {
					c.keys[k] = struct{}{}
				}
				if c.topic == "" {
					c.topic = e.topic
				}
				assigned = true
				break
			}
		}
		if !assigned {
			c := &cluster{
				keys:   eKeys,
				topic:  e.topic,
				events: []TimelineEvent{e},
			}
			clusters = append(clusters, c)
		}
	}

	out := make([]TimelineChapter, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, buildChapter(c.events, dominantList(c.events)))
	}
	return out
}

// entityKeys extracts the clustering keys for an event. Each persona, org,
// or project is namespaced so a person and an org with the same name don't
// accidentally cross-merge.
func entityKeys(e TimelineEvent) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range e.persons {
		out["p:"+p] = struct{}{}
	}
	for _, o := range e.orgs {
		out["o:"+o] = struct{}{}
	}
	for _, pr := range e.projects {
		out["pr:"+pr] = struct{}{}
	}
	return out
}

func shareKey(a, b map[string]struct{}) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for k := range b {
		if _, ok := a[k]; ok {
			return true
		}
	}
	return false
}

// buildChapter packages a list of events into a sorted chapter.
func buildChapter(events []TimelineEvent, dominant []string) TimelineChapter {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Date.Before(events[j].Date) })
	tStart := events[0].Date
	tEnd := events[len(events)-1].Date
	id := chapterID(events)
	return TimelineChapter{
		ID:               id,
		TimeStart:        tStart,
		TimeEnd:          tEnd,
		DominantEntities: dominant,
		EventCount:       len(events),
		Events:           events,
	}
}

// dominantList computes the top-3 entities (any namespace) by frequency.
func dominantList(events []TimelineEvent) []string {
	counts := map[string]int{}
	for _, e := range events {
		for _, p := range e.persons {
			counts["👤 "+p] += 1
		}
		for _, o := range e.orgs {
			counts["🏢 "+o] += 1
		}
		for _, pr := range e.projects {
			counts["📁 "+pr] += 1
		}
	}
	type kv struct {
		k string
		v int
	}
	var ranked []kv
	for k, v := range counts {
		ranked = append(ranked, kv{k, v})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].v == ranked[j].v {
			return ranked[i].k < ranked[j].k
		}
		return ranked[i].v > ranked[j].v
	})
	out := make([]string, 0, 3)
	for i, r := range ranked {
		if i >= 3 {
			break
		}
		out = append(out, r.k)
	}
	return out
}

func mergeChapters(a, b TimelineChapter) TimelineChapter {
	merged := append([]TimelineEvent{}, a.Events...)
	merged = append(merged, b.Events...)
	dominant := dominantList(merged)
	return buildChapter(merged, dominant)
}

func chapterID(events []TimelineEvent) string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ContentID + "@" + e.DateString
	}
	sort.Strings(ids)
	h := sha1.Sum([]byte(strings.Join(ids, "|")))
	return hex.EncodeToString(h[:8])
}

// titleAll fills Title for each chapter. Cache hits are zero-cost; misses
// dispatch to the LLM with a 6 s budget. Failures fall back to a
// deterministic label.
func (b *TimelineBuilder) titleAll(ctx context.Context, chapters []TimelineChapter) {
	if len(chapters) == 0 {
		return
	}
	var wg sync.WaitGroup
	for i := range chapters {
		i := i
		if cached, ok := b.titleCache.get(chapters[i].ID); ok {
			chapters[i].Title = cached
			continue
		}
		fallback := fallbackChapterTitle(chapters[i])
		chapters[i].Title = fallback // pre-fill so any LLM error keeps a sane default
		if b.llm == nil {
			b.titleCache.set(chapters[i].ID, fallback)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			line, err := b.titleViaLLM(ctx, chapters[i])
			if err != nil || line == "" {
				return
			}
			// Each goroutine writes to its own chapters[i] — distinct indices,
			// no shared element, so no mutex needed for the slice itself.
			chapters[i].Title = line
			b.titleCache.set(chapters[i].ID, line)
		}()
	}
	wg.Wait()
}

const timelineTitleSystemPrompt = `/no_think
Tu donnes un titre court (≤ 60 caractères) à un groupe d'évènements liés.
Format: une seule ligne descriptive, sans guillemets, sans markdown.
Tu utilises les noms d'entités fournis. Tu n'inventes rien.`

func (b *TimelineBuilder) titleViaLLM(ctx context.Context, ch TimelineChapter) (string, error) {
	llmCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("Période: ")
	sb.WriteString(ch.TimeStart.Format("2006-01-02"))
	sb.WriteString(" → ")
	sb.WriteString(ch.TimeEnd.Format("2006-01-02"))
	sb.WriteString("\nEntités dominantes: ")
	if len(ch.DominantEntities) == 0 {
		sb.WriteString("(aucune)")
	} else {
		sb.WriteString(strings.Join(ch.DominantEntities, ", "))
	}
	sb.WriteString("\nÉvènements:")
	maxDetails := 5
	for i, e := range ch.Events {
		if i >= maxDetails {
			sb.WriteString(fmt.Sprintf("\n- (+%d autres)", len(ch.Events)-maxDetails))
			break
		}
		sb.WriteString("\n- ")
		sb.WriteString(e.DateString)
		sb.WriteString(": ")
		if e.Title != "" {
			sb.WriteString(e.Title)
		}
		if e.Context != "" {
			sb.WriteString(" — ")
			sb.WriteString(e.Context)
		}
	}

	resp, err := b.llm.Chat(llmCtx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: timelineTitleSystemPrompt},
			{Role: "user", Content: sb.String()},
		},
		Stream:      false,
		Temperature: 0,
		MaxTokens:   60,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return "", fmt.Errorf("empty llm response")
	}
	line := cleanLLMLine(resp.Choices[0].Message.Content)
	if line == "" {
		return "", fmt.Errorf("llm returned empty title")
	}
	if len([]rune(line)) > timelineTitleMaxLen {
		runes := []rune(line)
		line = string(runes[:timelineTitleMaxLen-1]) + "…"
	}
	return line, nil
}

// fallbackChapterTitle is used when the LLM is unavailable or returns junk.
// Format: "<top entity> — <month range>" (or just the range when unknown).
func fallbackChapterTitle(ch TimelineChapter) string {
	rng := chapterRangeLabel(ch.TimeStart, ch.TimeEnd)
	if len(ch.DominantEntities) == 0 {
		return fmt.Sprintf("%d évènements — %s", ch.EventCount, rng)
	}
	return fmt.Sprintf("%s — %s", ch.DominantEntities[0], rng)
}

func chapterRangeLabel(start, end time.Time) string {
	if start.Year() == end.Year() && start.Month() == end.Month() {
		return start.Format("janvier 2006")
	}
	if start.Year() == end.Year() {
		return start.Format("janvier") + " → " + end.Format("janvier 2006")
	}
	return start.Format("01/2006") + " → " + end.Format("01/2006")
}

// readEventDates extracts the typed event_dates list, tolerating the JSON
// round-trip shape ([]any of map[string]any) as well as the in-process shape
// ([]map[string]any).
type rawEventDate struct {
	date    string
	context string
}

func readEventDates(meta map[string]any) []rawEventDate {
	if meta == nil {
		return nil
	}
	raw, ok := meta["extracted_event_dates"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []map[string]any:
		out := make([]rawEventDate, 0, len(v))
		for _, m := range v {
			out = append(out, asRawEventDate(m))
		}
		return out
	case []any:
		out := make([]rawEventDate, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				out = append(out, asRawEventDate(m))
			}
		}
		return out
	}
	return nil
}

func asRawEventDate(m map[string]any) rawEventDate {
	d, _ := m["date"].(string)
	c, _ := m["context"].(string)
	return rawEventDate{date: strings.TrimSpace(d), context: strings.TrimSpace(c)}
}

// parseTimelineDate accepts the common shapes we encounter in metadata and
// the doc Date column: 2006-01-02, RFC3339, or RFC3339 with no zone.
func parseTimelineDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006/01/02",
		"02/01/2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// titleCache is a TTL-bounded map keyed by chapter ID (sha1 of content_ids).
// A new run with the same composition is a cache hit and skips the LLM.
type titleCache struct {
	mu      sync.Mutex
	entries map[string]titleCacheEntry
}

type titleCacheEntry struct {
	value     string
	expiresAt time.Time
}

func newTitleCache() *titleCache {
	return &titleCache{entries: make(map[string]titleCacheEntry)}
}

func (c *titleCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return "", false
	}
	return e.value, true
}

func (c *titleCache) set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = titleCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(timelineTitleCacheTTL),
	}
}
