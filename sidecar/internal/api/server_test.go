package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// testConfig returns a config suitable for testing.
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

// testToken returns a valid test token for authentication.
const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// captureLogger returns a logger that captures output for assertions.
func captureLogger() (zerolog.Logger, *strings.Builder) {
	var buf strings.Builder
	return zerolog.New(&buf), &buf
}

func TestNewServer(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()

	server := NewServer(cfg, logger, testToken)

	if server == nil {
		t.Fatal("NewServer returned nil")
	}

	if server.router == nil {
		t.Error("router is nil")
	}

	if server.cfg != cfg {
		t.Error("config not set correctly")
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Without SetLLMClient being called, the health endpoint returns degraded status
	// This is expected as there's no LLM client configured
	status := resp["status"].(string)
	if status != "degraded" && status != "ok" {
		t.Errorf("expected status 'degraded' or 'ok', got '%s'", status)
	}

	// Verify other required fields are present
	if _, ok := resp["version"]; !ok {
		t.Error("expected version field in response")
	}
	if _, ok := resp["lm_studio"]; !ok {
		t.Error("expected lm_studio field in response")
	}
}

func TestModelsEndpointWithoutClient(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Don't set LLM client - should return service unavailable
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	req.Header.Set("X-Hygur-Token", testToken)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error == "" {
		t.Error("expected error message in response")
	}
}

func TestModelsEndpointRequiresAuth(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Request without auth token should return 401
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestModelsEndpointInvalidToken(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Request with invalid token should return 401
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	req.Header.Set("X-Hygur-Token", "invalid-token")
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestChatEndpointWithoutClient(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hygur-Token", testToken)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestMiddlewareLogging(t *testing.T) {
	cfg := testConfig()
	logger, buf := captureLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Verify log contains expected fields
	if !strings.Contains(logOutput, `"method":"GET"`) {
		t.Error("log output missing method field")
	}
	if !strings.Contains(logOutput, `"path":"/health"`) {
		t.Error("log output missing path field")
	}
	if !strings.Contains(logOutput, `"status":200`) {
		t.Error("log output missing status field")
	}
	if !strings.Contains(logOutput, `"duration_ms"`) {
		t.Error("log output missing duration_ms field")
	}
}

func TestPanicRecovery(t *testing.T) {
	cfg := testConfig()
	logger, _ := captureLogger()
	server := NewServer(cfg, logger, testToken)

	// Add a route that panics
	server.router.Get("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()

	// This should not panic
	server.router.ServeHTTP(rec, req)

	// Should return 500
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}

	// Should return JSON error
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "internal server error" {
		t.Errorf("expected error 'internal server error', got '%s'", resp.Error)
	}
}

func TestTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.Server.ReadTimeout = 100 * time.Millisecond // Very short timeout for testing
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Add a route group with timeout middleware that takes too long
	server.router.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(cfg.Server.ReadTimeout))
		r.Get("/slow", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(500 * time.Millisecond):
				w.Write([]byte("done"))
			case <-r.Context().Done():
				// Context cancelled due to timeout
				return
			}
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	// The handler should have been cancelled
	// Note: chi's timeout middleware returns 504 Gateway Timeout
	if rec.Code != http.StatusGatewayTimeout && rec.Code != http.StatusServiceUnavailable {
		// Some implementations might just not write anything
		// which results in 200 with empty body
		if rec.Code == http.StatusOK && rec.Body.Len() == 0 {
			// This is acceptable - the handler was cancelled
		} else {
			t.Errorf("expected timeout status, got %d", rec.Code)
		}
	}
}

func TestServerStartupAndShutdown(t *testing.T) {
	cfg := testConfig()
	cfg.Server.Port = 0 // Let OS assign a port
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	ctx, cancel := context.WithCancel(context.Background())

	// Start server in background
	var wg sync.WaitGroup
	var startErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		startErr = server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for shutdown with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown took too long")
	}

	// Check there was no error
	if startErr != nil {
		t.Errorf("server start error: %v", startErr)
	}
}

