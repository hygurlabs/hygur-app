package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

func apiKeyPtr(s string) *string { return &s }

// The LLM API key is a secret: PATCH /config must route it to the encrypted
// credential store, never to config.yaml, and GET /config must report only
// whether it is set — never the value. Clearing it removes the credential.
func TestConfig_APIKeyRoutedToCredentialStore(t *testing.T) {
	// PatchConfig SIGTERMs the process to apply config unless this is set.
	t.Setenv("HYGUR_NO_AUTORESTART", "1")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("lm_studio:\n  url: http://localhost:1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cs, err := auth.NewCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewCredentialStore: %v", err)
	}

	h := NewConfigHandler(&config.Config{}, cfgPath, zerolog.Nop())
	h.SetCredentialStore(cs)

	// Save a key.
	body, _ := json.Marshal(PatchConfigRequest{LMStudio: &PatchLMStudio{APIKey: apiKeyPtr("sk-mistral-xyz")}})
	rec := httptest.NewRecorder()
	h.PatchConfig(rec, httptest.NewRequest(http.MethodPatch, "/config", bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Stored in the credential store…
	fields, err := cs.GetConnectorCredential(auth.LLMCredentialID)
	if err != nil {
		t.Fatalf("GetConnectorCredential: %v", err)
	}
	if got := fields[auth.LLMCredentialField]; got != "sk-mistral-xyz" {
		t.Fatalf("stored key = %q, want sk-mistral-xyz", got)
	}

	// …never written to config.yaml.
	raw, _ := os.ReadFile(cfgPath)
	if bytes.Contains(raw, []byte("sk-mistral-xyz")) {
		t.Fatalf("API key leaked into config.yaml:\n%s", raw)
	}

	// GET reports it set, without exposing the value.
	getRec := httptest.NewRecorder()
	h.GetConfig(getRec, httptest.NewRequest(http.MethodGet, "/config", nil))
	var resp ConfigResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !resp.LMStudio.APIKeySet {
		t.Fatal("api_key_set = false, want true")
	}
	if bytes.Contains(getRec.Body.Bytes(), []byte("sk-mistral-xyz")) {
		t.Fatal("GET /config leaked the API key value")
	}

	// Clearing removes the credential.
	clearBody, _ := json.Marshal(PatchConfigRequest{LMStudio: &PatchLMStudio{APIKey: apiKeyPtr("")}})
	clearRec := httptest.NewRecorder()
	h.PatchConfig(clearRec, httptest.NewRequest(http.MethodPatch, "/config", bytes.NewReader(clearBody)))
	if clearRec.Code != http.StatusNoContent {
		t.Fatalf("clear PATCH status = %d, want 204", clearRec.Code)
	}
	if _, err := cs.GetConnectorCredential(auth.LLMCredentialID); err == nil {
		t.Fatal("expected credential removed after clearing the key")
	}
}
