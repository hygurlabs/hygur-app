package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWebUIInjectsToken(t *testing.T) {
	s := &Server{token: "tok-abc-123"}
	rec := httptest.NewRecorder()
	s.handleWebUI(rec, httptest.NewRequest("GET", "/", nil))

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if strings.Contains(body, "__HYGUR_TOKEN__") {
		t.Error("token placeholder was not substituted")
	}
	// The token is injected into the <meta name="hygur-token"> tag, which the
	// web client reads at startup (see webui/src/lib/api.ts readToken()).
	if !strings.Contains(body, `content="tok-abc-123"`) {
		t.Error("live token not injected into the page")
	}
	if !strings.Contains(body, "<title>Hygur</title>") {
		t.Error("served page is not the Hygur web client")
	}
}
