package edge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfig_SaveLoadWatermark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Absent → sane default (no error).
	c, err := LoadConfig(path)
	if err != nil || c.ProtonMailbox != "All Mail" {
		t.Fatalf("default config: %v / %+v", err, c)
	}

	c.Server = "https://cloud.hygur.ai"
	c.Token = "tok"
	c.Folder = "/x"
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil || got.Server != c.Server || got.Token != "tok" || got.Folder != "/x" {
		t.Fatalf("round-trip: %v / %+v", err, got)
	}

	wm := filepath.Join(dir, "files.watermark")
	now := time.Now().UTC().Truncate(time.Nanosecond)
	if err := WriteWatermark(wm, now); err != nil {
		t.Fatalf("WriteWatermark: %v", err)
	}
	if !ReadWatermark(wm).Equal(now) {
		t.Errorf("watermark round-trip: got %v want %v", ReadWatermark(wm), now)
	}
}

// TestUI_ConfigRedactsAndKeepsSecrets: GET never echoes the token/password (only
// *_set flags); POST with blank secrets keeps the stored ones.
func TestUI_ConfigRedactsAndKeepsSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	h := UIHandler(NewRunner(path), path)

	post := func(body string) {
		r := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST config = %d: %s", rec.Code, rec.Body.String())
		}
	}
	getCfg := func() map[string]any {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}

	// Initial save with a token + Proton password.
	post(`{"Server":"https://cloud.hygur.ai","Token":"secret-jwt","ProtonUser":"me@proton.me","ProtonPassword":"bridge-pw","ProtonMailbox":"All Mail"}`)
	m := getCfg()
	if body, _ := json.Marshal(m); strings.Contains(string(body), "secret-jwt") || strings.Contains(string(body), "bridge-pw") {
		t.Fatalf("GET /api/config leaked a secret: %s", body)
	}
	if m["token_set"] != true || m["proton_password_set"] != true {
		t.Errorf("expected *_set flags true: %+v", m)
	}

	// Re-save with BLANK secrets → stored ones are kept.
	post(`{"Server":"https://cloud.hygur.ai","Token":"","ProtonUser":"me@proton.me","ProtonPassword":"","ProtonMailbox":"INBOX"}`)
	c, _ := LoadConfig(path)
	if c.Token != "secret-jwt" || c.ProtonPassword != "bridge-pw" {
		t.Errorf("blank re-save must keep secrets, got token=%q pw=%q", c.Token, c.ProtonPassword)
	}
	if c.ProtonMailbox != "INBOX" {
		t.Errorf("non-secret field should update: %q", c.ProtonMailbox)
	}
}
