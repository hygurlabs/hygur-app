package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

func TestAuthMiddleware_MissingToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	server := NewServer(cfg, logger, "validtoken123")

	// Create a test handler that should not be called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with auth middleware
	handler := server.authMiddleware(testHandler)

	// Create request without token
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	// Handler should not have been called
	if handlerCalled {
		t.Error("handler was called but should have been blocked by auth middleware")
	}

	// Check response body contains expected error
	body := rec.Body.String()
	if body != `{"code":"UNAUTHORIZED","message":"Missing X-Hygur-Token header"}` {
		t.Errorf("unexpected response body: %s", body)
	}

	// Check content type
	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Create a test handler that should not be called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with auth middleware
	handler := server.authMiddleware(testHandler)

	// Create request with wrong token
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Hygur-Token", "wrongtoken")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	// Handler should not have been called
	if handlerCalled {
		t.Error("handler was called but should have been blocked by auth middleware")
	}

	// Check response body contains expected error
	body := rec.Body.String()
	if body != `{"code":"UNAUTHORIZED","message":"Invalid token"}` {
		t.Errorf("unexpected response body: %s", body)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Create a test handler that should be called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	// Wrap with auth middleware
	handler := server.authMiddleware(testHandler)

	// Create request with valid token
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Hygur-Token", validToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 200
	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	// Handler should have been called
	if !handlerCalled {
		t.Error("handler was not called but should have been")
	}

	// Check response body
	body := rec.Body.String()
	if body != "success" {
		t.Errorf("unexpected response body: %s", body)
	}
}

func TestAuthMiddleware_EmptyToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Create a test handler that should not be called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with auth middleware
	handler := server.authMiddleware(testHandler)

	// Create request with empty token header
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Hygur-Token", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401 (empty string is treated as missing)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	// Handler should not have been called
	if handlerCalled {
		t.Error("handler was called but should have been blocked by auth middleware")
	}
}

func TestAuthMiddleware_SimilarToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Create a test handler that should not be called
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with auth middleware
	handler := server.authMiddleware(testHandler)

	// Create request with token that differs by one character
	similarToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde0"
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Hygur-Token", similarToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Should return 401
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	// Handler should not have been called
	if handlerCalled {
		t.Error("handler was called but should have been blocked by auth middleware")
	}
}
