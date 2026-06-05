package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHostGuard: with the guard on, only loopback + allow-listed hosts pass;
// /health is always exempt (k8s probes use the pod IP as Host); off = allow all.
func TestHostGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	s := &Server{}
	s.SetHostGuard(true, []string{"cloud.hygur.ai"})
	h := s.hostGuardMiddleware(ok)

	check := func(host, path string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://x"+path, nil)
		r.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("host=%q path=%q → %d, want %d", host, path, rec.Code, want)
		}
	}
	check("127.0.0.1:8420", "/config", http.StatusOK)        // loopback
	check("localhost:8420", "/config", http.StatusOK)        // loopback
	check("cloud.hygur.ai", "/config", http.StatusOK)        // allow-listed (no port)
	check("evil.example.com", "/config", http.StatusForbidden) // rebinding → blocked
	check("evil.example.com", "/health", http.StatusOK)      // probe-exempt
	check("10.42.0.7:8420", "/health", http.StatusOK)        // pod-IP probe, exempt

	// Disabled → any Host allowed (preserves unconfigured self-hosted servers).
	s2 := &Server{}
	s2.SetHostGuard(false, nil)
	h2 := s2.hostGuardMiddleware(ok)
	r := httptest.NewRequest(http.MethodGet, "http://x/config", nil)
	r.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("disabled guard should allow any host, got %d", rec.Code)
	}
}
