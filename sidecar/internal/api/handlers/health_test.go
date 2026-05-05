package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

func TestHealthHandler_LMStudioConnected(t *testing.T) {
	// Create a mock LM Studio server that responds successfully
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLMStudio.Close()

	// Create LLM client pointing to mock server
	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())

	handler := NewHealthHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp.Status)
	}

	if resp.LMStudio != "connected" {
		t.Errorf("expected lm_studio 'connected', got '%s'", resp.LMStudio)
	}

	if resp.Version == "" {
		t.Error("expected version to be set")
	}

	// Content-Type header check
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got '%s'", contentType)
	}
}

func TestHealthHandler_LMStudioDisconnected(t *testing.T) {
	// Create a mock server that returns an error
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer mockLMStudio.Close()

	client := llm.NewClientWithHTTP(mockLMStudio.URL, 5*time.Second, 0, mockLMStudio.Client())

	handler := NewHealthHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got '%s'", resp.Status)
	}

	if resp.LMStudio != "disconnected" {
		t.Errorf("expected lm_studio 'disconnected', got '%s'", resp.LMStudio)
	}
}

func TestHealthHandler_NoLLMClient(t *testing.T) {
	// Handler with nil LLM client
	handler := NewHealthHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got '%s'", resp.Status)
	}

	if resp.LMStudio != "disconnected" {
		t.Errorf("expected lm_studio 'disconnected', got '%s'", resp.LMStudio)
	}
}

func TestHealthHandler_TimeoutRespected(t *testing.T) {
	// Create a server that delays longer than the 2-second timeout
	mockLMStudio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep for 3 seconds, longer than the 2-second timeout
		select {
		case <-r.Context().Done():
			// Context was cancelled (timeout)
			return
		case <-time.After(3 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer mockLMStudio.Close()

	// Create client with a longer timeout than the handler uses
	// The handler enforces 2s, so the client's timeout should not matter
	client := llm.NewClientWithHTTP(mockLMStudio.URL, 10*time.Second, 0, mockLMStudio.Client())

	handler := NewHealthHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	// Should complete within ~2 seconds (plus some buffer)
	if elapsed > 3*time.Second {
		t.Errorf("handler took too long: %v (expected < 3s due to 2s timeout)", elapsed)
	}

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should be disconnected because the ping timed out
	if resp.LMStudio != "disconnected" {
		t.Errorf("expected lm_studio 'disconnected' due to timeout, got '%s'", resp.LMStudio)
	}

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got '%s'", resp.Status)
	}
}

func TestHealthHandler_UptimeSeconds(t *testing.T) {
	handler := NewHealthHandler(nil)

	// Set start time to 10 seconds ago
	handler.SetStartTime(time.Now().Add(-10 * time.Second))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Uptime should be approximately 10 seconds (allow some tolerance)
	if resp.UptimeSeconds < 9 || resp.UptimeSeconds > 12 {
		t.Errorf("expected uptime_seconds ~10, got %d", resp.UptimeSeconds)
	}
}

func TestHealthHandler_ConnectionRefused(t *testing.T) {
	// Create a client pointing to a port that nothing is listening on
	client := llm.NewClientWithHTTP("http://127.0.0.1:59999", 2*time.Second, 0, http.DefaultClient)

	handler := NewHealthHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	// Use a context with timeout to ensure the test doesn't hang
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	handler.ServeHTTP(rec, req)

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.LMStudio != "disconnected" {
		t.Errorf("expected lm_studio 'disconnected', got '%s'", resp.LMStudio)
	}

	if resp.Status != "degraded" {
		t.Errorf("expected status 'degraded', got '%s'", resp.Status)
	}
}
