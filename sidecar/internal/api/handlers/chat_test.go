package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// createMockLLMServer creates a mock LM Studio server for testing.
func createMockLLMServer(t *testing.T, chunks []string, usage *llm.Usage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected ResponseWriter to be a Flusher")
		}

		for _, chunk := range chunks {
			sseChunk := fmt.Sprintf(`{"id":"chatcmpl-123","choices":[{"delta":{"content":"%s"}}]}`, chunk)
			fmt.Fprintf(w, "data: %s\n\n", sseChunk)
			flusher.Flush()
		}

		// Send finish reason chunk with usage
		if usage != nil {
			finishChunk := fmt.Sprintf(`{"id":"chatcmpl-123","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
			fmt.Fprintf(w, "data: %s\n\n", finishChunk)
			flusher.Flush()
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// createMockLLMClient creates an LLM client pointing to a mock server.
func createMockLLMClient(serverURL string) *llm.Client {
	return llm.NewClientWithHTTP(serverURL, 5*time.Second, 0, http.DefaultClient)
}

// parseSSEEvents parses SSE events from a response body.
func parseSSEEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatalf("failed to parse SSE event: %v, data: %s", err, data)
			}
			events = append(events, event)
		}
	}
	return events
}

func TestChatHandler_StreamingSuccess(t *testing.T) {
	// Create mock LM Studio server
	chunks := []string{"Hello", " ", "world", "!"}
	usage := &llm.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}
	mockServer := createMockLLMServer(t, chunks, usage)
	defer mockServer.Close()

	// Create handler
	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	handler := NewChatHandler(client, logger)

	// Create request
	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Check response headers
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", contentType)
	}

	cacheControl := rec.Header().Get("Cache-Control")
	if cacheControl != "no-cache" {
		t.Errorf("expected Cache-Control 'no-cache', got '%s'", cacheControl)
	}

	// Parse SSE events
	events := parseSSEEvents(t, rec.Body.String())
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (chunks + done), got %d", len(events))
	}

	// Check that we received delta events
	var receivedContent strings.Builder
	var doneEvent map[string]any
	for _, event := range events {
		if done, ok := event["done"].(bool); ok && done {
			doneEvent = event
		} else if delta, ok := event["delta"].(string); ok {
			receivedContent.WriteString(delta)
		}
	}

	if receivedContent.String() != "Hello world!" {
		t.Errorf("expected 'Hello world!', got '%s'", receivedContent.String())
	}

	// Check final event has usage
	if doneEvent == nil {
		t.Fatal("expected done event")
	}
	usageData, ok := doneEvent["usage"].(map[string]any)
	if !ok {
		t.Fatal("expected usage in done event")
	}
	if int(usageData["total_tokens"].(float64)) != 15 {
		t.Errorf("expected total_tokens 15, got %v", usageData["total_tokens"])
	}
}

func TestChatHandler_ValidationError_EmptyMessages(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewChatHandler(nil, logger)

	// Request with empty messages
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

func TestChatHandler_InvalidJSON(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewChatHandler(nil, logger)

	// Invalid JSON request
	reqBody := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errObj["code"] != "BAD_REQUEST" {
		t.Errorf("expected code 'BAD_REQUEST', got '%s'", errObj["code"])
	}
}

func TestChatHandler_MethodNotAllowed(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewChatHandler(nil, logger)

	// GET request should fail
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestChatHandler_NoLLMClient(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewChatHandler(nil, logger) // nil client

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}
}

func TestChatHandler_LLMError(t *testing.T) {
	// Create a mock server that returns an error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"Model not loaded"}}`))
	}))
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	handler := NewChatHandler(client, logger)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Even on LLM error, we should get SSE format since headers are set
	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", contentType)
	}

	// Parse SSE events - should contain an error event
	events := parseSSEEvents(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("expected at least one SSE event")
	}

	// Find error event
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

func TestChatHandler_ClientDisconnect(t *testing.T) {
	// Create a mock server that sends chunks slowly
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		for i := 0; i < 10; i++ {
			select {
			case <-r.Context().Done():
				// Client disconnected
				return
			default:
				chunk := fmt.Sprintf(`{"id":"chatcmpl-123","choices":[{"delta":{"content":"word%d "}}]}`, i)
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				flusher.Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	handler := NewChatHandler(client, logger)

	// Create request with a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	// Start handler in goroutine
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	// Cancel context after a short delay
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait for handler to complete
	select {
	case <-done:
		// Handler completed successfully
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not complete after context cancellation")
	}
}

func TestChatHandler_ErrorMidStream(t *testing.T) {
	// Create a mock server that sends some data then fails
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// Send one valid chunk
		chunk := `{"id":"chatcmpl-123","choices":[{"delta":{"content":"Hello"}}]}`
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		flusher.Flush()

		// Send invalid JSON to trigger error
		fmt.Fprintf(w, "data: {invalid json}\n\n")
		flusher.Flush()
	}))
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	handler := NewChatHandler(client, logger)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should still be SSE format
	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", contentType)
	}

	// Parse events
	events := parseSSEEvents(t, rec.Body.String())

	// Should have at least one delta event and one error event
	var foundDelta, foundError bool
	for _, event := range events {
		if delta, ok := event["delta"].(string); ok && delta != "" {
			foundDelta = true
		}
		if eventType, ok := event["type"].(string); ok && eventType == "error" {
			foundError = true
		}
	}

	if !foundDelta {
		t.Error("expected at least one delta event before error")
	}
	if !foundError {
		t.Error("expected error event for mid-stream failure")
	}
}

func TestChatHandler_WithOptionalParams(t *testing.T) {
	// Create mock server that verifies parameters
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
	handler := NewChatHandler(client, logger)

	reqBody := `{
		"messages":[{"role":"user","content":"Hello"}],
		"model":"test-model",
		"stream":true,
		"temperature":0.7,
		"max_tokens":100
	}`
	req := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify parameters were passed
	if receivedReq.Model != "test-model" {
		t.Errorf("expected model 'test-model', got '%s'", receivedReq.Model)
	}
	if receivedReq.Temperature == nil {
		t.Errorf("expected temperature 0.7, got nil")
	} else if *receivedReq.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %f", *receivedReq.Temperature)
	}
	if receivedReq.MaxTokens != 100 {
		t.Errorf("expected max_tokens 100, got %d", receivedReq.MaxTokens)
	}
}

func TestChatHandler_SSEFormat(t *testing.T) {
	// Create mock server with specific chunks
	chunks := []string{"A", "B", "C"}
	mockServer := createMockLLMServer(t, chunks, nil)
	defer mockServer.Close()

	client := createMockLLMClient(mockServer.URL)
	logger := zerolog.Nop()
	handler := NewChatHandler(client, logger)

	reqBody := `{"messages":[{"role":"user","content":"Hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify SSE format: each event should be "data: {...}\n\n"
	body := rec.Body.String()
	lines := strings.Split(body, "\n\n")

	// Filter out empty lines
	var nonEmpty []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}

	// Each non-empty segment should start with "data: "
	for _, line := range nonEmpty {
		if !strings.HasPrefix(line, "data: ") {
			t.Errorf("expected line to start with 'data: ', got: %s", line)
		}
	}
}
