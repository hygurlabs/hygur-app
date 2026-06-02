// Package agenda provides extraction of upcoming action items and deadlines
// from knowledge items, enabling proactive scheduling context in chat.
package agenda

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// AgendaAction represents an extracted action or deadline.
type AgendaAction struct {
	What        string
	DeadlineISO string // "2026-05-10"
	Priority    string // "high" | "medium" | "low"
	SourceID    string
	Confidence  float64
}

// marketingTopics lists extracted_topics values that signal low-signal content.
var marketingTopics = []string{"newsletter", "marketing", "spam", "promotion", "publicité"}

// Extractor extracts agenda actions from knowledge items.
type Extractor struct {
	llm   *llm.Client
	cache *agendaCache
}

// NewExtractor creates an Extractor backed by the given LLM client.
func NewExtractor(llmClient *llm.Client) *Extractor {
	return &Extractor{
		llm:   llmClient,
		cache: newAgendaCache(),
	}
}

// ExtractActions extracts upcoming actions from the given knowledge items.
// Priority extraction order:
//  1. Templated: items with extracted_due_dates in Metadata (no LLM cost).
//  2. LLM fallback: for items without due dates, ask the LLM (confidence ≥ 0.7).
//
// Fail-soft: LLM errors are logged and the templated actions are returned.
func (e *Extractor) ExtractActions(ctx context.Context, items []store.KnowledgeItem) ([]AgendaAction, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// Build cache key from sorted content IDs.
	cacheKey := buildCacheKey(items)
	if cached, ok := e.cache.get(cacheKey); ok {
		return cached, nil
	}

	var templated []AgendaAction
	var noDateItems []store.KnowledgeItem

	for _, item := range items {
		if isMarketingItem(item) {
			continue
		}

		dueDates := extractDueDates(item)
		if len(dueDates) > 0 {
			priority := extractPriority(item)
			for _, d := range dueDates {
				// Tier-1 stores raw regex captures ("25 avril 2026",
				// "25/04/2026", "25 April 2026"). Normalise to ISO before the
				// past-deadline filter — the filter is a lex string compare
				// against today's ISO date, so unnormalised values like
				// "30/04/2025" silently pass it ('3' > '2'). Drop entries we
				// can't parse rather than risk surfacing them as upcoming.
				iso, ok := normalizeToISO(d)
				if !ok {
					continue
				}
				templated = append(templated, AgendaAction{
					What:        item.Title,
					DeadlineISO: iso,
					Priority:    priority,
					SourceID:    item.ContentID,
					Confidence:  1.0,
				})
			}
		} else {
			noDateItems = append(noDateItems, item)
		}
	}

	// LLM fallback for items without extracted due dates.
	llmActions, err := e.extractViaLLM(ctx, noDateItems)
	if err != nil {
		slog.WarnContext(ctx, "agenda LLM extraction failed, using templated only", "err", err)
	} else {
		templated = append(templated, llmActions...)
	}

	// Drop deadlines that have already passed. ListRecentItems returns items
	// based on DB activity (recent updates, re-indexing) which has nothing to
	// do with how far in the future the deadline lies — without this filter,
	// a freshly re-indexed email referencing a year-old date still surfaces
	// as an "upcoming" action.
	todayISO := time.Now().Format("2006-01-02")
	upcoming := templated[:0]
	for _, a := range templated {
		if a.DeadlineISO >= todayISO {
			upcoming = append(upcoming, a)
		}
	}
	templated = upcoming

	// Sort by deadline ascending.
	sort.Slice(templated, func(i, j int) bool {
		return templated[i].DeadlineISO < templated[j].DeadlineISO
	})

	e.cache.set(cacheKey, templated)
	return templated, nil
}

// isMarketingItem returns true if the item's topics contain marketing/spam signals.
func isMarketingItem(item store.KnowledgeItem) bool {
	topics, ok := item.Metadata["extracted_topics"]
	if !ok {
		return false
	}

	var topicList []string
	switch v := topics.(type) {
	case []string:
		topicList = v
	case []interface{}:
		for _, t := range v {
			if s, ok := t.(string); ok {
				topicList = append(topicList, s)
			}
		}
	case string:
		topicList = []string{v}
	default:
		return false
	}

	for _, topic := range topicList {
		lower := strings.ToLower(topic)
		for _, bad := range marketingTopics {
			if strings.Contains(lower, bad) {
				return true
			}
		}
	}
	return false
}

