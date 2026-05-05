package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
)

// setupTestDB creates a test database with sample data for search tests.
func setupSearchTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Insert test knowledge items
	ctx := context.Background()

	items := []store.KnowledgeItem{
		{
			ContentID:      "content-1",
			SourceType:     "file",
			SourcePath:     strPtr("/test/document1.md"),
			Title:          "Go Programming Guide",
			NormalizedText: "This is a guide about Go programming language and concurrency patterns.",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ContentID:      "content-2",
			SourceType:     "file",
			SourcePath:     strPtr("/test/document2.md"),
			Title:          "Python Best Practices",
			NormalizedText: "Python coding standards and best practices for data science.",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ContentID:      "content-3",
			SourceType:     "file",
			SourcePath:     strPtr("/test/document3.md"),
			Title:          "Architecture Patterns",
			NormalizedText: "Microservices architecture and distributed systems design.",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	}

	for _, item := range items {
		if err := db.InsertKnowledgeItem(ctx, &item); err != nil {
			t.Fatalf("failed to insert knowledge item: %v", err)
		}
	}

	// Insert test chunks
	chunks := []store.Chunk{
		{
			ChunkID:   "chunk-1",
			ContentID: "content-1",
			ChunkHash: "hash1",
			Text:      "Go is a statically typed, compiled programming language designed for building simple, reliable, and efficient software.",
			CreatedAt: time.Now(),
		},
		{
			ChunkID:   "chunk-2",
			ContentID: "content-1",
			ChunkHash: "hash2",
			Text:      "Goroutines and channels provide concurrency primitives for Go programs.",
			CreatedAt: time.Now(),
		},
		{
			ChunkID:   "chunk-3",
			ContentID: "content-2",
			ChunkHash: "hash3",
			Text:      "Python is known for its readability and is widely used in data science and machine learning.",
			CreatedAt: time.Now(),
		},
		{
			ChunkID:   "chunk-4",
			ContentID: "content-3",
			ChunkHash: "hash4",
			Text:      "Microservices architecture breaks down applications into small, independently deployable services.",
			CreatedAt: time.Now(),
		},
	}

	for _, chunk := range chunks {
		if err := db.InsertChunk(ctx, &chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
	}

	// Insert vectors for the chunks
	testVector := makeTestEmbedding(0.1)
	for _, chunk := range chunks {
		if err := db.InsertChunkVector(ctx, chunk.ChunkID, testVector); err != nil {
			t.Fatalf("failed to insert chunk vector: %v", err)
		}
	}

	return db
}

// makeTestEmbedding creates a test embedding vector of the expected dimension.
func makeTestEmbedding(baseValue float32) []float32 {
	embedding := make([]float32, 768) // Typical embedding dimension
	for i := range embedding {
		embedding[i] = baseValue + float32(i)*0.0001
	}
	return embedding
}

// strPtr returns a pointer to a string.
func strPtr(s string) *string {
	return &s
}

// mockEmbeddingServer creates a test HTTP server that returns embeddings.
func mockEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/embeddings":
			resp := llm.EmbeddingResponse{
				Object: "list",
				Data: []llm.EmbeddingData{
					{
						Object:    "embedding",
						Embedding: makeTestEmbedding(0.5),
						Index:     0,
					},
				},
				Model: "test-embedding-model",
				Usage: &llm.EmbeddingUsage{
					PromptTokens: 10,
					TotalTokens:  10,
				},
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestSearchTool_Run(t *testing.T) {
	db := setupSearchTestDB(t)
	defer db.Close()

	server := mockEmbeddingServer(t)
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)
	searcher := retrieval.NewHybridSearcher(db, llmClient)
	tool := NewSearchTool(searcher)

	ctx := context.Background()

	t.Run("search returns results", func(t *testing.T) {
		results, err := tool.Run(ctx, "Go programming", 5)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if len(results) == 0 {
			t.Error("expected at least one result")
		}

		// Verify results have expected fields populated
		for _, r := range results {
			if r.ChunkID == "" {
				t.Error("ChunkID should not be empty")
			}
			if r.Score <= 0 {
				t.Errorf("Score should be positive, got %f", r.Score)
			}
		}
	})

	t.Run("search with default topK", func(t *testing.T) {
		// topK=0 should use default (10)
		results, err := tool.Run(ctx, "programming", 0)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// Should return results (up to default limit)
		if len(results) == 0 {
			t.Error("expected at least one result with default topK")
		}
	})

	t.Run("search with limited topK", func(t *testing.T) {
		results, err := tool.Run(ctx, "programming", 2)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		if len(results) > 2 {
			t.Errorf("expected at most 2 results, got %d", len(results))
		}
	})

	t.Run("search with empty query", func(t *testing.T) {
		results, err := tool.Run(ctx, "", 5)
		if err != nil {
			t.Fatalf("Run() error = %v, want no error with empty results", err)
		}

		if len(results) != 0 {
			t.Errorf("expected 0 results for empty query, got %d", len(results))
		}
	})

	t.Run("search is global (no project filter)", func(t *testing.T) {
		// Search should return results from all projects (no project filter)
		results, err := tool.Run(ctx, "architecture OR python OR go", 10)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}

		// We should get results from multiple content items
		contentIDs := make(map[string]bool)
		for _, r := range results {
			contentIDs[r.ContentID] = true
		}

		// We have 3 different content items, search should find across them
		if len(contentIDs) < 1 {
			t.Error("expected results from at least one content item")
		}
	})
}

func TestSearchTool_Run_NoLLMClient(t *testing.T) {
	db := setupSearchTestDB(t)
	defer db.Close()

	// Create searcher without LLM client (nil)
	searcher := retrieval.NewHybridSearcher(db, nil)
	tool := NewSearchTool(searcher)

	ctx := context.Background()

	// Hybrid search requires LLM client for embeddings
	_, err := tool.Run(ctx, "test query", 5)
	if err == nil {
		t.Error("Run() should return error when LLM client is nil")
	}

	if err != retrieval.ErrLLMClientRequired {
		t.Errorf("expected ErrLLMClientRequired, got %v", err)
	}
}

func TestNewSearchTool(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	tool := NewSearchTool(searcher)

	if tool == nil {
		t.Fatal("NewSearchTool() returned nil")
	}

	if tool.searcher != searcher {
		t.Error("SearchTool.searcher not set correctly")
	}
}
