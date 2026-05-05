// Package tools provides MCP tools for Hygur sidecar.
package tools

import (
	"context"

	"github.com/hygur/sidecar/internal/retrieval"
)

// SearchTool performs global search across all knowledge items using semantic search.
type SearchTool struct {
	searcher *retrieval.SemanticSearcher
}

// NewSearchTool creates a new SearchTool with the given SemanticSearcher.
func NewSearchTool(searcher *retrieval.SemanticSearcher) *SearchTool {
	return &SearchTool{
		searcher: searcher,
	}
}

// Run performs a global semantic search (no project filter) with the given query and topK.
// If topK is 0 or negative, the default (10) is used.
func (t *SearchTool) Run(ctx context.Context, query string, topK int) ([]retrieval.SearchResult, error) {
	opts := retrieval.SearchOptions{
		TopK:      topK,
		ProjectID: nil,
	}

	return t.searcher.Search(ctx, query, opts)
}