// extractDueDates reads extracted_due_dates from item Metadata.
func extractDueDates(item store.KnowledgeItem) []string {
	val, ok := item.Metadata["extracted_due_dates"]
	if !ok {
		return nil
	}

	switch v := val.(type) {
	case []string:
		return filterNonEmpty(v)
	case []interface{}:
		var out []string
		for _, s := range v {
			if str, ok := s.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

// extractPriority reads priority from item Metadata, defaulting to "medium".
func extractPriority(item store.KnowledgeItem) string {
	if p, ok := item.Metadata["priority"].(string); ok && p != "" {
		return p
	}
	return "medium"
}

// llmActionResult mirrors the JSON the LLM is asked to produce.
type llmActionResult struct {
	What        string  `json:"what"`
	DeadlineISO string  `json:"deadline_iso"`
	Priority    string  `json:"priority"`
	Confidence  float64 `json:"confidence"`
}

// extractViaLLM calls the LLM for items that have no pre-extracted due dates.
func (e *Extractor) extractViaLLM(ctx context.Context, items []store.KnowledgeItem) ([]AgendaAction, error) {
	if len(items) == 0 || e.llm == nil {
		return nil, nil
	}

	// Build a compact context block for the LLM, bounded so the prompt fits the
	// small indexing model's context window (minicpm5: 16384 tokens). Without
	// this cap a large batch of date-less items overflowed it (400 "maximum
	// context length"). ~12k chars ≈ well under 16k tokens, leaving room for the
	// system prompt + output. Items beyond the cap still get templated dates.
	const (
		maxItems        = 40
		maxContextChars = 12000
	)
	var sb strings.Builder
	for i, item := range items {
		if i >= maxItems || sb.Len() >= maxContextChars {
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", item.ContentID, item.Title))
		if len(item.NormalizedText) > 200 {
			sb.WriteString(item.NormalizedText[:200])
		} else {
			sb.WriteString(item.NormalizedText)
		}
		sb.WriteString("\n\n")
	}

	systemPrompt := `/no_think
Tu es un assistant d'extraction d'agenda. Réponds UNIQUEMENT en JSON valide, sans texte additionnel.
Extrait les actions avec deadline depuis les documents. Format:
[{"what":"description courte","deadline_iso":"YYYY-MM-DD","priority":"high|medium|low","confidence":0.0-1.0}]
Si aucune deadline n'est trouvée, retourne [].`

	userPrompt := fmt.Sprintf("Documents:\n%s\n\nExtrait les actions avec deadline (max 80 tokens).", sb.String())

	resp, err := e.llm.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:      false,
		Temperature: 0,
		// Reasoning models burn the whole budget inside <think> and never emit
		// JSON if MaxTokens is tiny — give room for a short array, and disable
		// thinking outright (the /no_think hint alone isn't honored by nemotron).
		MaxTokens:          512,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM chat: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, fmt.Errorf("empty LLM response")
	}

	// Parse JSON from response.
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	// Strip any markdown code fences if present.
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		var jsonLines []string
		for _, l := range lines {
			if strings.HasPrefix(l, "```") {
				continue
			}
			jsonLines = append(jsonLines, l)
		}
		content = strings.Join(jsonLines, "\n")
	}

	// Reasoning models (nemotron, Qwen3) may still wrap the answer in a
	// <think>…</think> block and/or surround it with prose despite enable_thinking
	// being off; slice down to the JSON array. An empty result means the model
	// found nothing actionable — a valid "no deadlines" answer, not a failure —
	// so the caller keeps the templated actions without logging a warning.
	content = extractJSONArray(content)
	if content == "" {
		return nil, nil
	}

	var results []llmActionResult
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return nil, fmt.Errorf("parse LLM JSON: %w (raw=%q)", err, content)
	}

	// Build a source map from title to content_id.
	titleToID := make(map[string]string, len(items))
	for _, item := range items {
		titleToID[item.Title] = item.ContentID
	}

	var actions []AgendaAction
	for _, r := range results {
		if r.Confidence < 0.7 || r.DeadlineISO == "" {
			continue
		}
		// Normalize priority.
		priority := r.Priority
		if priority != "high" && priority != "medium" && priority != "low" {
			priority = "medium"
		}
		// Try to match source.
		sourceID := titleToID[r.What]
		actions = append(actions, AgendaAction{
			What:        r.What,
			DeadlineISO: r.DeadlineISO,
			Priority:    priority,
			SourceID:    sourceID,
			Confidence:  r.Confidence,
		})
	}
	return actions, nil
}

// frenchMonths / englishMonths map locale-specific names (lowercased,
// accent-stripped for FR) to their month number. Tier-1 captures raw text
// like "25 avril 2026" or "25 April 2026" — we keep the lookups separate so a
// future locale addition is just a new map.
var frenchMonths = map[string]int{
	"janvier": 1, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6,
	"juillet": 7, "aout": 8, "septembre": 9, "octobre": 10, "novembre": 11, "decembre": 12,
}

var englishMonths = map[string]int{
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
	"july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
}

// reNumericDate matches DD/MM/YYYY, DD-MM-YYYY, DD.MM.YYYY (FR convention).
// Anchored so the whole input must be a date.
var reNumericDate = regexp.MustCompile(`^\s*(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{4})\s*$`)

// reISODate matches YYYY-MM-DD — used to short-circuit when the LLM (or the
// occasional machine-readable mail header) already produced ISO.
var reISODate = regexp.MustCompile(`^\s*(\d{4})-(\d{1,2})-(\d{1,2})\s*$`)

// reTextualDate matches "DD <month-name> YYYY" with the month name being a FR
// or EN word. Stripped of accents and lowercased before lookup.
var reTextualDate = regexp.MustCompile(`(?i)^\s*(\d{1,2})\s+([\p{L}.]+)\s+(\d{4})\s*$`)

// extractJSONArray pulls the JSON array out of a model completion, tolerating
// reasoning-model noise: a leading/trailing <think>…</think> block (closed or
// not) and surrounding prose. It returns "" when no array is present (treated
// by the caller as "no actionable deadlines", not an error).
func extractJSONArray(s string) string {
	if i := strings.LastIndex(s, "</think>"); i != -1 {
		s = s[i+len("</think>"):]
	}
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return strings.TrimSpace(s[start : end+1])
}

// normalizeToISO converts the raw deadline strings produced by tier-1 (FR/EN
// month names, DD/MM/YYYY, DD-MM-YYYY) and by the LLM (already ISO) into a
// canonical "YYYY-MM-DD". Returns ok=false when the input doesn't match any
// known shape — the caller drops these rather than surfacing untrustworthy
// dates.
func normalizeToISO(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}

	// Already ISO — validate calendar bounds and re-emit zero-padded.
	if m := reISODate.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		return composeISO(y, mo, d)
	}

	// DD/MM/YYYY family.
	if m := reNumericDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		return composeISO(y, mo, d)
	}

	// "DD <month> YYYY" — try FR then EN.
	if m := reTextualDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		monthKey := stripAccents(strings.ToLower(strings.TrimSuffix(m[2], ".")))
		y, _ := strconv.Atoi(m[3])
		if mo, ok := frenchMonths[monthKey]; ok {
			return composeISO(y, mo, d)
		}
		if mo, ok := englishMonths[monthKey]; ok {
			return composeISO(y, mo, d)
		}
	}

	return "", false
}

