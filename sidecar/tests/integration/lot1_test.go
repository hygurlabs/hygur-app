// Package integration provides end-to-end integration tests for Hygur sidecar.
// These tests verify the complete flow of Lot 1 functionality.
package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/api"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// testToken is a valid 64-character hex token for testing.
const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testConfig returns a config suitable for integration testing.
func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:            "127.0.0.1",
			Port:            0, // Use any available port
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 5 * time.Second,
		},
		LMStudio: config.LMStudioConfig{
			URL:          "http://localhost:1234",
			ModelDefault: "test-model",
			Timeout:      30 * time.Second,
			MaxRetries:   1,
		},
	}
}

// testLogger returns a logger that discards output for clean test output.
func testLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// mockLMStudioServer creates a mock LM Studio server for testing.
func mockLMStudioServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
			// Mock models response
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list",
				"data": []map[string]interface{}{
					{"id": "llama-3.2-3b-instruct", "object": "model", "owned_by": "local"},
					{"id": "qwen2.5-7b-instruct", "object": "model", "owned_by": "local"},
				},
			})

		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			// Mock streaming chat response
			var req map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			if req["stream"] == true {
				// SSE streaming response
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")

				flusher, ok := w.(http.Flusher)
				if !ok {
					http.Error(w, "streaming not supported", http.StatusInternalServerError)
					return
				}

				// Send a few chunks
				chunks := []string{"Hello", ", ", "this ", "is ", "a ", "test ", "response."}
				for _, chunk := range chunks {
					event := map[string]interface{}{
						"id":      "chatcmpl-test",
						"object":  "chat.completion.chunk",
						"created": time.Now().Unix(),
						"model":   "test-model",
						"choices": []map[string]interface{}{
							{
								"index": 0,
								"delta": map[string]interface{}{
									"content": chunk,
								},
								"finish_reason": nil,
							},
						},
					}
					data, _ := json.Marshal(event)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}

				// Send final event with usage
				finalEvent := map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   "test-model",
					"choices": []map[string]interface{}{
						{
							"index":         0,
							"delta":         map[string]interface{}{},
							"finish_reason": "stop",
						},
					},
					"usage": map[string]int{
						"prompt_tokens":     10,
						"completion_tokens": 7,
						"total_tokens":      17,
					},
				}
				data, _ := json.Marshal(finalEvent)
				fmt.Fprintf(w, "data: %s\n\n", data)
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
			} else {
				// Non-streaming response
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"created": time.Now().Unix(),
					"model":   "test-model",
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Hello, this is a test response.",
							},
							"finish_reason": "stop",
						},
					},
					"usage": map[string]int{
						"prompt_tokens":     10,
						"completion_tokens": 7,
						"total_tokens":      17,
					},
				})
			}

		default:
			http.NotFound(w, r)
		}
	}))
}

// setupTestServer creates a fully configured test server with a mock LM Studio.
func setupTestServer(t *testing.T) (*api.Server, *httptest.Server) {
	mockLMS := mockLMStudioServer(t)

	cfg := testConfig()
	cfg.LMStudio.URL = mockLMS.URL

	logger := testLogger()
	server := api.NewServer(cfg, logger, testToken)

	// Create LLM client pointing to mock server
	llmClient := llm.NewClientWithHTTP(
		mockLMS.URL,
		cfg.LMStudio.Timeout,
		cfg.LMStudio.MaxRetries,
		&http.Client{Timeout: cfg.LMStudio.Timeout},
	)
	server.SetLLMClient(llmClient)

	return server, mockLMS
}

