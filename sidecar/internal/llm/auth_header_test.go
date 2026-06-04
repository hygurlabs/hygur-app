package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
)

// A configured API key must be sent as a bearer token so hosted providers
// (Mistral, OpenAI…) accept the request. ListModels exercises setAuthHeader,
// which every request builder shares.
func TestClient_SendsBearerAuthWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	c := NewClient(&config.LMStudioConfig{URL: srv.URL, APIKey: "sk-test-123", Timeout: 2 * time.Second})
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-test-123")
	}
}

// Local runtimes (LM Studio, Ollama, vLLM) authenticate nothing; the header
// must be omitted so loopback/LAN setups stay byte-for-byte unchanged.
func TestClient_NoAuthHeaderWhenKeyEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	c := NewClient(&config.LMStudioConfig{URL: srv.URL, Timeout: 2 * time.Second})
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if hadAuth {
		t.Fatal("Authorization header present for a keyless local runtime; want absent")
	}
}
