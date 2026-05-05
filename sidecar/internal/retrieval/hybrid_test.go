package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

func TestExtractExcerpt(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected string
	}{
		{"short text", "Hello world", 100, "Hello world"},
		{"exact length", "Hello", 5, "Hello"},
		{"truncated", "Hello world, this is a long text", 10, "Hello worl..."},
		{"empty", "", 100, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractExcerpt(tt.text, tt.maxLen)
			if got != tt.expected {
				t.Errorf("extractExcerpt(%q, %d) = %q, want %q", tt.text, tt.maxLen, got, tt.expected)
			}
		})
	}
}

func TestSemanticSearcher_EmptyQuery(t *testing.T) {
	hs := &SemanticSearcher{}
	results, err := hs.Search(context.Background(), "", SearchOptions{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestSemanticSearcher_RequiresLLM(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer db.Close()

	hs := NewHybridSearcher(db, nil)
	_, err = hs.Search(context.Background(), "machine learning", SearchOptions{TopK: 10})
	if err == nil {
		t.Error("expected error when LLM client is nil")
	}
	if err != ErrLLMClientRequired {
		t.Errorf("expected ErrLLMClientRequired, got %v", err)
	}
}

func TestNewHybridSearcher(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer db.Close()

	hs := NewHybridSearcher(db, nil)
	if hs == nil {
		t.Error("NewHybridSearcher returned nil")
	}
	if hs.store != db {
		t.Error("store not properly assigned")
	}
}

func TestSearchResultFields(t *testing.T) {
	result := SearchResult{
		ChunkID:   "chunk-1",
		ContentID: "content-1",
		Score:     0.95,
		Excerpt:   "Test excerpt",
		Metadata:  map[string]any{"key": "value"},
		Source:    "vector",
	}

	if result.Source != "vector" {
		t.Errorf("Source = %s, want 'vector'", result.Source)
	}
	if result.Score != 0.95 {
		t.Errorf("Score = %f, want 0.95", result.Score)
	}
}

func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return db
}

func insertTestChunks(t *testing.T, db *store.DB, chunks []*store.Chunk) {
	t.Helper()
	ctx := context.Background()
	ki := &store.KnowledgeItem{
		ContentID:      "test-content-1",
		SourceType:     "test",
		Title:          "Test Document",
		NormalizedText: "Test document content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(ctx, ki); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}
	for _, chunk := range chunks {
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
	}
}
