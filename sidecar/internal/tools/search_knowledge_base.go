package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/retrieval"
)

// dateRangeTopK caps the result count for a date-windowed (aggregation) query.
// Big enough to cover a few months of periodic items (recharges, invoices),
// small enough to keep the context — and the model's reasoning time — bounded.
const dateRangeTopK = 24

// windowedExcerptChars caps each excerpt's length for a date-windowed query.
// Must be long enough to reach the KEY FACTS (amount/total often sit ~2 kB into
// a mail, after the header/metadata block) while trimming the long base64
// signature/footer that otherwise bloats the context. Too short and the model
// reports "amounts not provided"; full bodies make it grind for minutes.
const windowedExcerptChars = 2300

// SearchKnowledgeBaseTool exposes the unified RAG search to the LLM as a
// callable function. The chat path no longer runs retrieval inconditionally —
// the LLM decides per-turn whether to invoke this tool, which means commands
// like "create a note" cost zero RAG round-trips while genuine factual
// questions still get the full pipeline.
type SearchKnowledgeBaseTool struct {
	searcher        *retrieval.UnifiedSearcher
	defaultTopK     int
	minConfidence   float64
	maxContextChars int
}

// NewSearchKnowledgeBaseTool wires the tool to a UnifiedSearcher and the
// retrieval thresholds that previously lived in the RAG handler. Pass
// maxContextTokens=0 to disable the excerpt-budget cap.
func NewSearchKnowledgeBaseTool(searcher *retrieval.UnifiedSearcher, defaultTopK int, minConfidence float64, maxContextTokens int) *SearchKnowledgeBaseTool {
	if defaultTopK <= 0 {
		defaultTopK = 10
	}
	maxChars := 0
	if maxContextTokens > 0 {
		// ~4 chars per token; mirrors the heuristic in the legacy retrieve path.
		maxChars = maxContextTokens * 4
	}
	return &SearchKnowledgeBaseTool{
		searcher:        searcher,
		defaultTopK:     defaultTopK,
		minConfidence:   minConfidence,
		maxContextChars: maxChars,
	}
}

// Name implements Tool.
func (t *SearchKnowledgeBaseTool) Name() string { return "search_knowledge_base" }

// Description implements Tool. Phrased to discourage spurious calls on
// commands (create note, generate image) where retrieval adds no value.
func (t *SearchKnowledgeBaseTool) Description() string {
	return "Search the user's personal knowledge base — emails, documents, notes — for content relevant to the query. Call this only when answering the user requires information that may live in their data (names, facts, past conversations, attachments, dates). Do NOT call for greetings, casual chat, or pure commands like creating a note or generating something."
}

// ParameterSchema implements Tool.
func (t *SearchKnowledgeBaseTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query — phrase it like a search-engine query, keeping the most discriminating keywords (proper nouns, dates, numbers).",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results. Defaults to 10 if omitted.",
			},
			"date_from": map[string]any{
				"type":        "string",
				"description": "Optional ISO date (YYYY-MM-DD). Only return documents on/after this date. For any question scoped to a period (« ces deux derniers mois », « en avril », « récemment »), compute it from today's date and set it — you'll then receive ALL documents in the window, not just the closest matches.",
			},
			"date_to": map[string]any{
				"type":        "string",
				"description": "Optional ISO date (YYYY-MM-DD). Only return documents on/before this date. Pair with date_from to bound a period.",
			},
		},
		"required": []string{"query"},
	}
}

// SearchKnowledgeBaseSource is one entry of the tool result. Mirrors the
// shape of RAGSource in the chat handler so the UI can render the same
// "sources" panel without translation.
type SearchKnowledgeBaseSource struct {
	ContentID   string  `json:"content_id"`
	SourceType  string  `json:"source_type"`
	Title       string  `json:"title,omitempty"`
	Excerpt     string  `json:"excerpt"`
	Score       float64 `json:"score"`
	MailFrom    string  `json:"mail_from,omitempty"`
	MailDate    string  `json:"mail_date,omitempty"`
	MailSubject string  `json:"mail_subject,omitempty"`
	// Date is the document's canonical date (ISO 8601) — for mail, the message
	// date; otherwise created_at. Always populated so the LLM can reason about
	// "when" (recency, relative periods) for every source.
	Date string `json:"date,omitempty"`
	// Structured content signals extracted at ingestion. DueDates are deadlines
	// found IN the document body (e.g. "à payer avant le 25/01/2026"), distinct
	// from MailDate (when the message was received). They let the agent answer
	// deadline / fiscal-period questions ("le prochain paiement TVA 2026") from
	// the content instead of guessing from the received date.
	DueDates []string `json:"due_dates,omitempty"`
	Amounts  []string `json:"amounts,omitempty"`
}

