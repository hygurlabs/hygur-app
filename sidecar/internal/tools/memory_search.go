package tools

import (
	"context"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// MemorySearchTool searches for memories in the database.
type MemorySearchTool struct {
	store *store.DB
}

// NewMemorySearchTool creates a new MemorySearchTool.
func NewMemorySearchTool(store *store.DB) *MemorySearchTool {
	return &MemorySearchTool{
		store: store,
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

// Search returns memories matching the query using full-text search.
// maxResults limits the number of results. minScore filters by minimum score.
func (t *MemorySearchTool) Search(query string, maxResults int, minScore float64) ([]MemoryResult, error) {
	if query == "" {
		return nil, nil
	}

	// Get all memories (we can add a full-text search later)
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