// TestLot1EndToEnd tests the complete Lot 1 flow.
func TestLot1EndToEnd(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	t.Run("complete flow", func(t *testing.T) {
		// 1. Test GET /health
		t.Run("health check", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("health check: expected status %d, got %d", http.StatusOK, rec.Code)
			}

			var resp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("health check: failed to decode response: %v", err)
			}

			if resp["status"] != "ok" {
				t.Errorf("health check: expected status 'ok', got '%s'", resp["status"])
			}

			if resp["lm_studio"] != "connected" {
				t.Errorf("health check: expected lm_studio 'connected', got '%s'", resp["lm_studio"])
			}
		})

		// 2. Test GET /models with auth
		t.Run("list models", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/models", nil)
			req.Header.Set("X-Hygur-Token", testToken)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("list models: expected status %d, got %d", http.StatusOK, rec.Code)
			}

			var resp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("list models: failed to decode response: %v", err)
			}

			models, ok := resp["models"].([]interface{})
			if !ok {
				t.Fatal("list models: expected 'models' array in response")
			}

			if len(models) != 2 {
				t.Errorf("list models: expected 2 models, got %d", len(models))
			}
		})

		// 3. Test POST /chat with streaming
		t.Run("chat streaming", func(t *testing.T) {
			reqBody := `{
				"messages": [
					{"role": "user", "content": "Hello!"}
				],
				"stream": true
			}`

			req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Hygur-Token", testToken)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("chat streaming: expected status %d, got %d", http.StatusOK, rec.Code)
				t.Errorf("response body: %s", rec.Body.String())
				return
			}

			contentType := rec.Header().Get("Content-Type")
			if contentType != "text/event-stream" {
				t.Errorf("chat streaming: expected Content-Type 'text/event-stream', got '%s'", contentType)
			}

			// Parse SSE events
			scanner := bufio.NewScanner(rec.Body)
			var events []map[string]interface{}
			var lastDoneEvent map[string]interface{}

			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					data := strings.TrimPrefix(line, "data: ")
					if data == "[DONE]" {
						continue
					}

					var event map[string]interface{}
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						continue
					}
					events = append(events, event)

					if done, ok := event["done"].(bool); ok && done {
						lastDoneEvent = event
					}
				}
			}

			if len(events) == 0 {
				t.Error("chat streaming: expected at least one SSE event")
			}

			// Verify we got a done event
			if lastDoneEvent == nil {
				t.Error("chat streaming: expected a final 'done' event")
			}
		})
	})
}

// TestHealthEndpointPublic verifies that /health is accessible without authentication.
func TestHealthEndpointPublic(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	tests := []struct {
		name           string
		token          string
		expectedStatus int
	}{
		{
			name:           "no token",
			token:          "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid token",
			token:          "invalid",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "valid token",
			token:          testToken,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			if tc.token != "" {
				req.Header.Set("X-Hygur-Token", tc.token)
			}
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, rec.Code)
			}

			// Verify response structure
			var resp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			// Required fields per api-contract.md
			requiredFields := []string{"status", "version", "lm_studio"}
			for _, field := range requiredFields {
				if _, ok := resp[field]; !ok {
					t.Errorf("missing required field: %s", field)
				}
			}
		})
	}
}

// TestEndpointsRequireAuth verifies that /models and /chat return 401 without token.
func TestEndpointsRequireAuth(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	protectedEndpoints := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/models", ""},
		{http.MethodPost, "/chat", `{"messages": [{"role": "user", "content": "test"}]}`},
	}

	for _, ep := range protectedEndpoints {
		t.Run(fmt.Sprintf("%s %s without token", ep.method, ep.path), func(t *testing.T) {
			var body io.Reader
			if ep.body != "" {
				body = strings.NewReader(ep.body)
			}

			req := httptest.NewRequest(ep.method, ep.path, body)
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			// No X-Hygur-Token header

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
			}

			// Verify error response format per api-contract.md
			var resp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if _, ok := resp["code"]; !ok {
				if _, ok := resp["error"]; !ok {
					t.Error("expected 'code' or 'error' field in response")
				}
			}
		})

		t.Run(fmt.Sprintf("%s %s with invalid token", ep.method, ep.path), func(t *testing.T) {
			var body io.Reader
			if ep.body != "" {
				body = strings.NewReader(ep.body)
			}

			req := httptest.NewRequest(ep.method, ep.path, body)
			if ep.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("X-Hygur-Token", "invalid-token-here")

			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
			}
		})
	}
}