// composeISO validates the calendar fields and returns "YYYY-MM-DD". time.Date
// would silently normalise impossible inputs (e.g. month=13 → next January);
// we do the bounds check ourselves so callers can drop garbage.
func composeISO(y, mo, d int) (string, bool) {
	if y < 1900 || y > 2999 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return "", false
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

// stripAccents removes the few diacritics tier-1 captures leak into month
// names ("février" → "fevrier", "août" → "aout"). Targeted replacement is
// faster and clearer than dragging in golang.org/x/text just for this.
func stripAccents(s string) string {
	r := strings.NewReplacer(
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"à", "a", "â", "a", "ä", "a",
		"î", "i", "ï", "i",
		"ô", "o", "ö", "o",
		"ù", "u", "û", "u", "ü", "u",
		"ç", "c",
	)
	return r.Replace(s)
}

// buildCacheKey produces a sha1 hex of sorted content IDs.
func buildCacheKey(items []store.KnowledgeItem) string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ContentID
	}
	sort.Strings(ids)
	h := sha1.New()
	h.Write([]byte(strings.Join(ids, ",")))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func filterNonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// agendaCache is a thread-safe in-memory TTL cache keyed by sha1(content_ids).
type agendaCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	actions   []AgendaAction
	expiresAt time.Time
}

func newAgendaCache() *agendaCache {
	return &agendaCache{
		entries: make(map[string]cacheEntry),
		ttl:     time.Hour,
	}
}

func (c *agendaCache) get(key string) ([]AgendaAction, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return e.actions, true
}

func (c *agendaCache) set(key string, actions []AgendaAction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{
		actions:   actions,
		expiresAt: time.Now().Add(c.ttl),
	}
}
