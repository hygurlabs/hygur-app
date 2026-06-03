// Package integration — P1.6: prove the decoupling (P1.2 auth-by-mode, P1.3
// identity/store seam, P1.4 API versioning) didn't break the request pipeline,
// in BOTH auth modes, end-to-end through the real router → middleware →
// handler → store. Uses tags CRUD so no LLM/embeddings are required; the
// import→embeddings→search→chat pipeline is covered by the other integration
// tests (with their mock LM Studio).
package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/api"
	"github.com/hygur/sidecar/internal/api/handlers"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

const parityToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func setupParityServer(t *testing.T) *api.Server {
	t.Helper()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:            "127.0.0.1",
			Port:            0,
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    30 * time.Second,
			ShutdownTimeout: 5 * time.Second,
		},
		LMStudio: config.LMStudioConfig{URL: "http://localhost:1234", Timeout: 30 * time.Second, MaxRetries: 1},
	}
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Skipf("fts5 not available, skipping: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	server := api.NewServer(cfg, zerolog.New(io.Discard), parityToken)
	server.SetTagHandler(handlers.NewTagHandler(db, zerolog.New(io.Discard)))
	return server
}

func createTag(server *api.Server, headers map[string]string, name string) int {
	body, _ := json.Marshal(map[string]string{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/tags", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	return rec.Code
}

func listTagsContains(server *api.Server, headers map[string]string, name string) (int, bool) {
	req := httptest.NewRequest(http.MethodGet, "/tags", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, false
	}
	var resp struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, tg := range resp.Tags {
		if tg.Name == name {
			return rec.Code, true
		}
	}
	return rec.Code, false
}

// TestParity_LocalMode: the canonical create→list flow works end-to-end through
// the real router+store under the loopback token; a missing token is rejected.
func TestParity_LocalMode(t *testing.T) {
	server := setupParityServer(t)
	good := map[string]string{"X-Hygur-Token": parityToken}

	if code := createTag(server, nil, "should-fail"); code != http.StatusUnauthorized {
		t.Fatalf("no token: expected 401, got %d", code)
	}
	if code := createTag(server, good, "local-tag"); code/100 != 2 {
		t.Fatalf("create: expected 2xx, got %d", code)
	}
	if code, found := listTagsContains(server, good, "local-tag"); !found {
		t.Fatalf("list: 'local-tag' not found (code %d)", code)
	}
}

// TestParity_RemoteMode: the same flow under remote (per-device JWT) auth; the
// loopback static token is rejected, a valid device JWT is accepted, and the
// pipeline behaves identically.
func TestParity_RemoteMode(t *testing.T) {
	server := setupParityServer(t)

	pubPEM, privPEM, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	priv, _ := auth.ParseEd25519PrivateKeyPEM(privPEM)
	pub, _ := auth.ParseEd25519PublicKeyPEM(pubPEM)
	server.SetAuthenticator(auth.JWTAuth{PublicKey: pub})

	tok, err := auth.SignDeviceToken(priv, auth.DeviceClaims{
		Sub: "u1", Acc: "a1", Dev: "d1", Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	jwt := map[string]string{"Authorization": "Bearer " + tok}

	// The loopback static token must NOT be accepted in remote mode.
	if code := createTag(server, map[string]string{"X-Hygur-Token": parityToken}, "should-fail"); code != http.StatusUnauthorized {
		t.Fatalf("static token in remote mode: expected 401, got %d", code)
	}
	// A valid device JWT drives the full flow.
	if code := createTag(server, jwt, "remote-tag"); code/100 != 2 {
		t.Fatalf("create (jwt): expected 2xx, got %d", code)
	}
	if code, found := listTagsContains(server, jwt, "remote-tag"); !found {
		t.Fatalf("list (jwt): 'remote-tag' not found (code %d)", code)
	}
}
