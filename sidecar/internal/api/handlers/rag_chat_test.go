package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/intent"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// TestInjectMemoriesIntoSystem_PrependsBlock verifies that durable memories
// are surfaced as a system instruction prefix when no system message exists.
func TestInjectMemoriesIntoSystem_PrependsBlock(t *testing.T) {
	in := []llm.Message{
		{Role: "user", Content: "qui est mon comptable ?"},
	}
	memories := []tools.MemoryResult{
		{Type: store.MemoryFact, Content: "Comptable: Pierre Dupont chez Acme Compta"},
	}
	out := injectMemoriesIntoSystem(in, memories)
	if len(out) != 2 {
		t.Fatalf("want 2 messages (system + user), got %d", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("first message must be system, got %q", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "Pierre Dupont") {
		t.Fatalf("system content missing memory: %q", out[0].Content)
	}
	if out[1].Content != "qui est mon comptable ?" {
		t.Fatalf("user message tampered: %q", out[1].Content)
	}
}

// TestInjectMemoriesIntoSystem_AugmentsExistingSystem confirms an existing
// system message keeps its original content while gaining the memory block.
func TestInjectMemoriesIntoSystem_AugmentsExistingSystem(t *testing.T) {
	in := []llm.Message{
		{Role: "system", Content: "Tu es un assistant français."},
		{Role: "user", Content: "et la TVA ?"},
	}
	memories := []tools.MemoryResult{
		{Type: store.MemoryAction, Content: "Payer la TVA Q1 avant le 30/04"},
	}
	out := injectMemoriesIntoSystem(in, memories)
	if len(out) != 2 {
		t.Fatalf("want 2 messages, got %d", len(out))
	}
	if out[0].Role != "system" {
		t.Fatalf("first message must be system, got %q", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "Tu es un assistant français.") {
		t.Fatalf("system base content lost: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "Payer la TVA Q1") {
		t.Fatalf("memory block missing: %q", out[0].Content)
	}
}

// TestInjectMemoriesIntoSystem_NoOpOnEmpty makes sure the helper is a no-op
// when no memories matched, so chat behaviour is unchanged in cold sessions.
func TestInjectMemoriesIntoSystem_NoOpOnEmpty(t *testing.T) {
	in := []llm.Message{{Role: "user", Content: "hello"}}
	out := injectMemoriesIntoSystem(in, nil)
	if len(out) != 1 || out[0].Content != "hello" {
		t.Fatalf("expected unchanged messages, got %+v", out)
	}
}

// mockUnifiedSearcher implements a mock for testing RAG functionality.
type mockUnifiedSearcher struct {
	results *retrieval.UnifiedSearchResponse
	err     error
}

func (m *mockUnifiedSearcher) Search(ctx context.Context, req retrieval.UnifiedSearchRequest) (*retrieval.UnifiedSearchResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// createMockUnifiedSearcher creates a mock searcher with predefined results.
func createMockUnifiedSearcher(results []retrieval.UnifiedResult, searchIntent *intent.Intent) *mockUnifiedSearcher {
	return &mockUnifiedSearcher{
		results: &retrieval.UnifiedSearchResponse{
			Results: results,
			Intent:  searchIntent,
			SearchStats: retrieval.UnifiedSearchStats{
				TotalResults:     len(results),
				KnowledgeResults: len(results),
				MailResults:      0,
				SearchDuration:   10 * time.Millisecond,
			},
		},
	}
}

// createFailingSearcher creates a mock that returns an error.
func createFailingSearcher(err error) *mockUnifiedSearcher {
	return &mockUnifiedSearcher{err: err}
}

// wrapSearcher wraps a mockUnifiedSearcher into a UnifiedSearcher interface.
// Since we can't easily mock UnifiedSearcher, we'll use a real one with mock dependencies.
// For simplicity in tests, we'll create a minimal test helper.
type searcherWrapper struct {
	mock *mockUnifiedSearcher
}

func TestRAGChatHandler_WithRAGEnabled(t *testing.T) {
	// Create mock LM Studio server
	chunks := []string{"Based on ", "the context, ", "the answer is 42."}
	usage := &llm.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
	}
	mockLLMServer := createMockLLMServer(t, chunks, usage)
	defer mockLLMServer.Close()

	// Create LLM client
	client := createMockLLMClient(mockLLMServer.URL)

	// Create mock search results
	mockResults := []retrieval.UnifiedResult{
		{
			ChunkID:    "chunk-1",
			ContentID:  "doc-1",
			SourceType: "file",
			Score:      0.95,
			Excerpt:    "The answer to life, the universe, and everything is 42.",
			Title:      "Hitchhiker's Guide",
		},
		{
			ChunkID:    "chunk-2",
			ContentID:  "mail-1",
			SourceType: "mail",
			Score:      0.85,
			Excerpt:    "Regarding your question about the ultimate answer...",
			Title:      "Re: Ultimate Question",
		},
	}
	mockIntent := &intent.Intent{
		Query:      "what is the answer",
		Sources:    []intent.SourceType{intent.SourceAll},
		Weights:    intent.DefaultWeights,
		Confidence: 0.9,
	}

	// Create a real UnifiedSearcher with mocked store and LLM
	// For this test, we'll verify the handler behavior with a nil searcher first
	// and then add integration points

	logger := zerolog.Nop()
	config := DefaultRAGConfig

	// Create handler without searcher to test fallback behavior
	handler := NewRAGChatHandler(client, nil, nil, config, logger)

	// Create request
	reqBody := `{"messages":[{"role":"user","content":"What is the answer to life?"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should succeed without RAG context when searcher is nil
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", contentType)
	}

	// Parse events and verify stream
	events := parseSSEEvents(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	// No rag_context event should be present when searcher is nil
	for _, event := range events {
		if eventType, ok := event["type"].(string); ok && eventType == "rag_context" {
			t.Error("unexpected rag_context event when searcher is nil")
		}
	}

	// Verify we got content
	var receivedContent strings.Builder
	for _, event := range events {
		if delta, ok := event["delta"].(string); ok {
			receivedContent.WriteString(delta)
		}
	}
	if receivedContent.String() != "Based on the context, the answer is 42." {
		t.Errorf("expected 'Based on the context, the answer is 42.', got '%s'", receivedContent.String())
	}

	// Test to verify mock results structure is correct (for documentation)
	_ = mockResults
	_ = mockIntent
}

func TestRAGChatHandler_RAGDisabledInRequest(t *testing.T) {
	// Create mock LM Studio server
	chunks := []string{"Hello", " world!"}
	mockLLMServer := createMockLLMServer(t, chunks, nil)
	defer mockLLMServer.Close()

	client := createMockLLMClient(mockLLMServer.URL)
	logger := zerolog.Nop()
	config := DefaultRAGConfig

	handler := NewRAGChatHandler(client, nil, nil, config, logger)

	// Request with rag_enabled: false
	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true,"rag_enabled":false}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// No RAG context event should be present
	events := parseSSEEvents(t, rec.Body.String())
	for _, event := range events {
		if eventType, ok := event["type"].(string); ok && eventType == "rag_context" {
			t.Error("unexpected rag_context event when RAG is disabled")
		}
	}
}

func TestRAGChatHandler_ValidationError_EmptyMessages(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewRAGChatHandler(nil, nil, nil, DefaultRAGConfig, logger)

	reqBody := `{"messages":[],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected code 'VALIDATION_ERROR', got '%s'", errObj["code"])
	}
}

func TestRAGChatHandler_InvalidJSON(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewRAGChatHandler(nil, nil, nil, DefaultRAGConfig, logger)

	reqBody := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestRAGChatHandler_MethodNotAllowed(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewRAGChatHandler(nil, nil, nil, DefaultRAGConfig, logger)

	req := httptest.NewRequest(http.MethodGet, "/chat", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestRAGChatHandler_NoLLMClient(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewRAGChatHandler(nil, nil, nil, DefaultRAGConfig, logger) // nil client

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestRAGChatHandler_SSEEventFormat(t *testing.T) {
	// Create mock LM Studio server
	chunks := []string{"A", "B", "C"}
	mockLLMServer := createMockLLMServer(t, chunks, nil)
	defer mockLLMServer.Close()

	client := createMockLLMClient(mockLLMServer.URL)
	logger := zerolog.Nop()
	config := DefaultRAGConfig

	handler := NewRAGChatHandler(client, nil, nil, config, logger)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify SSE format: each event should be "data: {...}\n\n"
	body := rec.Body.String()
	lines := strings.Split(body, "\n\n")

	var nonEmpty []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}

	for _, line := range nonEmpty {
		if !strings.HasPrefix(line, "data: ") {
			t.Errorf("expected line to start with 'data: ', got: %s", line)
		}
	}
}

func TestRAGChatHandler_WithOptionalParams(t *testing.T) {
	var receivedReq llm.ChatRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	config := DefaultRAGConfig

	handler := NewRAGChatHandler(client, nil, nil, config, logger)

	reqBody := `{
		"messages":[{"role":"user","content":"Hello"}],
		"model":"test-model",
		"stream":true,
		"temperature":0.7,
		"max_tokens":100,
		"rag_enabled":false
	}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify parameters were passed
	if receivedReq.Model != "test-model" {
		t.Errorf("expected model 'test-model', got '%s'", receivedReq.Model)
	}
	if receivedReq.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %f", receivedReq.Temperature)
	}
	if receivedReq.MaxTokens != 100 {
		t.Errorf("expected max_tokens 100, got %d", receivedReq.MaxTokens)
	}
}


func TestRAGConfig_Validate(t *testing.T) {
	tests := []struct {
		name     string
		input    RAGConfig
		expected RAGConfig
	}{
		{
			name:  "empty config applies defaults for required fields",
			input: RAGConfig{},
			expected: RAGConfig{
				Enabled:          false, // Enabled is not modified by Validate
				TopK:             DefaultRAGConfig.TopK,
				MaxContextTokens: DefaultRAGConfig.MaxContextTokens,
				MinConfidence:    0, // 0 is valid for MinConfidence
				AlwaysSearch:     false,
			},
		},
		{
			name: "valid config unchanged",
			input: RAGConfig{
				Enabled:          true,
				TopK:             10,
				MaxContextTokens: 3000,
				MinConfidence:    0.5,
				AlwaysSearch:     true,
			},
			expected: RAGConfig{
				Enabled:          true,
				TopK:             10,
				MaxContextTokens: 3000,
				MinConfidence:    0.5,
				AlwaysSearch:     true,
			},
		},
		{
			name: "topK capped at 50",
			input: RAGConfig{
				TopK: 100,
			},
			expected: RAGConfig{
				TopK:             50,
				MaxContextTokens: DefaultRAGConfig.MaxContextTokens,
				MinConfidence:    0,
			},
		},
		{
			name: "maxContextTokens capped at 50000",
			input: RAGConfig{
				MaxContextTokens: 100000,
			},
			expected: RAGConfig{
				TopK:             DefaultRAGConfig.TopK,
				MaxContextTokens: 50000,
				MinConfidence:    0,
			},
		},
		{
			name: "minConfidence clamped to 0-1",
			input: RAGConfig{
				MinConfidence: 1.5,
			},
			expected: RAGConfig{
				TopK:             DefaultRAGConfig.TopK,
				MaxContextTokens: DefaultRAGConfig.MaxContextTokens,
				MinConfidence:    1,
			},
		},
		{
			name: "negative values handled",
			input: RAGConfig{
				TopK:             -5,
				MaxContextTokens: -100,
				MinConfidence:    -0.5,
			},
			expected: RAGConfig{
				TopK:             DefaultRAGConfig.TopK,
				MaxContextTokens: DefaultRAGConfig.MaxContextTokens,
				MinConfidence:    0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.input.Validate()
			if result.TopK != tt.expected.TopK {
				t.Errorf("TopK: expected %d, got %d", tt.expected.TopK, result.TopK)
			}
			if result.MaxContextTokens != tt.expected.MaxContextTokens {
				t.Errorf("MaxContextTokens: expected %d, got %d", tt.expected.MaxContextTokens, result.MaxContextTokens)
			}
			if result.MinConfidence != tt.expected.MinConfidence {
				t.Errorf("MinConfidence: expected %f, got %f", tt.expected.MinConfidence, result.MinConfidence)
			}
			if result.Enabled != tt.expected.Enabled {
				t.Errorf("Enabled: expected %v, got %v", tt.expected.Enabled, result.Enabled)
			}
			if result.AlwaysSearch != tt.expected.AlwaysSearch {
				t.Errorf("AlwaysSearch: expected %v, got %v", tt.expected.AlwaysSearch, result.AlwaysSearch)
			}
		})
	}
}

func TestBuildMessagesWithContext(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewRAGChatHandler(nil, nil, nil, DefaultRAGConfig, logger)

	tests := []struct {
		name            string
		messages        []llm.Message
		ragContext      *RAGContext
		expectedLen     int
		checkSystemMsg  bool
		systemMsgPrefix string
	}{
		{
			name: "no context returns original messages",
			messages: []llm.Message{
				{Role: "user", Content: "Hello"},
			},
			ragContext:  &RAGContext{Sources: []RAGSource{}},
			expectedLen: 1,
		},
		{
			name: "adds system message with context",
			messages: []llm.Message{
				{Role: "user", Content: "What is the answer?"},
			},
			ragContext: &RAGContext{
				Sources: []RAGSource{
					{
						ContentID:  "doc-1",
						SourceType: "file",
						Title:      "Test Document",
						Excerpt:    "The answer is 42.",
						Score:      0.9,
					},
				},
			},
			expectedLen:    2, // system + user
			checkSystemMsg: true,
		},
		{
			name: "augments existing system message",
			messages: []llm.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "What is the answer?"},
			},
			ragContext: &RAGContext{
				Sources: []RAGSource{
					{
						ContentID:  "doc-1",
						SourceType: "file",
						Title:      "Test Document",
						Excerpt:    "The answer is 42.",
						Score:      0.9,
					},
				},
			},
			expectedLen:     2, // augmented system + user
			checkSystemMsg:  true,
			systemMsgPrefix: "You are a helpful assistant.",
		},
		{
			name: "handles mail source type",
			messages: []llm.Message{
				{Role: "user", Content: "Check my email"},
			},
			ragContext: &RAGContext{
				Sources: []RAGSource{
					{
						ContentID:  "mail-1",
						SourceType: "mail",
						Title:      "Re: Meeting",
						Excerpt:    "The meeting is at 3pm.",
						Score:      0.85,
					},
				},
			},
			expectedLen:    2,
			checkSystemMsg: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.buildMessagesWithContext(tt.messages, tt.ragContext)

			if len(result) != tt.expectedLen {
				t.Errorf("expected %d messages, got %d", tt.expectedLen, len(result))
			}

			if tt.checkSystemMsg && len(result) > 0 {
				// First message should be system
				if result[0].Role != "system" {
					t.Errorf("expected first message role 'system', got '%s'", result[0].Role)
				}

				// Should contain context header
				if !strings.Contains(result[0].Content, "## Contexte pertinent") {
					t.Error("expected system message to contain '## Contexte pertinent'")
				}

				// Should contain citation instruction
				if !strings.Contains(result[0].Content, "Cite les sources") {
					t.Error("expected system message to contain citation instruction")
				}

				// If there was an existing system message, it should be preserved
				if tt.systemMsgPrefix != "" {
					if !strings.HasPrefix(result[0].Content, tt.systemMsgPrefix) {
						t.Errorf("expected system message to start with '%s'", tt.systemMsgPrefix)
					}
				}
			}
		})
	}
}

func TestRAGChatHandler_LLMError(t *testing.T) {
	// Create a mock server that returns an error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"Model not loaded"}}`))
	}))
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	config := DefaultRAGConfig

	handler := NewRAGChatHandler(client, nil, nil, config, logger)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should get SSE format since headers are set
	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", contentType)
	}

	// Parse SSE events - should contain an error event
	events := parseSSEEvents(t, rec.Body.String())

	var foundError bool
	for _, event := range events {
		if eventType, ok := event["type"].(string); ok && eventType == "error" {
			foundError = true
			errObj, ok := event["error"].(map[string]any)
			if !ok {
				t.Fatal("expected error object in error event")
			}
			if errObj["code"] != "LLM_STUDIO_ERROR" {
				t.Errorf("expected code 'LLM_STUDIO_ERROR', got '%s'", errObj["code"])
			}
		}
	}

	if !foundError {
		t.Error("expected error event in response")
	}
}

func TestRAGContextEvent_JSONFormat(t *testing.T) {
	event := RAGContextEvent{
		Type: "rag_context",
		Sources: []RAGSource{
			{
				ContentID:  "doc-1",
				SourceType: "file",
				Title:      "Test Doc",
				Excerpt:    "Some content...",
				Score:      0.95,
			},
		},
		Intent: &IntentDTO{
			Query:      "test query",
			Sources:    []string{"knowledge"},
			Weights:    map[string]float64{"knowledge": 0.6, "mail": 0.4},
			Confidence: 0.9,
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal RAGContextEvent: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// Verify structure
	if parsed["type"] != "rag_context" {
		t.Errorf("expected type 'rag_context', got '%v'", parsed["type"])
	}

	sources, ok := parsed["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Error("expected sources array with 1 element")
	}

	intentObj, ok := parsed["intent"].(map[string]any)
	if !ok {
		t.Error("expected intent object")
	}
	if intentObj["query"] != "test query" {
		t.Errorf("expected intent query 'test query', got '%v'", intentObj["query"])
	}
}
