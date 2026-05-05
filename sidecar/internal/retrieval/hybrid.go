// Package retrieval provides semantic (vector-only) search capabilities.
package retrieval

import (
	"context"
	"errors"
	"fmt"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// ErrLLMClientRequired is returned when a vector search is attempted without an LLM client.
var ErrLLMClientRequired = errors.New("LLM client is required for vector search")

// Default search configuration values.
const (
	DefaultTopK       = 10
	DefaultMultiplier = 10
	MaxExcerptLength  = 500
)

// SearchOptions configures a search request.
// The Mode and Sources fields are accepted but ignored — all searches are semantic (vector).
type SearchOptions struct {
	TopK      int     // Number of results to return (default 10)
	ProjectID *string // Filter by project (optional, not implemented yet)
	// Deprecated: all searches are now vector/semantic. Accepted for API compat.
	Mode    string
	Sources []string
}

// SearchResult represents a single search result.
type SearchResult struct {
	ChunkID   string         // Unique identifier of the chunk
	ContentID string         // Parent content identifier
	Score     float64        // Cosine similarity score
	Excerpt   string         // Text excerpt from the chunk
	Metadata  map[string]any // Chunk metadata
	Source    string         // Always "vector"
}

// SemanticSearcher performs vector-only similarity search.
// The type alias HybridSearcher is kept for one release to avoid breaking existing callers.
type SemanticSearcher struct {
	store *store.DB
	llm   *llm.Client
}

// HybridSearcher is an alias for SemanticSearcher kept for backward compatibility.
type HybridSearcher = SemanticSearcher

// NewHybridSearcher creates a new SemanticSearcher instance.
func NewHybridSearcher(s *store.DB, l *llm.Client) *SemanticSearcher {
	return &SemanticSearcher{store: s, llm: l}
}

// Search performs a semantic (vector) search.
func (hs *SemanticSearcher) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if query == "" {
		return []SearchResult{}, nil
	}
	if opts.TopK <= 0 {
		opts.TopK = DefaultTopK
	}
	return hs.searchVector(ctx, query, opts)
}

func (hs *SemanticSearcher) searchVector(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if hs.llm == nil {
		return nil, ErrLLMClientRequired
	}

	embedding, err := hs.llm.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	vecResults, err := hs.store.SearchChunksVec(ctx, embedding, opts.TopK)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	results := make([]SearchResult, len(vecResults))
	for i, r := range vecResults {
		results[i] = SearchResult{
			ChunkID:   r.ChunkID,
			ContentID: r.ContentID,
			Score:     r.Score,
			Source:    "vector",
		}
		chunk, err := hs.store.GetChunk(ctx, r.ChunkID)
		if err == nil && chunk != nil {
			results[i].Excerpt = extractExcerpt(chunk.Text, MaxExcerptLength)
			results[i].Metadata = chunk.Metadata
		}
	}
	return results, nil
}

// extractExcerpt truncates text to maxLen characters.
func extractExcerpt(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
