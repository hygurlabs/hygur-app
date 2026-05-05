package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// testLogger creates a zerolog logger that discards output for tests.
func testLogger() zerolog.Logger {
	return zerolog.New(zerolog.NewTestWriter(nil)).Level(zerolog.Disabled)
}

// setupTestDB creates an in-memory database for testing.
func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return db
}

// setupTestIngestor creates an ingestor with a text parser for testing.
func setupTestIngestor() *ingest.Ingestor {
	ing := ingest.NewIngestor()
	// Register the text parser if available
	return ing
}

// createTestFile creates a temporary file for ingestion tests.
func createTestFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	return tmpFile
}

// setupSearcherWithMockLLM creates a SemanticSearcher backed by a mock embedding server.
// The server returns a 768-dim zero vector for any query.
func setupSearcherWithMockLLM(t *testing.T, db *store.DB) (*retrieval.SemanticSearcher, func()) {
	t.Helper()
	dims := 768
	zeros := strings.Repeat("0,", dims-1) + "0"
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[{"index":0,"embedding":[%s]}],"model":"test"}`, zeros)
	}))
	llmClient := llm.NewClientWithHTTP(mockServer.URL, 5*time.Second, 0, &http.Client{Timeout: 5 * time.Second})
	return retrieval.NewHybridSearcher(db, llmClient), mockServer.Close
}

// createTestRouter creates a chi router with the knowledge handler mounted.
func createTestRouter(h *KnowledgeHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/knowledge", func(r chi.Router) {
		r.Post("/ingest", h.Ingest)
		r.Post("/search", h.Search)
		r.Get("/{content_id}", h.Get)
		r.Delete("/{content_id}", h.Delete)
	})
	return r
}

// TestKnowledgeHandler_Ingest_ValidFile tests ingestion of a valid file.
func TestKnowledgeHandler_Ingest_ValidFile(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	// Register a simple text parser
	ing.RegisterParser(&testTextParser{})

	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	// Create a test file
	testFile := createTestFile(t, "This is test content for ingestion.")

	reqBody := IngestRequest{
		Path: testFile,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var resp IngestResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ContentID == "" {
		t.Error("expected content_id to be set")
	}

	if resp.Status != "indexed" {
		t.Errorf("expected status 'indexed', got '%s'", resp.Status)
	}

	if resp.ChunkCount < 1 {
		t.Errorf("expected at least 1 chunk, got %d", resp.ChunkCount)
	}

	if resp.Title != "test.txt" {
		t.Errorf("expected title 'test.txt', got '%s'", resp.Title)
	}
}

// TestKnowledgeHandler_Ingest_FileNotFound tests ingestion with non-existent file.
func TestKnowledgeHandler_Ingest_FileNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	reqBody := IngestRequest{
		Path: "/nonexistent/path/to/file.txt",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Ingest_RelativePath tests ingestion with relative path (error).
func TestKnowledgeHandler_Ingest_RelativePath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	reqBody := IngestRequest{
		Path: "relative/path/to/file.txt",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected code 'VALIDATION_ERROR', got '%s'", errorObj["code"])
	}
}

// TestKnowledgeHandler_Ingest_EmptyPath tests ingestion with empty path.
func TestKnowledgeHandler_Ingest_EmptyPath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	reqBody := IngestRequest{
		Path: "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Ingest_InvalidJSON tests ingestion with invalid JSON.
func TestKnowledgeHandler_Ingest_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Search_ValidQuery tests search with a valid query.
func TestKnowledgeHandler_Search_ValidQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher, closeMock := setupSearcherWithMockLLM(t, db)
	defer closeMock()
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	reqBody := SearchRequest{
		Query: "test query",
		TopK:  10,
		Mode:  "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Results should be empty since we haven't indexed anything
	if resp.Results == nil {
		t.Error("expected results array to be non-nil")
	}
}

// TestKnowledgeHandler_Search_EmptyQuery tests search with empty query (error).
func TestKnowledgeHandler_Search_EmptyQuery(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	reqBody := SearchRequest{
		Query: "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Search_InvalidMode tests search with invalid mode.
func TestKnowledgeHandler_Search_InvalidMode(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	reqBody := SearchRequest{
		Query: "test query",
		Mode:  "invalid_mode",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected code 'VALIDATION_ERROR', got '%s'", errorObj["code"])
	}
}

// TestKnowledgeHandler_Search_DefaultTopK tests search with default topK.
func TestKnowledgeHandler_Search_DefaultTopK(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher, closeMock := setupSearcherWithMockLLM(t, db)
	defer closeMock()
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	// Don't specify topK, should default to 10
	reqBody := SearchRequest{
		Query: "test query",
		Mode:  "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Search_DifferentModes tests accepted/rejected mode values.
func TestKnowledgeHandler_Search_DifferentModes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	// These modes are all valid aliases for semantic search.
	validModes := []string{"semantic", "hybrid", "vector", ""}
	for _, mode := range validModes {
		reqBody := SearchRequest{Query: "test query", Mode: mode}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		// Without an LLM client the search itself fails (500), but mode validation passes.
		if rec.Code == http.StatusBadRequest {
			t.Errorf("mode '%s' should not be rejected; got 400: %s", mode, rec.Body.String())
		}
	}

	// "fts" is explicitly rejected.
	reqBody := SearchRequest{Query: "test query", Mode: "fts"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("mode 'fts' should be rejected with 400; got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Get_Exists tests getting an existing knowledge item.
func TestKnowledgeHandler_Get_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a test knowledge item
	item := &store.KnowledgeItem{
		ContentID:      "test-content-id",
		SourceType:     "file",
		Title:          "Test Document",
		NormalizedText: "This is normalized text content.",
		Metadata:       map[string]any{"author": "test"},
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/test-content-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp KnowledgeItemResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ContentID != "test-content-id" {
		t.Errorf("expected content_id 'test-content-id', got '%s'", resp.ContentID)
	}

	if resp.Title != "Test Document" {
		t.Errorf("expected title 'Test Document', got '%s'", resp.Title)
	}

	if resp.SourceType != "file" {
		t.Errorf("expected source_type 'file', got '%s'", resp.SourceType)
	}
}

// TestKnowledgeHandler_Get_NotFound tests getting a non-existent knowledge item.
func TestKnowledgeHandler_Get_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/nonexistent-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "NOT_FOUND" {
		t.Errorf("expected code 'NOT_FOUND', got '%s'", errorObj["code"])
	}
}

// TestKnowledgeHandler_Delete_Success tests successful deletion.
func TestKnowledgeHandler_Delete_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a test knowledge item
	item := &store.KnowledgeItem{
		ContentID:      "test-delete-id",
		SourceType:     "file",
		Title:          "Test Document",
		NormalizedText: "This is content to be deleted.",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/knowledge/test-delete-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}

	// Verify item was deleted
	deletedItem, err := db.GetKnowledgeItem(context.Background(), "test-delete-id")
	if err != nil {
		t.Fatalf("failed to check deleted item: %v", err)
	}
	if deletedItem != nil {
		t.Error("expected item to be deleted")
	}
}

// TestKnowledgeHandler_Delete_NotFound tests deletion of non-existent item.
func TestKnowledgeHandler_Delete_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/knowledge/nonexistent-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_NoStore tests handlers when store is not configured.
func TestKnowledgeHandler_NoStore(t *testing.T) {
	h := NewKnowledgeHandler(nil, nil, nil, testLogger())
	router := createTestRouter(h)

	// Test GET without store
	req := httptest.NewRequest(http.MethodGet, "/knowledge/test-id", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}

	// Test DELETE without store
	req = httptest.NewRequest(http.MethodDelete, "/knowledge/test-id", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_NoIngestor tests ingest when ingestor is not configured.
func TestKnowledgeHandler_NoIngestor(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	// Create a test file
	testFile := createTestFile(t, "Test content")

	reqBody := IngestRequest{
		Path: testFile,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_NoSearcher tests search when searcher is not configured.
func TestKnowledgeHandler_NoSearcher(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	reqBody := SearchRequest{
		Query: "test query",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Get_WithChunks tests getting item with chunk count.
func TestKnowledgeHandler_Get_WithChunks(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a test knowledge item
	item := &store.KnowledgeItem{
		ContentID:      "test-with-chunks",
		SourceType:     "file",
		Title:          "Test Document",
		NormalizedText: "This is content with chunks.",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	// Insert some chunks
	for i := 0; i < 3; i++ {
		chunk := &store.Chunk{
			ChunkID:   "chunk-" + string(rune('a'+i)),
			ContentID: "test-with-chunks",
			ChunkHash: "hash-" + string(rune('a'+i)),
			Text:      "Chunk content",
			CreatedAt: time.Now(),
		}
		if err := db.InsertChunk(context.Background(), chunk); err != nil {
			t.Fatalf("failed to insert test chunk: %v", err)
		}
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/test-with-chunks", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp KnowledgeItemResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ChunkCount != 3 {
		t.Errorf("expected chunk_count 3, got %d", resp.ChunkCount)
	}
}

// testTextParser is a simple text parser for testing.
type testTextParser struct{}

func (p *testTextParser) SupportedExtensions() []string {
	return []string{".txt"}
}

func (p *testTextParser) Parse(ctx context.Context, r io.Reader) (string, ingest.Metadata, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return "", nil, err
	}
	return string(content), ingest.Metadata{"type": "text"}, nil
}

// TestKnowledgeHandler_Ingest_Directory tests ingestion with a directory path (error).
func TestKnowledgeHandler_Ingest_Directory(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	// Use temp directory path
	tmpDir := t.TempDir()

	reqBody := IngestRequest{
		Path: tmpDir,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected code 'VALIDATION_ERROR', got '%s'", errorObj["code"])
	}
}

// TestKnowledgeHandler_Search_TopKBounds tests search with various topK values.
func TestKnowledgeHandler_Search_TopKBounds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher, closeMock := setupSearcherWithMockLLM(t, db)
	defer closeMock()
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	testCases := []struct {
		name     string
		topK     int
		expected int // Expected capped value (not directly testable but we ensure no error)
	}{
		{"zero", 0, 10},        // Should default to 10
		{"negative", -5, 10},   // Should default to 10
		{"normal", 50, 50},     // Should use provided value
		{"max", 100, 100},      // Should use provided value
		{"over_max", 200, 100}, // Should cap at 100
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reqBody := SearchRequest{
				Query: "test query",
				TopK:  tc.topK,
				Mode:  "",
			}
			body, _ := json.Marshal(reqBody)

			req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestKnowledgeHandler_Ingest_WithProjectAndTags tests ingestion with project and tags.
func TestKnowledgeHandler_Ingest_WithProjectAndTags(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ing := setupTestIngestor()
	ing.RegisterParser(&testTextParser{})

	h := NewKnowledgeHandler(db, ing, nil, testLogger())
	router := createTestRouter(h)

	// Create a test file
	testFile := createTestFile(t, "Content with project and tags.")

	projectID := "test-project-123"
	reqBody := IngestRequest{
		Path:      testFile,
		ProjectID: &projectID,
		Tags:      []string{"tag1", "tag2"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Search_WithProjectID tests search with project filter.
func TestKnowledgeHandler_Search_WithProjectID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	searcher, closeMock := setupSearcherWithMockLLM(t, db)
	defer closeMock()
	h := NewKnowledgeHandler(db, nil, searcher, testLogger())
	router := createTestRouter(h)

	projectID := "test-project-id"
	reqBody := SearchRequest{
		Query:     "test query",
		ProjectID: &projectID,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
}

// TestKnowledgeHandler_Delete_CascadeChunks tests that deleting an item also deletes its chunks.
func TestKnowledgeHandler_Delete_CascadeChunks(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a test knowledge item
	item := &store.KnowledgeItem{
		ContentID:      "test-cascade-delete",
		SourceType:     "file",
		Title:          "Test Document",
		NormalizedText: "Content to be deleted with chunks.",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	// Insert some chunks
	for i := 0; i < 3; i++ {
		chunk := &store.Chunk{
			ChunkID:   "cascade-chunk-" + string(rune('a'+i)),
			ContentID: "test-cascade-delete",
			ChunkHash: "hash-" + string(rune('a'+i)),
			Text:      "Chunk content",
			CreatedAt: time.Now(),
		}
		if err := db.InsertChunk(context.Background(), chunk); err != nil {
			t.Fatalf("failed to insert test chunk: %v", err)
		}
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/knowledge/test-cascade-delete", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}

	// Verify chunks were also deleted (cascade)
	chunks, err := db.GetChunksByContentID(context.Background(), "test-cascade-delete")
	if err != nil {
		t.Fatalf("failed to check chunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks after cascade delete, got %d", len(chunks))
	}
}

// TestKnowledgeHandler_Get_Metadata tests that metadata is properly returned.
func TestKnowledgeHandler_Get_Metadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a test knowledge item with metadata
	item := &store.KnowledgeItem{
		ContentID:      "test-metadata",
		SourceType:     "file",
		Title:          "Test Document with Metadata",
		NormalizedText: "Content with metadata.",
		Metadata: map[string]any{
			"author":   "Test Author",
			"date":     "2024-01-01",
			"tags":     []string{"test", "metadata"},
			"revision": 3,
		},
		VersionID: "v1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/test-metadata", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp KnowledgeItemResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Metadata == nil {
		t.Error("expected metadata to be non-nil")
	}

	if resp.Metadata["author"] != "Test Author" {
		t.Errorf("expected author 'Test Author', got '%v'", resp.Metadata["author"])
	}
}

// TestKnowledgeHandler_Get_SourcePath tests that source_path is properly returned.
func TestKnowledgeHandler_Get_SourcePath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sourcePath := "/path/to/document.txt"

	// Insert a test knowledge item with source path
	item := &store.KnowledgeItem{
		ContentID:      "test-source-path",
		SourceType:     "file",
		SourcePath:     &sourcePath,
		Title:          "Test Document",
		NormalizedText: "Content with source path.",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	h := NewKnowledgeHandler(db, nil, nil, testLogger())
	router := createTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/test-source-path", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp KnowledgeItemResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.SourcePath == nil {
		t.Error("expected source_path to be non-nil")
	}

	if *resp.SourcePath != sourcePath {
		t.Errorf("expected source_path '%s', got '%s'", sourcePath, *resp.SourcePath)
	}
}
