package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// CalendarSummaryResult is the calendar-view header synthesis.
type CalendarSummaryResult struct {
	Summary string `json:"summary"` // short prose; empty when nothing is upcoming
	Window  string `json:"window"`  // "today" | "next 2 days" | "this week"
	Count   int    `json:"count"`   // events in the chosen window
}

const calSummaryTTL = time.Hour

// Single-tenant process → a package-level cache is enough.
var (
	calSumMu      sync.Mutex
	calSumKey     string
	calSumValue   CalendarSummaryResult
	calSumExpires time.Time
)

// calSummarySystemPrompt is deliberately strict (anti-hallucination, "facts
// before reply"): the model may only use the events it's handed.
const calSummarySystemPrompt = `You are a personal assistant. Write a VERY short summary (2-4 sentences) of the upcoming agenda, most urgent first.

Rules:
- Use only the events listed; never invent or add a detail (date, time, place, person) that isn't there.
- If "This weekend" lists a birthday or party, end with a short weekend reminder; otherwise don't.
- One flowing short paragraph, no preamble or bullets. English, minimal reasoning.`

var partyKeywordRe = regexp.MustCompile(`(?i)anniversaire|annif|birthday|soir[ée]e|soiree|party|f[êe]te|ap[ée]ro|apero|barbecue|\bbbq\b`)

// CalendarSummary returns a short, LLM-written synthesis of upcoming events
// (adaptive window) plus a weekend birthdays/parties reminder. Empty when nothing
// is upcoming (the UI then shows its own "nothing coming up"). Cached ~1h.
func (d *DailyBrief) CalendarSummary(ctx context.Context) (CalendarSummaryResult, error) {
	if d == nil || d.store == nil || d.llm == nil {
		return CalendarSummaryResult{}, nil
	}
	now := time.Now()
	weekEnd := now.Add(7 * 24 * time.Hour)
	// Small past margin so an event earlier today still counts as "today".
	events, err := d.store.ListEventsInWindow(ctx, now.Add(-3*time.Hour), weekEnd, 60)
	if err != nil {
		return CalendarSummaryResult{}, err
	}
	if len(events) == 0 {
		return CalendarSummaryResult{}, nil
	}

	// Adaptive window: busy today → today; otherwise a light week → the week;
	// otherwise the next 2 days.
	endToday := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	twoDayEnd := now.Add(48 * time.Hour)
	var today, twoDay []*store.KnowledgeItem
	week := events
	for _, e := range events {
		st := eventStart(e)
		if !st.After(endToday) {
			today = append(today, e)
		}
		if !st.After(twoDayEnd) {
			twoDay = append(twoDay, e)
		}
	}
	var windowed []*store.KnowledgeItem
	var label string
	switch {
	case len(today) >= 4:
		windowed, label = today, "today"
	case len(week) < 10:
		windowed, label = week, "this week"
	case len(twoDay) > 0:
		windowed, label = twoDay, "next 2 days"
	default:
		windowed, label = week, "this week"
	}
	if len(windowed) > 14 {
		windowed = windowed[:14]
	}
	weekend := weekendHighlights(week)

	key := calSummaryCacheKey(windowed, weekend, label)
	calSumMu.Lock()
	if calSumKey == key && time.Now().Before(calSumExpires) {
		v := calSumValue
		calSumMu.Unlock()
		return v, nil
	}
	calSumMu.Unlock()

	summary := d.generateCalendarSummary(ctx, now, windowed, weekend, label)
	res := CalendarSummaryResult{Summary: summary, Window: label, Count: len(windowed)}

	calSumMu.Lock()
	calSumKey, calSumValue, calSumExpires = key, res, time.Now().Add(calSummaryTTL)
	calSumMu.Unlock()
	return res, nil
}

func (d *DailyBrief) generateCalendarSummary(ctx context.Context, now time.Time, windowed, weekend []*store.KnowledgeItem, label string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Today: %s\nWindow: %s\n\nUpcoming events (nearest to furthest):\n", now.Format("2006-01-02 (Mon)"), label)
	for _, e := range windowed {
		sb.WriteString("- ")
		sb.WriteString(eventLine(e))
		sb.WriteByte('\n')
	}
	if len(weekend) > 0 {
		sb.WriteString("\nThis weekend:\n")
		for _, e := range weekend {
			sb.WriteString("- ")
			sb.WriteString(eventLine(e))
			sb.WriteByte('\n')
		}
	}

	resp, err := d.llm.Chat(ctx, llm.ChatRequest{
		Category: "background",
		Pass:     "calendar_summary",
		Messages: []llm.Message{
			{Role: "system", Content: calSummarySystemPrompt},
			{Role: "user", Content: sb.String()},
		},
		Temperature:        llm.Temp(0.2),
		MaxTokens:          600,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	text := ""
	if err == nil && resp != nil && len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		text = stripReasoningTags(resp.Choices[0].Message.Content)
		if text == "" {
			text = stripReasoningTags(resp.Choices[0].Message.Reasoning)
		}
	}
	if strings.TrimSpace(text) == "" {
		// Deterministic fallback so the header isn't blank when events exist.
		var parts []string
		for i, e := range windowed {
			if i >= 3 {
				break
			}
			parts = append(parts, eventLine(e))
		}
		return "Upcoming (" + label + "): " + strings.Join(parts, " ; ")
	}
	return strings.TrimSpace(text)
}

// eventStart returns the event's start (canonical_date, set to the start by the
// CalDAV connector), falling back to created_at.
func eventStart(e *store.KnowledgeItem) time.Time {
	if st := store.GetCanonicalDate(e); !st.IsZero() {
		return st
	}
	return e.CreatedAt
}

func eventLine(e *store.KnowledgeItem) string {
	st := eventStart(e)
	allDay, _ := e.Metadata["all_day"].(bool)
	// Include the weekday (computed here, not by the model): small models
	// reliably misderive the day-of-week from a date while writing prose.
	when := st.Format("2006-01-02 Mon 15:04")
	if allDay {
		when = st.Format("2006-01-02 Mon") + " (all day)"
	}
	line := when + " — " + strings.TrimSpace(e.Title)
	if loc, _ := e.Metadata["location"].(string); strings.TrimSpace(loc) != "" {
		line += " — " + strings.TrimSpace(loc)
	}
	return line
}

// weekendHighlights returns upcoming Saturday/Sunday events that look like a
// birthday or party, for the weekend reminder.
func weekendHighlights(week []*store.KnowledgeItem) []*store.KnowledgeItem {
	var out []*store.KnowledgeItem
	for _, e := range week {
		wd := eventStart(e).Weekday()
		if (wd == time.Saturday || wd == time.Sunday) && partyKeywordRe.MatchString(e.Title) {
			out = append(out, e)
		}
	}
	return out
}

func calSummaryCacheKey(windowed, weekend []*store.KnowledgeItem, label string) string {
	h := sha256.New()
	h.Write([]byte(label))
	all := append(append([]*store.KnowledgeItem{}, windowed...), weekend...)
	for _, e := range all {
		fmt.Fprintf(h, "|%s@%d", e.ContentID, eventStart(e).Unix())
	}
	// Day component so phrasing ("tomorrow") stays correct across days.
	fmt.Fprintf(h, "|%s", time.Now().Format("2006-01-02"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
