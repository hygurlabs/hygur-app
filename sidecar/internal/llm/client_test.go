package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
)

// TestChat_BreakerOpensAfterConsecutiveOutages is the B1 wiring test: a backend
// that always 503s makes Chat return ErrLLMUnavailable; after `threshold`
// consecutive outages the breaker opens and the next call fast-fails WITHOUT
// touching the server.
func TestChat_BreakerOpensAfterConsecutiveOutages(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, 2*time.Second, 0, http.DefaultClient) // 0 retries → 1 hit per call
	req := ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}}

	for i := 0; i < defaultBreakerThreshold; i++ {
		if _, err := c.Chat(context.Background(), req); !errors.Is(err, ErrLLMUnavailable) {
			t.Fatalf("call %d: err=%v, want ErrLLMUnavailable", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != int32(defaultBreakerThreshold) {
		t.Fatalf("server hits = %d, want %d", got, defaultBreakerThreshold)
	}
	// Breaker is now open → fast-fail, server untouched.
	if _, err := c.Chat(context.Background(), req); !errors.Is(err, ErrLLMUnavailable) {
		t.Fatalf("open-breaker call: err=%v, want ErrLLMUnavailable", err)
	}
	if got := atomic.LoadInt32(&hits); got != int32(defaultBreakerThreshold) {
		t.Fatalf("breaker must fast-fail without hitting the server: hits=%d, want %d", got, defaultBreakerThreshold)
	}
}

// Helper function to create a test client pointing to a test server.
func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    10 * time.Second,
		MaxRetries: 2,
	}
	return NewClient(cfg)
}

func TestNewClient(t *testing.T) {
	cfg := &config.LMStudioConfig{
		URL:        "http://localhost:1234/",
		Timeout:    30 * time.Second,
		MaxRetries: 3,
	}

	client := NewClient(cfg)

	if client.baseURL != "http://localhost:1234" {
		t.Errorf("expected baseURL without trailing slash, got %s", client.baseURL)
	}
	if client.timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", client.timeout)
	}
	if client.maxRetries != 3 {
		t.Errorf("expected maxRetries 3, got %d", client.maxRetries)
	}
}

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		resp := ModelsResponse{
			Object: "list",
			Data: []ModelInfo{
				{ID: "model-1", Object: "model", OwnedBy: "local"},
				{ID: "model-2", Object: "model", OwnedBy: "local"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "model-1" {
		t.Errorf("expected model-1, got %s", models[0].ID)
	}
}

func TestListModelsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Internal server error",
				"type":    "server_error",
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	_, err := client.ListModels(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", apiErr.StatusCode)
	}
}

func TestPing(t *testing.T) {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		expected bool
	}{
		{
			name: "server available",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(ModelsResponse{Object: "list", Data: []ModelInfo{}})
			},
			expected: true,
		},
		{
			name: "server returns error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := newTestClient(t, server)
			ctx := context.Background()

			ok, err := client.Ping(ctx)
			if err != nil {
				t.Fatalf("Ping failed with error: %v", err)
			}
			if ok != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, ok)
			}
		})
	}
}

func TestPingServerDown(t *testing.T) {
	// Create a server and immediately close it
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close()

	cfg := &config.LMStudioConfig{
		URL:        serverURL,
		Timeout:    2 * time.Second,
		MaxRetries: 0,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	ok, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping should not return error for connection refused: %v", err)
	}
	if ok {
		t.Error("expected false for closed server")
	}
}

func TestChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		if req.Stream {
			t.Error("expected stream to be false for Chat")
		}

		resp := ChatResponse{
			ID:    "chatcmpl-123",
			Model: req.Model,
			Choices: []Choice{
				{
					Index: 0,
					Message: &Message{
						Role:    "assistant",
						Content: "Hello! How can I help you?",
					},
					FinishReason: "stop",
				},
			},
			Usage: &Usage{
				PromptTokens:     10,
				CompletionTokens: 8,
				TotalTokens:      18,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	resp, err := client.Chat(ctx, req)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("expected id chatcmpl-123, got %s", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello! How can I help you?" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
}

func TestStreamChat(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-123","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		if !req.Stream {
			t.Error("expected stream to be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected ResponseWriter to be a Flusher")
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
	}

	var accumulated strings.Builder
	var doneCount int
	var finalUsage *Usage

	err := client.StreamChat(ctx, req, func(delta string, done bool, usage *Usage) error {
		if done {
			doneCount++
			finalUsage = usage
		} else if delta != "" {
			accumulated.WriteString(delta)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	if accumulated.String() != "Hello there!" {
		t.Errorf("expected 'Hello there!', got '%s'", accumulated.String())
	}

	if doneCount != 1 {
		t.Errorf("expected done to be called once, got %d", doneCount)
	}

	if finalUsage == nil {
		t.Error("expected usage to be populated")
	} else if finalUsage.TotalTokens != 8 {
		t.Errorf("expected total tokens 8, got %d", finalUsage.TotalTokens)
	}
}

func TestStreamChatErrorMidStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Send one valid chunk
		chunk := `{"id":"chatcmpl-123","choices":[{"delta":{"content":"Hello"}}]}`
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()

		// Send invalid JSON
		fmt.Fprintf(w, "data: {invalid json}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
	}

	var received []string
	err := client.StreamChat(ctx, req, func(delta string, done bool, usage *Usage) error {
		if delta != "" {
			received = append(received, delta)
		}
		return nil
	})

	if err == nil {
		t.Fatal("expected error for invalid JSON mid-stream")
	}

	if !strings.Contains(err.Error(), "failed to parse SSE data") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Should have received the first chunk before error
	if len(received) != 1 || received[0] != "Hello" {
		t.Errorf("expected ['Hello'], got %v", received)
	}
}

func TestStreamChatHandlerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		chunk := `{"id":"chatcmpl-123","choices":[{"delta":{"content":"Hello"}}]}`
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
	}

	handlerErr := fmt.Errorf("handler error")
	err := client.StreamChat(ctx, req, func(delta string, done bool, usage *Usage) error {
		return handlerErr
	})

	if err != handlerErr {
		t.Errorf("expected handler error, got: %v", err)
	}
}

func TestStreamChatTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay longer than the context timeout
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    5 * time.Second,
		MaxRetries: 0,
	}
	client := NewClient(cfg)

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
	}

	err := client.StreamChat(ctx, req, func(delta string, done bool, usage *Usage) error {
		return nil
	})

	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

func TestRetryOnServerError(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Success on third attempt
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ModelsResponse{Object: "list", Data: []ModelInfo{{ID: "model-1"}}})
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    10 * time.Second,
		MaxRetries: 3,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}

	if len(models) != 1 {
		t.Errorf("expected 1 model, got %d", len(models))
	}

	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestNoRetryOn4xxErrors(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{"message": "Bad request"},
		})
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    10 * time.Second,
		MaxRetries: 3,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	_, err := client.ListModels(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	// Should only attempt once - no retry on 400
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("expected 1 attempt (no retry on 400), got %d", attempts)
	}
}

func TestChatRetryOnError(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			ID: "chatcmpl-123",
			Choices: []Choice{
				{Index: 0, Message: &Message{Role: "assistant", Content: "Hi"}, FinishReason: "stop"},
			},
		})
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    10 * time.Second,
		MaxRetries: 2,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	resp, err := client.Chat(ctx, ChatRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("unexpected response id: %s", resp.ID)
	}
}

