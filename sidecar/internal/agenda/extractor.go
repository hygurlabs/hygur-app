// Package agenda provides extraction of upcoming action items and deadlines
// from knowledge items, enabling proactive scheduling context in chat.
package agenda

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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
				templated = append(templated, AgendaAction{
					What:        item.Title,
					DeadlineISO: d,
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

	// Build a compact context block for the LLM.
	var sb strings.Builder
	for _, item := range items {
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
		MaxTokens:   80,
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

	var results []llmActionResult
	if err := json.Unmarshal([]byte(content), &results); err != nil {
		return nil, fmt.Errorf("parse LLM JSON: %w", err)
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