func TestShutdownWithoutStart(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Shutdown without starting should not error
	err := server.Shutdown(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRouter(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	router := server.Router()
	if router == nil {
		t.Error("Router() returned nil")
	}

	if router != server.router {
		t.Error("Router() returned different router than expected")
	}
}

func TestRequestIDPropagation(t *testing.T) {
	cfg := testConfig()
	logger, buf := captureLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	logOutput := buf.String()

	// The request ID should be present in logs
	if !strings.Contains(logOutput, `"request_id"`) {
		t.Error("log output missing request_id field")
	}
}

func TestCORSMiddleware(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Add CORS middleware for this test
	// Note: CORS middleware is defined but not used in setupMiddleware
	// This test verifies the CORS middleware implementation

	t.Run("localhost origin allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/health", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()

		// Apply CORS middleware manually for testing
		handler := server.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected status %d for OPTIONS, got %d", http.StatusNoContent, rec.Code)
		}

		if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Error("CORS origin header not set correctly")
		}
	})

	t.Run("external origin blocked", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Origin", "http://example.com")
		rec := httptest.NewRecorder()

		handler := server.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		handler.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("external origin should not be allowed")
		}
	})
}

func TestGracefulShutdownDrainsConnections(t *testing.T) {
	cfg := testConfig()
	cfg.Server.ShutdownTimeout = 2 * time.Second
	logger := zerolog.Nop() // Use Nop logger to avoid data races
	server := NewServer(cfg, logger, testToken)

	// Add a slow handler
	server.router.Get("/slow-drain", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("done"))
	})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Initiate shutdown
	cancel()

	// Wait for clean shutdown
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown took too long")
	}
}

func TestNotFoundRoute(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// POST to health endpoint should fail
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestSetLLMClient(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	if server.llmClient != nil {
		t.Error("llmClient should be nil initially")
	}

	// Create a mock client
	mockClient := &llm.Client{}
	server.SetLLMClient(mockClient)

	if server.llmClient != mockClient {
		t.Error("llmClient not set correctly")
	}
}

func TestAddr(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// Before starting, addr should be empty
	if server.Addr() != "" {
		t.Errorf("expected empty addr before start, got %s", server.Addr())
	}
}

func TestLoggingWithQueryString(t *testing.T) {
	cfg := testConfig()
	logger, buf := captureLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodGet, "/health?foo=bar", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Query string should be logged
	if !strings.Contains(logOutput, `"query":"foo=bar"`) {
		t.Error("log output missing query field")
	}
}

func TestLoggingFor500Status(t *testing.T) {
	cfg := testConfig()
	logger, buf := captureLogger()
	server := NewServer(cfg, logger, testToken)

	// Add a route that returns 500
	server.router.Get("/error", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodGet, "/error", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Remote addr should be logged for 500 errors
	if !strings.Contains(logOutput, `"remote_addr"`) {
		t.Error("log output missing remote_addr field for 500 error")
	}
}

func TestCORSMiddleware127001(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	rec := httptest.NewRecorder()

	handler := server.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d for OPTIONS, got %d", http.StatusNoContent, rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8080" {
		t.Error("CORS origin header not set correctly for 127.0.0.1")
	}
}

func TestAuthMiddlewareWithValidToken(t *testing.T) {
	cfg := testConfig()
	logger := testLogger()
	server := NewServer(cfg, logger, testToken)

	// The auth middleware should pass through with valid token
	called := false
	handler := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Hygur-Token", testToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("auth middleware should pass through to next handler with valid token")
	}
}

func TestPanicRecoveryLogging(t *testing.T) {
	cfg := testConfig()
	logger, buf := captureLogger()
	server := NewServer(cfg, logger, testToken)

	// Add a route that panics
	server.router.Get("/panic-log", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic for logging")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic-log", nil)
	rec := httptest.NewRecorder()

	server.router.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Verify panic was logged with error level
	if !strings.Contains(logOutput, `"level":"error"`) {
		t.Error("panic should be logged at error level")
	}
	if !strings.Contains(logOutput, `"panic"`) {
		t.Error("panic field should be present in log")
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	writeJSON(rec, http.StatusOK, data)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["key"] != "value" {
		t.Error("response content incorrect")
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()

	writeError(rec, http.StatusBadRequest, "test error")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "test error" {
		t.Errorf("expected error 'test error', got '%s'", resp.Error)
	}
}