// Test the StreamReader interface
func TestStreamReader(t *testing.T) {
	sseData := `data: {"id":"1","choices":[{"delta":{"content":"Hello"}}]}

data: {"id":"1","choices":[{"delta":{"content":" World"}}]}

data: {"id":"1","choices":[{"delta":{}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}

data: [DONE]

`

	reader := NewStreamReader(strings.NewReader(sseData))

	// First chunk
	delta, done, _, err := reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Error("unexpected done")
	}
	if delta != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", delta)
	}

	// Second chunk
	delta, done, _, err = reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Error("unexpected done")
	}
	if delta != " World" {
		t.Errorf("expected ' World', got '%s'", delta)
	}

	// Third chunk (empty delta with usage)
	// The reader skips empty content deltas, so we'll get [DONE] next

	// Done
	delta, done, usage, err := reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Error("expected done")
	}
	if delta != "" {
		t.Errorf("expected empty delta at done, got '%s'", delta)
	}

	// Check accumulated
	if reader.Accumulated() != "Hello World" {
		t.Errorf("expected 'Hello World', got '%s'", reader.Accumulated())
	}

	// Verify usage was captured
	if usage == nil {
		t.Error("expected usage to be populated")
	} else if usage.TotalTokens != 7 {
		t.Errorf("expected total tokens 7, got %d", usage.TotalTokens)
	}

	// Subsequent calls should return done
	_, done, _, _ = reader.Next()
	if !done {
		t.Error("expected done on subsequent call")
	}
}

func TestContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait for the context to be cancelled
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newTestClient(t, server)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := client.ListModels(ctx)
	if err == nil {
		t.Fatal("expected error due to cancelled context")
	}

	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got: %v", err)
	}
}

func TestAPIErrorFormat(t *testing.T) {
	err := &APIError{
		StatusCode: 500,
		Message:    "Internal server error",
		Type:       "server_error",
	}

	expected := "LM Studio API error (status 500): Internal server error"
	if err.Error() != expected {
		t.Errorf("expected '%s', got '%s'", expected, err.Error())
	}
}

func TestNewClientWithHTTP(t *testing.T) {
	customClient := &http.Client{Timeout: 5 * time.Second}
	client := NewClientWithHTTP("http://localhost:1234/", 10*time.Second, 2, customClient)

	if client.baseURL != "http://localhost:1234" {
		t.Errorf("expected baseURL without trailing slash, got %s", client.baseURL)
	}
	if client.httpClient != customClient {
		t.Error("expected custom HTTP client to be used")
	}
}

func TestStreamChatRetryOnServerError(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Success on second attempt
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    10 * time.Second,
		MaxRetries: 2,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	var content strings.Builder
	err := client.StreamChat(ctx, ChatRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	}, func(delta string, done bool, usage *Usage) error {
		content.WriteString(delta)
		return nil
	})

	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}

	if content.String() != "OK" {
		t.Errorf("expected 'OK', got '%s'", content.String())
	}
}

func TestChatBadRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]string{
				"message": "Invalid model specified",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	_, err := client.Chat(ctx, ChatRequest{
		Model:    "nonexistent",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	if err == nil {
		t.Fatal("expected error")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "Invalid model") {
		t.Errorf("expected error about invalid model, got: %s", apiErr.Message)
	}
}

func TestStreamReaderEOF(t *testing.T) {
	// Stream ends without [DONE] marker
	sseData := `data: {"id":"1","choices":[{"delta":{"content":"Hello"}}]}

`

	reader := NewStreamReader(strings.NewReader(sseData))

	// First chunk
	delta, done, _, err := reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Error("unexpected done")
	}
	if delta != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", delta)
	}

	// EOF
	_, done, _, err = reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Error("expected done on EOF")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		errMsg    string
		retryable bool
	}{
		{"connection refused", true},
		{"connection reset", true},
		{"timeout exceeded", true},
		{"temporary failure", true},
		{"unknown error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = fmt.Errorf("%s", tt.errMsg)
			}
			if got := isRetryableError(err); got != tt.retryable {
				t.Errorf("isRetryableError(%q) = %v, want %v", tt.errMsg, got, tt.retryable)
			}
		})
	}
}

