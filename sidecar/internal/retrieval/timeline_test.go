package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// fakeTimelineSearcher returns a static UnifiedSearchResponse so we can test
// the flatten/cluster/title pipeline without standing up a real DB.
type fakeTimelineSearcher struct {
	results []UnifiedResult
	err     error
}

func (f *fakeTimelineSearcher) Search(_ context.Context, _ UnifiedSearchRequest) (*UnifiedSearchResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &UnifiedSearchResponse{Results: f.results}, nil
}

// fakeTimelineLLM is a stub that either returns canned titles or fails. We
// use the user message to select the canned title when available so two
// chapters get distinct titles in a single Build run.
type fakeTimelineLLM struct {
	titlesByEntity map[string]string
	failOnce       bool
	failed         bool
	calls          int
}

func (f *fakeTimelineLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	if f.failOnce && !f.failed {
		f.failed = true
		return nil, errors.New("transient llm fail")
	}
	user := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			user = m.Content
			break
		}
	}
	for entity, title := range f.titlesByEntity {
		if strings.Contains(user, entity) {
			return &llm.ChatResponse{Choices: []llm.Choice{{Message: &llm.Message{Content: title}}}}, nil
		}
	}
	return &llm.ChatResponse{Choices: []llm.Choice{{Message: &llm.Message{Content: "untitled"}}}}, nil
}

func mkResult(contentID, title string, dates []map[string]any, persons, orgs, projects, topics []string) UnifiedResult {
	meta := map[string]any{}
	if len(dates) > 0 {
		meta["extracted_event_dates"] = dates
	}
	if len(persons) > 0 {
		meta["extracted_persons"] = persons
	}
	if len(orgs) > 0 {
		meta["extracted_orgs"] = orgs
	}
	if len(projects) > 0 {
		meta["extracted_projects"] = projects
	}
	if len(topics) > 0 {
		meta["extracted_topics"] = topics
	}
	return UnifiedResult{
		ContentID: contentID,
		Title:     title,
		Excerpt:   title,
		Metadata:  meta,
	}
}

// TestTimeline_GroupsSameTopicSameWeek asserts events sharing an entity in
// the same week get merged into a single chapter, while a third event with
// no overlap stays separate.
func TestTimeline_GroupsSameTopicSameWeek(t *testing.T) {
	results := []UnifiedResult{
		mkResult("doc:a", "Mail Acme Compta 1",
			[]map[string]any{{"date": "2026-03-02", "context": "Devis TVA"}},
			nil, []string{"Acme Compta"}, nil, []string{"facture"}),
		mkResult("doc:b", "Mail Acme Compta 2",
			[]map[string]any{{"date": "2026-03-04", "context": "Suivi TVA"}},
			nil, []string{"Acme Compta"}, nil, []string{"facture"}),
		mkResult("doc:c", "Mail Vétérinaire",
			[]map[string]any{{"date": "2026-03-03", "context": "RDV chien"}},
			nil, []string{"VetClinic"}, nil, []string{"rdv"}),
	}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query: "TVA",
		Now:   time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Build err: %v", err)
	}
	if len(resp.Chapters) != 2 {
		t.Fatalf("want 2 chapters, got %d", len(resp.Chapters))
	}
	// Find the Acme Compta chapter — it should hold 2 events.
	var found bool
	for _, ch := range resp.Chapters {
		if ch.EventCount == 2 {
			found = true
			ids := map[string]bool{}
			for _, e := range ch.Events {
				ids[e.ContentID] = true
			}
			if !ids["doc:a"] || !ids["doc:b"] {
				t.Fatalf("merged chapter should contain doc:a and doc:b, got %v", ids)
			}
		}
	}
	if !found {
		t.Fatal("expected one chapter with 2 events")
	}
}

// TestTimeline_KeepsIsolatedEventAsSingleton checks that an event with no
// shared entity remains its own chapter.
func TestTimeline_KeepsIsolatedEventAsSingleton(t *testing.T) {
	results := []UnifiedResult{
		mkResult("doc:lonely", "Ancien doc",
			[]map[string]any{{"date": "2026-01-15", "context": "isolated"}},
			[]string{"Alice"}, nil, nil, nil),
		mkResult("doc:other", "Autre doc",
			[]map[string]any{{"date": "2026-01-16", "context": "different"}},
			[]string{"Bob"}, nil, nil, nil),
	}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query: "x",
		Now:   time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Chapters) != 2 {
		t.Fatalf("want 2 singletons, got %d", len(resp.Chapters))
	}
	for _, ch := range resp.Chapters {
		if ch.EventCount != 1 {
			t.Fatalf("expected each chapter to have 1 event, got %d", ch.EventCount)
		}
	}
}

