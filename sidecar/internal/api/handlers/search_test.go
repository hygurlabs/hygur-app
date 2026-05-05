package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// strPtr is a helper to create string pointers for test data.
func strPtr(s string) *string {
	return &s
}

// setupSearchHandlerTestDB creates a test database with sample data.
func setupSearchHandlerTestDB(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	ctx := context.Background()

	// Insert test knowledge items
	items := []store.KnowledgeItem{
		{
			ContentID:      "content-1",
			SourceType:     "file",
			SourcePath:     strPtr("/test/doc1.md"),
			Title:          "Go Programming",
			NormalizedText: "Go programming language guide.",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ContentID:      "content-2",
			SourceType:     "file",
			SourcePath:     strPtr("/test/doc2.md"),
			Title:          "Python Guide",
			NormalizedText: "Python programming best practices.",
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
			Text:      "Go is a statically typed language for building reliable software.",
			CreatedAt: time.Now(),
		},
		{
			ChunkID:   "chunk-2",
			ContentID: "content-2",
			ChunkHash: "hash2",
			Text:      "Python is popular for data science and machine learning applications.",
			CreatedAt: time.Now(),
		},
	}

	for _, chunk := range chunks {
		if err := db.InsertChunk(ctx, &chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
	}

	// Insert vectors for the chunks
	testVector := makeSearchHandlerTestEmbedding(0.1)
	for _, chunk := range chunks {
		if err := db.InsertChunkVector(ctx, chunk.ChunkID, testVector); err != nil {
			t.Fatalf("failed to insert chunk vector: %v", err)
		}
	}

	return db
}

func makeSearchHandlerTestEmbedding(baseValue float32) []float32 {
	embedding := make([]float32, 768)
	for i := range embedding {
		embedding[i] = baseValue + float32(i)*0.0001
	}
	return embedding
}

// mockSearchHandlerEmbeddingServer creates a test server for embeddings.
func mockSearchHandlerEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			resp := llm.EmbeddingResponse{
				Object: "list",
				Data: []llm.EmbeddingData{
					{
						Object:    "embedding",
						Embedding: makeSearchHandlerTestEmbedding(0.5),
						Index:     0,
					},
				},
				Model: "test-model",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
}

func TestSearchHandler_Search(t *testing.T) {
	db := setupSearchHandlerTestDB(t)
	defer db.Close()

	server := mockSearchHandlerEmbeddingServer(t)
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 0, http.DefaultClient)
	searcher := retrieval.NewHybridSearcher(db, llmClient)
	searchTool := tools.NewSearchTool(searcher)
	logger := zerolog.Nop()

	handler := NewSearchHandler(searchTool, db, logger)

	t.Run("successful search", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search?q=Go+programming", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		var result ToolSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if result.Total < 0 {
			t.Error("Total should not be negative")
		}

		// Check that results have titles enriched
		for _, r := range result.Results {
			if r.ChunkID == "" {
				t.Error("ChunkID should not be empty")
			}
		}
	})

	t.Run("search with top_k parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search?q=programming&top_k=1", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		var result ToolSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(result.Results) > 1 {
			t.Errorf("expected at most 1 result, got %d", len(result.Results))
		}
	})

	t.Run("missing q parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}

		var errResp map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}

		errObj, ok := errResp["error"].(map[string]any)
		if !ok {
			t.Fatal("expected error object in response")
		}

		if errObj["code"] != "VALIDATION_ERROR" {
			t.Errorf("expected error code VALIDATION_ERROR, got %v", errObj["code"])
		}
	})

	t.Run("invalid top_k parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search?q=test&top_k=abc", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}
	})

	t.Run("negative top_k parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search?q=test&top_k=-5", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}
	})

	t.Run("top_k capped at 100", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tools/search?q=test&top_k=500", nil)
		w := httptest.NewRecorder()

		handler.Search(w, req)

		resp := w.Result()
		// Should succeed, just cap the value
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}
	})
}