func TestIsRetryableStatus(t *testing.T) {
	retryable := []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway}
	notRetryable := []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError}

	for _, status := range retryable {
		if !isRetryableStatus(status) {
			t.Errorf("expected status %d to be retryable", status)
		}
	}
	for _, status := range notRetryable {
		if isRetryableStatus(status) {
			t.Errorf("expected status %d to not be retryable", status)
		}
	}
}

func TestChatWithOptionalParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify optional params were sent
		if req.Temperature == nil {
			t.Errorf("expected temperature 0.7, got nil")
		} else if *req.Temperature != 0.7 {
			t.Errorf("expected temperature 0.7, got %f", *req.Temperature)
		}
		if req.MaxTokens != 100 {
			t.Errorf("expected max_tokens 100, got %d", req.MaxTokens)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatResponse{
			ID:      "chatcmpl-123",
			Choices: []Choice{{Index: 0, Message: &Message{Role: "assistant", Content: "Hi"}, FinishReason: "stop"}},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	resp, err := client.Chat(ctx, ChatRequest{
		Model:       "test-model",
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: Temp(0.7),
		MaxTokens:   100,
	})

	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.ID != "chatcmpl-123" {
		t.Errorf("unexpected response id: %s", resp.ID)
	}
}

func TestStreamChatWithFinishReason(t *testing.T) {
	// Test that finish_reason is handled correctly
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := newTestClient(t, server)
	ctx := context.Background()

	var content strings.Builder
	var doneCount int

	err := client.StreamChat(ctx, ChatRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	}, func(delta string, done bool, usage *Usage) error {
		if done {
			doneCount++
		} else {
			content.WriteString(delta)
		}
		return nil
	})

	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	if content.String() != "Hi" {
		t.Errorf("expected 'Hi', got '%s'", content.String())
	}
	if doneCount != 1 {
		t.Errorf("expected done count 1, got %d", doneCount)
	}
}

// Benchmark streaming performance
func BenchmarkStreamChat(b *testing.B) {
	// Generate 100 chunks
	chunks := make([]string, 100)
	for i := range chunks {
		chunks[i] = fmt.Sprintf(`{"id":"chatcmpl-123","choices":[{"delta":{"content":"word%d "}}]}`, i)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain request body
		io.Copy(io.Discard, r.Body)

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := &config.LMStudioConfig{
		URL:        server.URL,
		Timeout:    30 * time.Second,
		MaxRetries: 0,
	}
	client := NewClient(cfg)
	ctx := context.Background()

	req := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.StreamChat(ctx, req, func(delta string, done bool, usage *Usage) error {
			return nil
		})
	}
}

// TestChatOmitsChatTemplateKwargs verifies the NoChatTemplateKwargs config gate:
// when set, chat_template_kwargs is stripped from the wire request (for hosted
// backends like Gemma on Infomaniak that reject the field); when unset, it is
// sent as before (vLLM/Qwen enable_thinking:false path).
func TestChatOmitsChatTemplateKwargs(t *testing.T) {
	for _, tc := range []struct {
		name string
		omit bool
		want bool // expect chat_template_kwargs present on the wire
	}{
		{"default sends it", false, true},
		{"gated strips it", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}))
			defer server.Close()

			client := NewClient(&config.LMStudioConfig{
				URL: server.URL, Timeout: 10 * time.Second, MaxRetries: 1,
				NoChatTemplateKwargs: tc.omit,
			})
			_, err := client.Chat(context.Background(), ChatRequest{
				Messages:           []Message{{Role: "user", Content: "hi"}},
				ChatTemplateKwargs: map[string]any{"enable_thinking": false},
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if got := strings.Contains(gotBody, "chat_template_kwargs"); got != tc.want {
				t.Errorf("chat_template_kwargs present=%v, want %v (body: %s)", got, tc.want, gotBody)
			}
		})
	}
}