// metaStrings coerces a metadata value (stored as a JSON array, so it comes back
// as []any after a DB round-trip, or a native []string) into a []string,
// skipping non-string elements. Returns nil when the key is absent or empty.
func metaStrings(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

// SearchKnowledgeBaseResult is the JSON the LLM consumes as a tool message.
type SearchKnowledgeBaseResult struct {
	Sources []SearchKnowledgeBaseSource `json:"sources"`
	Total   int                         `json:"total"`
}

type searchKnowledgeBaseArgs struct {
	Query    string `json:"query"`
	TopK     int    `json:"top_k,omitempty"`
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`
}

// Execute implements Tool. Errors are returned to the chat loop, which feeds
// them back to the LLM as a tool-error message so the model can recover
// instead of hanging.
func (t *SearchKnowledgeBaseTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.searcher == nil {
		return nil, fmt.Errorf("search_knowledge_base: searcher unavailable")
	}

	var in searchKnowledgeBaseArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("search_knowledge_base: invalid arguments: %w", err)
	}
	if in.Query == "" {
		return nil, fmt.Errorf("search_knowledge_base: query is required")
	}
	topK := in.TopK
	if topK <= 0 {
		topK = t.defaultTopK
	}

	// Parse an optional date window. Accept full ISO 8601 or a bare date.
	parseDate := func(s string) *time.Time {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02"} {
			if tm, err := time.Parse(layout, s); err == nil {
				u := tm.UTC()
				return &u
			}
		}
		return nil
	}
	dateFrom := parseDate(in.DateFrom)
	dateTo := parseDate(in.DateTo)
	// A period query is an aggregation ("récapitulatif", "liste") over a window,
	// not a top-similarity lookup — return EVERY document in range, not just the
	// closest few. Raise the cap so a small default top_k doesn't silently drop
	// in-window items (the date filter already bounds the set).
	if (dateFrom != nil || dateTo != nil) && topK < dateRangeTopK {
		topK = dateRangeTopK
	}

	resp, err := t.searcher.Search(ctx, retrieval.UnifiedSearchRequest{
		Query:    in.Query,
		TopK:     topK,
		DateFrom: dateFrom,
		DateTo:   dateTo,
	})
	if err != nil {
		return nil, fmt.Errorf("search_knowledge_base: %w", err)
	}

	// A windowed (aggregation) query returns MANY documents; keeping full bodies
	// blows the context up and makes the model grind for minutes (→ the SSE drops
	// = "Load failed"). The LLM only needs each item's date + key facts to
	// recap/total, so cap each excerpt short when a window is set.
	windowed := dateFrom != nil || dateTo != nil
	result := SearchKnowledgeBaseResult{}
	var totalChars int
	for _, r := range resp.Results {
		if r.Score < t.minConfidence {
			continue
		}
		excerpt := r.Excerpt
		if windowed && len(excerpt) > windowedExcerptChars {
			excerpt = excerpt[:windowedExcerptChars]
		}
		if t.maxContextChars > 0 && totalChars+len(excerpt) > t.maxContextChars {
			break
		}
		result.Sources = append(result.Sources, SearchKnowledgeBaseSource{
			ContentID:   r.ContentID,
			SourceType:  r.SourceType,
			Title:       r.Title,
			Excerpt:     excerpt,
			Score:       r.Score,
			MailFrom:    r.MailFrom,
			MailDate:    r.MailDate,
			MailSubject: r.MailSubject,
			Date:        r.Date,
			DueDates:    metaStrings(r.Metadata, "extracted_due_dates"),
			Amounts:     metaStrings(r.Metadata, "extracted_amounts"),
		})
		totalChars += len(excerpt)
	}

	// For a date-windowed query (an aggregation over a period), return the
	// sources in chronological order. Models tend to follow the input order, so
	// this reliably yields a chronologically-ordered recap/table — far more
	// dependable than a prompt instruction alone. ISO-8601 dates sort lexically =
	// chronologically; undated items go last. Non-windowed queries keep score
	// order (relevance ranking).
	if dateFrom != nil || dateTo != nil {
		sort.SliceStable(result.Sources, func(i, j int) bool {
			di, dj := result.Sources[i].Date, result.Sources[j].Date
			if di == "" {
				return false
			}
			if dj == "" {
				return true
			}
			return di < dj
		})
	}

	result.Total = len(result.Sources)

	return json.Marshal(result)
}
