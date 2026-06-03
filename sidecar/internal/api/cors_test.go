package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

// Regression: corsMiddleware must be wired (it was defined but never registered)
// and must allow the Tauri desktop origin + advertise the X-Hygur-API header,
// or every cross-origin client (Tauri shell, vite dev) breaks.
func TestCORS_TauriPreflight(t *testing.T) {
	srv := NewServer(&config.Config{}, zerolog.Nop(), "tok")

	req := httptest.NewRequest(http.MethodOptions, "/config", nil)
	req.Header.Set("Origin", "tauri://localhost")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-hygur-token,x-hygur-api,content-type")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "tauri://localhost" {
		t.Fatalf("ACAO = %q, want tauri://localhost", got)
	}
	if ah := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(ah, "X-Hygur-API") {
		t.Fatalf("Allow-Headers missing X-Hygur-API: %q", ah)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	srv := NewServer(&config.Config{}, zerolog.Nop(), "tok")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO should be empty for a disallowed origin, got %q", got)
	}
}