// TestBackendCompatTransforms verifies the Infomaniak-profile request transforms:
// max_tokens→max_completion_tokens and reasoning_effort injection (gated by config).
func TestBackendCompatTransforms(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	// Infomaniak profile.
	c := NewClient(&config.LMStudioConfig{
		URL: server.URL, Timeout: 10 * time.Second, MaxRetries: 1,
		MaxCompletionTokens: true, ReasoningEffort: "none",
	})
	_, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 500,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(gotBody, `"max_completion_tokens":500`) {
		t.Errorf("expected max_completion_tokens:500, body: %s", gotBody)
	}
	if strings.Contains(gotBody, `"max_tokens"`) {
		t.Errorf("max_tokens must be dropped on the Infomaniak profile, body: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"reasoning_effort":"none"`) {
		t.Errorf("expected reasoning_effort:none, body: %s", gotBody)
	}

	// Default (Sparky) profile keeps max_tokens, no reasoning_effort.
	c2 := NewClient(&config.LMStudioConfig{URL: server.URL, Timeout: 10 * time.Second, MaxRetries: 1})
	_, _ = c2.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 500})
	if !strings.Contains(gotBody, `"max_tokens":500`) || strings.Contains(gotBody, "reasoning_effort") {
		t.Errorf("default profile should keep max_tokens + omit reasoning_effort, body: %s", gotBody)
	}
}

// TestRerank verifies the dedicated reranker call + relevance ordering.
func TestRerank(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Out-of-order on purpose — Rerank must sort by relevance desc.
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.2},{"index":2,"relevance_score":0.9},{"index":1,"relevance_score":0.5}]}`))
	}))
	defer server.Close()

	c := NewClient(&config.LMStudioConfig{URL: "http://unused", RerankURL: server.URL, RerankModel: "bge-reranker-v2-m3"})
	if !c.RerankConfigured() {
		t.Fatal("RerankConfigured should be true")
	}
	order, err := c.Rerank(context.Background(), "q", []string{"a", "b", "c"}, 0)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	want := []int{2, 1, 0}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order = %v, want %v", order, want)
	}
	// Not configured → RerankConfigured false.
	if NewClient(&config.LMStudioConfig{URL: "x"}).RerankConfigured() {
		t.Error("RerankConfigured should be false without rerank_url/model")
	}
}

// TestChatRequestDecodingParamsWire is the WP14 safety net for the pointer
// refactor: it proves that a pinned Temperature:0 now actually reaches the wire
// (the whole point of switching Temperature to *float64), that Seed/TopP
// serialize when set, and that nil pointers are omitted so callers that don't
// pin a parameter still get the backend default.
func TestChatRequestDecodingParamsWire(t *testing.T) {
	marshal := func(req ChatRequest) string {
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		return string(b)
	}

	// Temperature:0 (a non-nil *0.0) MUST appear on the wire — this is the bug
	// the whole WP14 change fixes.
	if got := marshal(ChatRequest{Temperature: Temp(0)}); !strings.Contains(got, `"temperature":0`) {
		t.Errorf("Temp(0) must serialize temperature:0, got %s", got)
	}

	// A nil Temperature (zero-value ChatRequest) MUST omit the field so
	// unpinned callers keep hitting the backend default.
	if got := marshal(ChatRequest{}); strings.Contains(got, `"temperature"`) {
		t.Errorf("nil temperature must be omitted, got %s", got)
	}

	// Seed set → present; nil → omitted.
	if got := marshal(ChatRequest{Seed: SeedOf(42)}); !strings.Contains(got, `"seed":42`) {
		t.Errorf("SeedOf(42) must serialize seed:42, got %s", got)
	}
	if got := marshal(ChatRequest{}); strings.Contains(got, `"seed"`) {
		t.Errorf("nil seed must be omitted, got %s", got)
	}

	// TopP set → present; nil → omitted.
	if got := marshal(ChatRequest{TopP: Temp(1)}); !strings.Contains(got, `"top_p":1`) {
		t.Errorf("Temp(1) must serialize top_p:1, got %s", got)
	}
	if got := marshal(ChatRequest{}); strings.Contains(got, `"top_p"`) {
		t.Errorf("nil top_p must be omitted, got %s", got)
	}
}
