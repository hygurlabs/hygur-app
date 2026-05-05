package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

func TestModelsHandler_ListModels(t *testing.T) {
	// Create a mock LM Studio server that returns a list of models
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"object": "list",
				"data": [
					{"id": "llama-3.2-3b-instruct", "object": "model", "owned_by": "local"},
					{"id": "qwen2.5-7b-instruct", "object": "model", "owned_by": "local"}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLMStudio.Close()

	// Create LLM client pointing to mock server
	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())
	logger := zerolog.Nop()

	handler := NewModelsHandler(client, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Check Content-Type header
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got '%s'", contentType)
	}

	var resp ModelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Models) != 2 {
		t.Errorf("expected 2 models, got %d", len(resp.Models))
	}

	// Verify first model
	if resp.Models[0].ID != "llama-3.2-3b-instruct" {
		t.Errorf("expected first model ID 'llama-3.2-3b-instruct', got '%s'", resp.Models[0].ID)
	}
	if resp.Models[0].Name != "llama-3.2-3b-instruct" {
		t.Errorf("expected first model Name 'llama-3.2-3b-instruct', got '%s'", resp.Models[0].Name)
	}

	// Verify second model
	if resp.Models[1].ID != "qwen2.5-7b-instruct" {
		t.Errorf("expected second model ID 'qwen2.5-7b-instruct', got '%s'", resp.Models[1].ID)
	}
}

func TestModelsHandler_EmptyModels(t *testing.T) {
	// Create a mock LM Studio server that returns an empty list
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object": "list", "data": []}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLMStudio.Close()

	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())
	logger := zerolog.Nop()

	handler := NewModelsHandler(client, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp ModelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Models == nil {
		t.Error("expected models array to be non-nil")
	}

	if len(resp.Models) != 0 {
		t.Errorf("expected 0 models, got %d", len(resp.Models))
	}
}

func TestModelsHandler_LMStudioDown(t *testing.T) {
	// Create a mock server that returns an error
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer mockLMStudio.Close()

	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())
	logger := zerolog.Nop()

	handler := NewModelsHandler(client, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errResp.Error.Code != "LM_STUDIO_UNREACHABLE" {
		t.Errorf("expected error code 'LM_STUDIO_UNREACHABLE', got '%s'", errResp.Error.Code)
	}

	if errResp.Error.Message != "Cannot connect to LM Studio" {
		t.Errorf("expected error message 'Cannot connect to LM Studio', got '%s'", errResp.Error.Message)
	}
}

func TestModelsHandler_ConnectionRefused(t *testing.T) {
	// Create a client pointing to a port that nothing is listening on
	client := llm.NewClientWithHTTP("http://127.0.0.1:59999", 2*time.Second, 0, http.DefaultClient)
	logger := zerolog.Nop()

	handler := NewModelsHandler(client, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	// Use a context with timeout to ensure the test doesn't hang
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errResp.Error.Code != "LM_STUDIO_UNREACHABLE" {
		t.Errorf("expected error code 'LM_STUDIO_UNREACHABLE', got '%s'", errResp.Error.Code)
	}
}

func TestModelsHandler_NoLLMClient(t *testing.T) {
	// Handler with nil LLM client
	logger := zerolog.Nop()
	handler := NewModelsHandler(nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errResp.Error.Code != "LM_STUDIO_UNREACHABLE" {
		t.Errorf("expected error code 'LM_STUDIO_UNREACHABLE', got '%s'", errResp.Error.Code)
	}
}

func TestModelsHandler_ResponseFormat(t *testing.T) {
	// Verify the response matches the expected JSON structure per api-contract.md
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"object": "list",
				"data": [
					{"id": "test-model", "object": "model", "owned_by": "local"}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLMStudio.Close()

	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())
	logger := zerolog.Nop()

	handler := NewModelsHandler(client, logger)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Parse as raw JSON to verify structure
	var rawResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&rawResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check that response has "models" key (not "data" like OpenAI format)
	if _, ok := rawResp["models"]; !ok {
		t.Error("response should have 'models' key")
	}

	// Check that response does NOT have "object" key (OpenAI format)
	if _, ok := rawResp["object"]; ok {
		t.Error("response should not have 'object' key (OpenAI format)")
	}

	// Check that response does NOT have "data" key (OpenAI format)
	if _, ok := rawResp["data"]; ok {
		t.Error("response should not have 'data' key (OpenAI format)")
	}

	// Verify models array structure
	models, ok := rawResp["models"].([]interface{})
	if !ok {
		t.Fatal("'models' should be an array")
	}

	if len(models) != 1 {
		t.Errorf("expected 1 model, got %d", len(models))
	}

	model, ok := models[0].(map[string]interface{})
	if !ok {
		t.Fatal("model should be an object")
	}

	// Check required fields per api-contract.md
	if _, ok := model["id"]; !ok {
		t.Error("model should have 'id' field")
	}
	if _, ok := model["name"]; !ok {
		t.Error("model should have 'name' field")
	}
}
