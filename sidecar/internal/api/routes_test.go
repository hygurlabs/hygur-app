package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestKnowledgeRoutes_AuthRequired tests that knowledge endpoints require authentication.
func TestKnowledgeRoutes_AuthRequired(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Setup knowledge handler with minimal dependencies
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	knowledgeHandler := handlers.NewKnowledgeHandler(db, nil, searcher, logger)
	server.SetKnowledgeHandler(knowledgeHandler)

	router := server.Router()

	testCases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"POST /knowledge/ingest without token", http.MethodPost, "/knowledge/ingest", []byte(`{"path":"/tmp/test.txt"}`)},
		{"POST /knowledge/search without token", http.MethodPost, "/knowledge/search", []byte(`{"query":"test"}`)},
		{"GET /knowledge/{id} without token", http.MethodGet, "/knowledge/test-id", nil},
		{"DELETE /knowledge/{id} without token", http.MethodDelete, "/knowledge/test-id", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != nil {
				req = httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
			}

			// Verify error response format
			var errResp map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}

			if errResp["code"] != "UNAUTHORIZED" {
				t.Errorf("expected code 'UNAUTHORIZED', got '%v'", errResp["code"])
			}
		})
	}
}

// TestKnowledgeRoutes_WithValidToken tests that knowledge endpoints pass authentication with valid token.
func TestKnowledgeRoutes_WithValidToken(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)

	// Setup knowledge handler with minimal dependencies
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer db.Close()

	searcher := retrieval.NewHybridSearcher(db, nil)
	knowledgeHandler := handlers.NewKnowledgeHandler(db, nil, searcher, logger)
	server.SetKnowledgeHandler(knowledgeHandler)

	router := server.Router()

	// Test POST /knowledge/search with valid token
	t.Run("POST /knowledge/search with valid token", func(t *testing.T) {
		body := []byte(`{"query":"test"}`)
		req := httptest.NewRequest(http.MethodPost, "/knowledge/search", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hygur-Token", validToken)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		// The key test is that we don't get 401 (unauthorized).
		// Accept any non-auth response: 200 (empty results), 400 (validation), 500 (no LLM).
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("authentication should have passed, but got %d", rec.Code)
		}
		if rec.Code == http.StatusBadRequest {
			t.Errorf("expected no validation error for valid semantic search, got 400: %s", rec.Body.String())
		}
	})

	// Test GET /knowledge/{id} with valid token (should not return 401)
	t.Run("GET /knowledge/{id} with valid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/knowledge/nonexistent-id", nil)
		req.Header.Set("X-Hygur-Token", validToken)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		// The key test is that we don't get 401 (unauthorized)
		// We accept 404 (not found) or 500 (internal error)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("authentication should have passed, but got %d", rec.Code)
		}
		// Accept 404 or 500
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
			t.Errorf("expected status 404 or 500, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

// TestKnowledgeRoutes_NotConfigured tests that knowledge endpoints return 503 when handler not configured.
func TestKnowledgeRoutes_NotConfigured(t *testing.T) {
	logger := zerolog.Nop()
	cfg := &config.Config{}
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	server := NewServer(cfg, logger, validToken)
	// Don't set knowledge handler

	router := server.Router()

	testCases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"POST /knowledge/ingest", http.MethodPost, "/knowledge/ingest", []byte(`{"path":"/tmp/test.txt"}`)},
		{"GET /knowledge/{id}", http.MethodGet, "/knowledge/test-id", nil},
		{"DELETE /knowledge/{id}", http.MethodDelete, "/knowledge/test-id", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != nil {
				req = httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			req.Header.Set("X-Hygur-Token", validToken)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
			}
		})
	}
}
