package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

func managedTestHandler(managed bool) *ConfigHandler {
	cfg := &config.Config{}
	cfg.LMStudio.URL = "https://llm.infomaniak.example/v1"
	cfg.LMStudio.EmbeddingURL = "https://emb.infomaniak.example/v1"
	cfg.LMStudio.IndexingURL = "https://idx.infomaniak.example/v1"
	cfg.LMStudio.ModelDefault = "secret-model"
	h := NewConfigHandler(cfg, "", zerolog.Nop())
	h.SetManaged(managed)
	return h
}

// TestGetConfig_ManagedRedactsEndpoints: a managed cloud tenant must never leak
// the upstream LLM endpoints/models; an unmanaged sidecar returns them.
func TestGetConfig_ManagedRedactsEndpoints(t *testing.T) {
	rec := httptest.NewRecorder()
	managedTestHandler(true).GetConfig(rec, httptest.NewRequest(http.MethodGet, "/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"infomaniak", "secret-model"} {
		if strings.Contains(body, leak) {
			t.Errorf("managed GET /config leaked %q: %s", leak, body)
		}
	}
	var resp ConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Managed {
		t.Error("expected managed=true in the response")
	}
	if resp.LMStudio.URL != "" || resp.LMStudio.EmbeddingURL != "" || resp.LMStudio.IndexingURL != "" {
		t.Errorf("endpoints not redacted: %+v", resp.LMStudio)
	}

	// Unmanaged: the endpoint is exposed (self-hosters configure their own).
	rec2 := httptest.NewRecorder()
	managedTestHandler(false).GetConfig(rec2, httptest.NewRequest(http.MethodGet, "/config", nil))
	if !strings.Contains(rec2.Body.String(), "infomaniak") {
		t.Errorf("unmanaged GET should expose the endpoint, got: %s", rec2.Body.String())
	}
}

// TestPatchConfig_ManagedRejectsLMStudio: a managed tenant can't repoint the LLM.
func TestPatchConfig_ManagedRejectsLMStudio(t *testing.T) {
	h := managedTestHandler(true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/config",
		strings.NewReader(`{"lm_studio":{"url":"https://evil.example"}}`))
	h.PatchConfig(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("managed PATCH lm_studio = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if h.cfg.LMStudio.URL != "https://llm.infomaniak.example/v1" {
		t.Errorf("URL was mutated despite managed mode: %q", h.cfg.LMStudio.URL)
	}
}
