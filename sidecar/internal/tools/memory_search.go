package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// MemorySearchTool searches for memories in the database. Phase 3.3 adds
// embedding-cosine ranking over accepted memories on top of the legacy LIKE
// search the older /memory/search endpoint exposes.
type MemorySearchTool struct {
	// Read-only: embeds NoSideEffect so it is never gated by the confirmation flow.
	NoSideEffect
	store *store.DB
	llm   *llm.Client
}

// NewMemorySearchTool creates a new MemorySearchTool. The llm client is
// optional: when nil, SearchAccepted falls back to recency-sorted accepted
// memories (still gated by accepted_at) so chat injection keeps working even
// if the embedding endpoint is unavailable.
func NewMemorySearchTool(store *store.DB, llmClient *llm.Client) *MemorySearchTool {
	return &MemorySearchTool{
		store: store,
		llm:   llmClient,
	}
}

// MemoryResult represents a single search result.
type MemoryResult struct {
	MemoryID  string
	Type      store.MemoryType
	Content   string
	Score     float64
	ContextID string
	CreatedAt time.Time
}

// Search returns memories matching the query using substring search.
// Kept as the legacy /memory/search backend; new chat injection should use
// SearchAccepted instead.
func (t *MemorySearchTool) Search(query string, maxResults int, minScore float64) ([]MemoryResult, error) {
	if query == "" {
		return nil, nil
	}

	memories, err := t.store.SearchMemories(context.Background(), query, maxResults)
	if err != nil {
		return nil, err
	}

	var results []MemoryResult
	for _, m := range memories {
		results = append(results, MemoryResult{
			MemoryID:  m.MemoryID,
			Type:      m.Type,
			Content:   m.Content,
			Score:     m.Score,
			ContextID: m.ContextID,
			CreatedAt: m.CreatedAt,
		})
	}

	return results, nil
}

// SearchAccepted ranks accepted memories by cosine similarity to the query and
// returns the top-K, capped by `tokenBudget` (rough 4-chars-per-token estimate).
// When the embedding client is unavailable (or every memory lacks an
// embedding), it falls back to most-recent-first over the same accepted set so
// the injection still adds value rather than silently disappearing.
//
// Why not reuse the legacy LIKE Search? Two reasons:
//  1. Phase 3.3 mandates cosine retrieval over an embedding index.
//  2. Chat injection must skip pending candidates; the legacy search returns
//     every memory regardless of acceptance state.
func (t *MemorySearchTool) SearchAccepted(ctx context.Context, query string, topK, tokenBudget int) ([]MemoryResult, error) {
	if query == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}
	memories, err := t.store.ListAcceptedMemories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accepted memories: %w", err)
	}
	if len(memories) == 0 {
		return nil, nil
	}

	// Try embedding-based ranking first.
	var queryVec []float32
	if t.llm != nil {
		if vec, embedErr := t.llm.GenerateEmbedding(ctx, query); embedErr == nil {
			queryVec = vec
		}
	}

	type ranked struct {
		mem   *store.Memory
		score float64
	}
	candidates := make([]ranked, 0, len(memories))
	if len(queryVec) > 0 {
		for _, m := range memories {
			if len(m.Embedding) != len(queryVec) {
				// Memories without an embedding (legacy rows) keep a 0 score
				// so they only surface when no other candidate matches.
				candidates = append(candidates, ranked{mem: m, score: 0})
				continue
			}
			candidates = append(candidates, ranked{
				mem:   m,
				score: cosine(queryVec, m.Embedding),
			})
		}
	} else {
		// Fallback: rank by recency so the top-K still reflects "what the
		// user just told us" rather than a deterministic but arbitrary order.
		for _, m := range memories {
			candidates = append(candidates, ranked{mem: m, score: float64(m.CreatedAt.Unix())})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	out := make([]MemoryResult, 0, topK)
	totalChars := 0
	maxChars := tokenBudget * 4 // ≈ 4 chars/token (conservative)
	for _, c := range candidates {
		if len(out) >= topK {
			break
		}
		// Drop near-zero similarity hits when we have a real embedding query;
		// otherwise the prompt fills with irrelevant facts. Threshold tuned
		// empirically — 0.2 keeps loose-but-related hits, drops noise.
		if len(queryVec) > 0 && c.score < 0.2 {
			continue
		}
		contentLen := len(strings.TrimSpace(c.mem.Content))
		if maxChars > 0 && totalChars+contentLen > maxChars {
			break
		}
		totalChars += contentLen
		out = append(out, MemoryResult{
			MemoryID:  c.mem.MemoryID,
			Type:      c.mem.Type,
			Content:   c.mem.Content,
			Score:     c.score,
			ContextID: c.mem.ContextID,
			CreatedAt: c.mem.CreatedAt,
		})
	}
	return out, nil
}

// --- LLM tool adapter (read-only) -------------------------------------------
// recall_memory lets the chat path deliberately re-query the user's accepted
// memories with a focused term — beyond the top-K already pre-injected each turn
// — to answer "what do you remember about X?". Read-only.

const recallMemoryTokenBudget = 1200

// Name implements tools.Tool.
func (t *MemorySearchTool) Name() string { return "recall_memory" }

// Description implements tools.Tool.
func (t *MemorySearchTool) Description() string {
	return "Recall what Hygur remembers about the user (facts, preferences, recurring actions) matching a query."
}

// ParameterSchema implements tools.Tool.
func (t *MemorySearchTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What to recall about the user.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum memories to return (default 8).",
			},
		},
		"required": []string{"query"},
	}
}

// Execute implements tools.Tool: focused recall over accepted memories. Returns
// the matching memories without touching the store.
func (t *MemorySearchTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	topK := req.MaxResults
	if topK <= 0 || topK > 25 {
		topK = 8
	}
	hits, err := t.SearchAccepted(ctx, req.Query, topK, recallMemoryTokenBudget)
	if err != nil {
		return nil, fmt.Errorf("recall failed: %w", err)
	}
	type memOut struct {
		Content string  `json:"content"`
		Type    string  `json:"type"`
		Score   float64 `json:"score"`
	}
	out := make([]memOut, 0, len(hits))
	for _, m := range hits {
		out = append(out, memOut{Content: m.Content, Type: string(m.Type), Score: m.Score})
	}
	return json.Marshal(map[string]any{"memories": out})
}

// cosine computes cosine similarity between two equally-sized float32 vectors.
// Mirror of store.cosineSimilarity (unexported there) — duplicated rather
// than refactored upward to keep the tool layer free of store-internal
// helpers.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