// TestModelsEndpointWithLMStudio tests the /models endpoint with a working LM Studio mock.
func TestModelsEndpointWithLMStudio(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	req.Header.Set("X-Hygur-Token", testToken)
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	models, ok := resp["models"].([]interface{})
	if !ok {
		t.Fatal("expected 'models' array in response")
	}

	// Verify model structure per api-contract.md
	for i, m := range models {
		model, ok := m.(map[string]interface{})
		if !ok {
			t.Errorf("model %d: expected object", i)
			continue
		}

		if _, ok := model["id"]; !ok {
			t.Errorf("model %d: missing 'id' field", i)
		}
		if _, ok := model["name"]; !ok {
			t.Errorf("model %d: missing 'name' field", i)
		}
	}
}

// TestChatStreamingFlow tests the complete chat streaming flow.
func TestChatStreamingFlow(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	reqBody := `{
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Say hello!"}
		],
		"stream": true,
		"temperature": 0.7,
		"max_tokens": 100
	}`

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hygur-Token", testToken)
	rec := httptest.NewRecorder()

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	// Verify SSE headers
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type 'text/event-stream', got '%s'", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected Cache-Control 'no-cache', got '%s'", cc)
	}

	// Parse and validate SSE events
	body := rec.Body.String()
	lines := strings.Split(body, "\n")

	var gotDelta, gotDone bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if _, ok := event["delta"]; ok {
			gotDelta = true
		}
		if done, ok := event["done"].(bool); ok && done {
			gotDone = true
		}
	}

	if !gotDelta {
		t.Error("expected at least one delta event")
	}
	if !gotDone {
		t.Error("expected a done event")
	}
}

// TestChatValidation tests request validation for the /chat endpoint.
func TestChatValidation(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	tests := []struct {
		name           string
		body           string
		expectedStatus int
	}{
		{
			name:           "empty messages",
			body:           `{"messages": []}`,
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "invalid json",
			body:           `{invalid}`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "missing messages",
			body:           `{"stream": true}`,
			expectedStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Hygur-Token", testToken)
			rec := httptest.NewRecorder()

			server.Router().ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d: %s", tc.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestHealthWithLMStudioDown tests health endpoint when LM Studio is unreachable.
func TestHealthWithLMStudioDown(t *testing.T) {
	cfg := testConfig()
	cfg.LMStudio.URL = "http://localhost:59999" // Non-existent server

	logger := testLogger()
	server := api.NewServer(cfg, logger, testToken)

	// Create LLM client pointing to non-existent server with short timeout
	llmClient := llm.NewClientWithHTTP(
		cfg.LMStudio.URL,
		100*time.Millisecond,
		0,
		&http.Client{Timeout: 100 * time.Millisecond},
	)
	server.SetLLMClient(llmClient)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	// Use context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	server.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "degraded" {
		t.Errorf("expected status 'degraded' when LM Studio is down, got '%s'", resp["status"])
	}

	if resp["lm_studio"] != "disconnected" {
		t.Errorf("expected lm_studio 'disconnected', got '%s'", resp["lm_studio"])
	}
}

// TestConcurrentRequests tests handling of concurrent requests.
func TestConcurrentRequests(t *testing.T) {
	server, mockLMS := setupTestServer(t)
	defer mockLMS.Close()

	const numRequests = 10
	done := make(chan bool, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			server.Router().ServeHTTP(rec, req)
			done <- rec.Code == http.StatusOK
		}()
	}

	successCount := 0
	for i := 0; i < numRequests; i++ {
		if <-done {
			successCount++
		}
	}

	if successCount != numRequests {
		t.Errorf("expected %d successful requests, got %d", numRequests, successCount)
	}
}
