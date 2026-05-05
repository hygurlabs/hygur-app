package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
)

// TestBriefHandler_NoSchedulerReturns503 — when the brief scheduler isn't
// configured (daily_brief absent from config or store/llm missing) the
// handler should fail loudly so the macOS UI can surface a clear message.
func TestBriefHandler_NoSchedulerReturns503(t *testing.T) {
	h := NewBriefHandler(nil, zerolog.New(io.Discard))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/brief/run", nil)
	h.RunNow(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body: %v", err)
	}
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "BRIEF_DISABLED" {
		t.Errorf("error.code = %q, want BRIEF_DISABLED", code)
	}
}
