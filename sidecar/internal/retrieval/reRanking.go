package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// rerankTimeout caps the LLM rerank call. Reasoning-capable backends (Nemotron,
// Qwen-think) can stream hundreds of `reasoning_content` tokens before the
// answer arrives — without a timeout the chat hangs indefinitely waiting on a
// post-retrieval refinement that the user hasn't even seen UI for. Matches
// judgeTimeout so the two LLM filters share the same worst-case budget.
const rerankTimeout = 30 * time.Second

// Rerank re-orders unified search results by relevance using an LLM.
// It groups chunks by document, builds an LLM prompt with all chunks,
// and returns the document IDs in the LLM-reordered list.
func (us *UnifiedSearcher) Rerank(ctx context.Context, query string, results []UnifiedResult) ([]string, error) {
	if len(results) == 0 {
		return nil, nil
	}

	// Group chunks by ContentID (document)
	docChunks := make(map[string][]UnifiedResult)
	keep := make(map[string]bool)
	for _, r := range results {
		keep[r.ContentID] = true
		docChunks[r.ContentID] = append(docChunks[r.ContentID], r)
	}

	// Limit number of documents for LLM context
	if len(docChunks) > 20 {
		// Take top 20 by first occurrence order
		var ordered []string
		for cid := range keep {
			ordered = append(ordered, cid)
		}
		// Sort by score to keep the best
		type cscore struct {
			id string
			sc float64
		}
		var scores []cscore
		for cid, chunks := range docChunks {
			scores = append(scores, cscore{cid, chunks[0].Score})
		}
		sort.Slice(scores, func(i, j int) bool {
			return scores[i].sc > scores[j].sc
		})
		for _, s := range scores[:20] {
			keep[s.id] = true
		}
		// Reset docChunks to only keep top 20
		temp := make(map[string][]UnifiedResult)
		for cid, chunks := range docChunks {
			if keep[cid] {
				temp[cid] = chunks
			}
		}
		docChunks = temp
	}

	// Dedicated reranker (cross-encoder) when configured — cheaper + better than
	// the LLM-as-judge path below, and it keeps reranking off the chat-token budget.
	if us.llm != nil && us.llm.RerankConfigured() {
		return us.rerankDedicated(ctx, query, docChunks)
	}

	// LLM-as-judge fallback: no dedicated /rerank endpoint is configured, so the
	// only way to rerank is an uncapped LLM chat call per query — a cost/DoS risk.
	// Gated OFF by default (retrieval.llm_rerank_fallback); when disabled we skip
	// the LLM entirely and return the documents in their original relevance order.
	if !us.useLLMRerankFallback {
		seen := make(map[string]bool)
		var orderedContentIDs []string
		for _, r := range results {
			if _, kept := docChunks[r.ContentID]; !kept || seen[r.ContentID] {
				continue
			}
			seen[r.ContentID] = true
			orderedContentIDs = append(orderedContentIDs, r.ContentID)
		}
		return orderedContentIDs, nil
	}

	var sb strings.Builder
	sb.WriteString("You are a relevance ranking assistant. You will receive a query and several text chunks. Rank them by relevance to the query.\n\n")
	fmt.Fprintf(&sb, "Query: %s\n\n", query)

	var chunkIDs []string
	chunkMap := make(map[string]string) // chunkID -> contentID
	var idx int
	for cid, chunks := range docChunks {
		for _, c := range chunks {
			chunkMap[c.ChunkID] = cid
			sb.WriteString(fmt.Sprintf("Chunk %d: [%s] %s\n\n", idx+1, c.Title, c.Excerpt))
			chunkIDs = append(chunkIDs, c.ChunkID)
			idx++
		}
	}

	sb.WriteString("Return a JSON array of chunk IDs in order of relevance (most relevant first). Example: [\"chunk1\", \"chunk2\", \"chunk3\"]")

	// Bound the rerank call so a slow reasoning model can't hold the chat
	// pipeline open. On timeout the caller falls back to the original order.
	rctx, cancel := context.WithTimeout(ctx, rerankTimeout)
	defer cancel()

	resp, err := us.llm.Chat(rctx, llm.ChatRequest{
		Messages:    []llm.Message{{Role: "user", Content: sb.String()}},
		Stream:      false,
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call LLM for re-ranking: %w", err)
	}

	// Parse response
	order := parseRerankResponse(resp, chunkMap)

	// Convert ordered chunk IDs to content IDs (unique, in order)
	seen := make(map[string]bool)
	var orderedContentIDs []string
	for _, cid := range order {
		contentID := chunkMap[cid]
		if !seen[contentID] {
			seen[contentID] = true
			orderedContentIDs = append(orderedContentIDs, contentID)
		}
	}

	return orderedContentIDs, nil
}

// parseRerankResponse parses the LLM response for re-ranking.
// Returns an ordered list of chunk IDs.
func parseRerankResponse(resp *llm.ChatResponse, chunkMap map[string]string) []string {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil
	}

	text := strings.TrimSpace(resp.Choices[0].Message.Content)
	if text == "" {
		// Reasoning-capable backends may route the answer to the reasoning
		// field instead of content. Fall back so the rerank still works.
		text = strings.TrimSpace(resp.Choices[0].Message.Reasoning)
	}

	// Try to parse as JSON array
	if strings.HasPrefix(text, "[") {
		// Extract the array from text (might have surrounding text)
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start >= 0 && end > start {
			jsonStr := text[start : end+1]
			var ids []string
			if err := json.Unmarshal([]byte(jsonStr), &ids); err == nil && len(ids) > 0 {
				return ids
			}
		}
	}

	// Fallback: try comma-separated
	var prefixes = []string{"[", ""}
	var suffixes = []string{"]", ""}
	for _, prefix := range prefixes {
		for _, suffix := range suffixes {
			s := strings.TrimPrefix(strings.TrimSuffix(text, suffix), prefix)
			parts := strings.Split(s, ",")
			if len(parts) > 0 && len(parts[0]) > 0 && len(parts[0]) < 100 {
				var ids []string
				for _, p := range parts {
					ids = append(ids, strings.TrimSpace(p))
				}
				return ids
			}
		}
	}

	return nil
}

// rerankDedicated reranks via the configured dedicated reranker (Cohere-shaped
// /rerank, e.g. Infomaniak's bge-reranker-v2-m3): one document per content id
// (title + best chunk), returning content ids most-relevant-first. The cid order
// is stabilised (map iteration is random) so the reranker's index→cid mapping is
// deterministic.
func (us *UnifiedSearcher) rerankDedicated(ctx context.Context, query string, docChunks map[string][]UnifiedResult) ([]string, error) {
	cids := make([]string, 0, len(docChunks))
	for cid := range docChunks {
		cids = append(cids, cid)
	}
	sort.Strings(cids)
	texts := make([]string, len(cids))
	for i, cid := range cids {
		c := docChunks[cid][0]
		texts[i] = strings.TrimSpace(c.Title + "\n" + c.Excerpt)
	}
	rctx, cancel := context.WithTimeout(ctx, rerankTimeout)
	defer cancel()
	order, err := us.llm.Rerank(rctx, query, texts, 0)
	if err != nil {
		return nil, fmt.Errorf("dedicated rerank failed: %w", err)
	}
	out := make([]string, 0, len(order))
	for _, i := range order {
		if i >= 0 && i < len(cids) {
			out = append(out, cids[i])
		}
	}
	return out, nil
}
