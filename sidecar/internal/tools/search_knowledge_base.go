package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hygur/sidecar/internal/retrieval"
)

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
}

// SearchKnowledgeBaseResult is the JSON the LLM consumes as a tool message.
type SearchKnowledgeBaseResult struct {
	Sources []SearchKnowledgeBaseSource `json:"sources"`
	Total   int                         `json:"total"`
}

type searchKnowledgeBaseArgs struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
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

	resp, err := t.searcher.Search(ctx, retrieval.UnifiedSearchRequest{
		Query: in.Query,
		TopK:  topK,
	})
	if err != nil {
		return nil, fmt.Errorf("search_knowledge_base: %w", err)
	}

	result := SearchKnowledgeBaseResult{}
	var totalChars int
	for _, r := range resp.Results {
		if r.Score < t.minConfidence {
			continue
		}
		if t.maxContextChars > 0 && totalChars+len(r.Excerpt) > t.maxContextChars {
			break
		}
		result.Sources = append(result.Sources, SearchKnowledgeBaseSource{
			ContentID:   r.ContentID,
			SourceType:  r.SourceType,
			Title:       r.Title,
			Excerpt:     r.Excerpt,
			Score:       r.Score,
			MailFrom:    r.MailFrom,
			MailDate:    r.MailDate,
			MailSubject: r.MailSubject,
		})
		totalChars += len(r.Excerpt)
	}
	result.Total = len(result.Sources)

	return json.Marshal(result)
}