// TestTimeline_AdaptiveBucketing_ManyEvents asserts that with > 30 events
// spread across multiple months we use monthly (not weekly) bucketing —
// otherwise tightly-clustered weeks would explode the chapter count.
func TestTimeline_AdaptiveBucketing_ManyEvents(t *testing.T) {
	var results []UnifiedResult
	// 40 events spread across 4 months (10 per month), all sharing the
	// same org → with monthly bucketing we expect 4 chapters.
	for monthIdx, m := range []time.Month{time.January, time.February, time.March, time.April} {
		for d := 1; d <= 10; d++ {
			id := time.Date(2026, m, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
			results = append(results, mkResult(
				"doc:m"+id,
				"Acme event "+id,
				[]map[string]any{{"date": id, "context": "acme"}},
				nil, []string{"Acme"}, nil, nil,
			))
		}
		_ = monthIdx
	}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query: "Acme",
		Now:   time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// We expect 4 monthly buckets × 1 entity-cluster per bucket = 4 chapters.
	if len(resp.Chapters) != 4 {
		t.Fatalf("want 4 monthly chapters, got %d", len(resp.Chapters))
	}
	for _, ch := range resp.Chapters {
		if ch.EventCount != 10 {
			t.Errorf("monthly chapter expected 10 events, got %d", ch.EventCount)
		}
	}
}

// TestTimeline_FailSoftWhenLLMDown verifies that the chapter still gets a
// non-empty title when the LLM call returns an error.
func TestTimeline_FailSoftWhenLLMDown(t *testing.T) {
	results := []UnifiedResult{
		mkResult("doc:x", "Doc",
			[]map[string]any{{"date": "2026-03-10"}},
			nil, []string{"Acme"}, nil, nil),
	}
	llmStub := &fakeTimelineLLM{failOnce: true}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, llmStub)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query: "Acme",
		Now:   time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Chapters) != 1 {
		t.Fatalf("want 1 chapter, got %d", len(resp.Chapters))
	}
	title := resp.Chapters[0].Title
	if title == "" {
		t.Fatal("chapter title is empty after LLM failure — fail-soft did not apply")
	}
	if !strings.Contains(strings.ToLower(title), "acme") {
		t.Errorf("fallback title should mention dominant entity, got %q", title)
	}
}

// TestTimeline_EmptySearchReturnsEmpty confirms a no-results search produces
// an empty (not nil) chapter list.
func TestTimeline_EmptySearchReturnsEmpty(t *testing.T) {
	b := NewTimelineBuilder(&fakeTimelineSearcher{}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{Query: "nothing"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if len(resp.Chapters) != 0 {
		t.Fatalf("want 0 chapters, got %d", len(resp.Chapters))
	}
}

// TestTimeline_RangeDaysFiltersOldEvents verifies range_days drops events
// outside the window.
func TestTimeline_RangeDaysFiltersOldEvents(t *testing.T) {
	results := []UnifiedResult{
		mkResult("doc:fresh", "Fresh",
			[]map[string]any{{"date": "2026-04-15"}},
			nil, []string{"Acme"}, nil, nil),
		mkResult("doc:stale", "Stale",
			[]map[string]any{{"date": "2024-01-01"}},
			nil, []string{"Acme"}, nil, nil),
	}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query:     "Acme",
		RangeDays: 90,
		Now:       time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	totalEvents := 0
	for _, ch := range resp.Chapters {
		totalEvents += ch.EventCount
	}
	if totalEvents != 1 {
		t.Fatalf("range_days=90 should keep 1 event, got %d", totalEvents)
	}
}

// TestTimeline_TitleCacheHitsSkipLLM ensures the second Build call with the
// same chapter composition does not re-invoke the LLM.
func TestTimeline_TitleCacheHitsSkipLLM(t *testing.T) {
	results := []UnifiedResult{
		mkResult("doc:c1", "Doc 1",
			[]map[string]any{{"date": "2026-03-10"}},
			nil, []string{"Acme"}, nil, nil),
	}
	llmStub := &fakeTimelineLLM{titlesByEntity: map[string]string{"Acme": "Réunion Acme — mars"}}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: results}, llmStub)
	now := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	if _, err := b.Build(context.Background(), TimelineQuery{Query: "x", Now: now}); err != nil {
		t.Fatalf("first build: %v", err)
	}
	callsAfterFirst := llmStub.calls
	if callsAfterFirst == 0 {
		t.Fatal("expected at least one LLM call on first build")
	}
	if _, err := b.Build(context.Background(), TimelineQuery{Query: "x", Now: now}); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if llmStub.calls != callsAfterFirst {
		t.Fatalf("second build should be cache hit; calls grew %d → %d", callsAfterFirst, llmStub.calls)
	}
}

// TestTimeline_ToleratesAnyShapedEventDates verifies the JSON round-trip
// shape ([]any of map[string]any) is decoded just like the in-process shape.
func TestTimeline_ToleratesAnyShapedEventDates(t *testing.T) {
	r := UnifiedResult{
		ContentID: "doc:any",
		Title:     "Any-shape",
		Excerpt:   "snippet",
		Metadata: map[string]any{
			"extracted_event_dates": []any{
				map[string]any{"date": "2026-02-10", "context": "ctx-a"},
				map[string]any{"date": "2026-02-11", "context": "ctx-b"},
			},
			"extracted_orgs": []any{"Acme"},
		},
	}
	b := NewTimelineBuilder(&fakeTimelineSearcher{results: []UnifiedResult{r}}, nil)
	resp, err := b.Build(context.Background(), TimelineQuery{
		Query: "x",
		Now:   time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	total := 0
	for _, ch := range resp.Chapters {
		total += ch.EventCount
	}
	if total != 2 {
		t.Fatalf("want 2 events from []any shape, got %d", total)
	}
}

// TestTimeline_NoSearcherReturnsError is a defensive guardrail.
func TestTimeline_NoSearcherReturnsError(t *testing.T) {
	var b *TimelineBuilder
	_, err := b.Build(context.Background(), TimelineQuery{Query: "x"})
	if err == nil {
		t.Fatal("expected error for nil builder")
	}

	b2 := &TimelineBuilder{}
	_, err = b2.Build(context.Background(), TimelineQuery{Query: "x"})
	if err == nil {
		t.Fatal("expected error when searcher unset")
	}
}