func TestSearchHandler_Search_NoTool(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewSearchHandler(nil, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/tools/search?q=test", nil)
	w := httptest.NewRecorder()

	handler.Search(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
}

func TestSearchHandler_Search_NoLLMClient(t *testing.T) {
	db := setupSearchHandlerTestDB(t)
	defer db.Close()

	// Searcher without LLM client
	searcher := retrieval.NewHybridSearcher(db, nil)
	searchTool := tools.NewSearchTool(searcher)
	logger := zerolog.Nop()

	handler := NewSearchHandler(searchTool, db, logger)

	req := httptest.NewRequest(http.MethodGet, "/tools/search?q=test", nil)
	w := httptest.NewRecorder()

	handler.Search(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
}

func TestNewSearchHandler(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	searchTool := tools.NewSearchTool(searcher)
	logger := zerolog.Nop()

	handler := NewSearchHandler(searchTool, db, logger)

	if handler == nil {
		t.Fatal("NewSearchHandler() returned nil")
	}

	if handler.tool != searchTool {
		t.Error("handler.tool not set correctly")
	}

	if handler.store != db {
		t.Error("handler.store not set correctly")
	}
}

func TestNewSearchHandlerWithUnified(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	searchTool := tools.NewSearchTool(searcher)
	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()

	handler := NewSearchHandlerWithUnified(searchTool, db, unifiedSearcher, logger)

	if handler == nil {
		t.Fatal("NewSearchHandlerWithUnified() returned nil")
	}

	if handler.unifiedSearcher != unifiedSearcher {
		t.Error("handler.unifiedSearcher not set correctly")
	}
}

func TestSearchHandler_SetUnifiedSearcher(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	logger := zerolog.Nop()
	handler := NewSearchHandler(nil, db, logger)

	if handler.unifiedSearcher != nil {
		t.Error("unifiedSearcher should be nil initially")
	}

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	handler.SetUnifiedSearcher(unifiedSearcher)

	if handler.unifiedSearcher != unifiedSearcher {
		t.Error("SetUnifiedSearcher did not set the searcher correctly")
	}
}

func TestSearchHandler_UnifiedSearch_NoSearcher(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewSearchHandler(nil, nil, logger)

	reqBody := `{"query": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_InvalidBody(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_EmptyQuery(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	reqBody := `{"query": ""}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_InvalidSource(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	reqBody := `{"query": "test", "sources": ["invalid_source"]}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_InvalidWeightKey(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	reqBody := `{"query": "test", "weights": {"invalid_source": 0.5}}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_NoLLMClient(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// UnifiedSearcher without LLM client
	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	reqBody := `{"query": "test query"}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
}

func TestSearchHandler_UnifiedSearch_ValidSources(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Note: This will fail at search time due to no LLM client,
	// but we're testing that source validation passes
	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	tests := []struct {
		name    string
		sources []string
	}{
		{"knowledge only", []string{"knowledge"}},
		{"mail only", []string{"mail"}},
		{"all", []string{"all"}},
		{"both explicit", []string{"knowledge", "mail"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody, _ := json.Marshal(map[string]any{
				"query":   "test",
				"sources": tt.sources,
			})
			req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(string(reqBody)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handler.UnifiedSearch(w, req)

			resp := w.Result()
			// Should not be a bad request - source validation passed
			// May fail with 503 due to no LLM client, which is expected
			if resp.StatusCode == http.StatusBadRequest {
				t.Errorf("source validation should pass for %v", tt.sources)
			}
		})
	}
}

func TestSearchHandler_UnifiedSearch_ValidWeights(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	unifiedSearcher := retrieval.NewUnifiedSearcher(db, nil)
	logger := zerolog.Nop()
	handler := NewSearchHandlerWithUnified(nil, db, unifiedSearcher, logger)

	reqBody := `{"query": "test", "weights": {"knowledge": 0.7, "mail": 0.3}}`
	req := httptest.NewRequest(http.MethodPost, "/search", newJSONReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.UnifiedSearch(w, req)

	resp := w.Result()
	// Should not be a bad request - weight validation passed
	if resp.StatusCode == http.StatusBadRequest {
		t.Error("weight validation should pass for valid weights")
	}
}

// Helper function to create a JSON reader from a string
func newJSONReader(s string) *stringReader {
	return &stringReader{s: s, i: 0}
}

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(p []byte) (n int, err error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n = copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
