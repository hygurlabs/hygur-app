package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// WP21 — /health caches its LM/embedding ping results server-side (30s TTL) so the
// Sidebar's frequent poll + k8s probes don't each fire two live network pings. A second
// request within the TTL must be served from the cache without re-pinging.
func TestHealthHandler_PingCachedWithinTTL(t *testing.T) {
	var pings int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			atomic.AddInt32(&pings, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	client := llm.NewClientWithHTTP(mock.URL, 5*time.Second, 0, mock.Client())
	handler := NewHealthHandler(client)

	do := func() HealthResponse {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		var resp HealthResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	first := do()
	afterFirst := atomic.LoadInt32(&pings)
	if afterFirst == 0 {
		t.Fatal("first /health must ping the endpoint")
	}
	if first.Inference != "connected" || first.Embedding != "connected" {
		t.Fatalf("first response = %+v, want inference/embedding connected", first)
	}

	second := do()
	if got := atomic.LoadInt32(&pings); got != afterFirst {
		t.Errorf("second /health within TTL must not re-ping: pings %d -> %d", afterFirst, got)
	}
	if second.Inference != "connected" || second.Embedding != "connected" {
		t.Errorf("cached response = %+v, want inference/embedding connected", second)
	}
}
