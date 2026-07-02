package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/hygur/sidecar/internal/auth"
)

func testWebAuthn(t *testing.T, store *Store) *WebAuthnService {
	t.Helper()
	_, priv, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	svc, err := NewService(store, priv, "hygur.ai", time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	wa, err := NewWebAuthnService(store, svc, "hygur.ai", "Hygur Cloud", []string{"https://cloud.hygur.ai"})
	if err != nil {
		t.Fatalf("NewWebAuthnService: %v", err)
	}
	return wa
}

func TestStore_WebauthnRoundTrip(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	acc, err := s.CreateAccount(now, "owner@example.com", "active", nil)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Resolve by slug (tenant id).
	if got, err := s.getAccountByTenantID(acc.TenantID); err != nil || got.AccountNumber != acc.AccountNumber {
		t.Fatalf("getAccountByTenantID: got %+v err %v", got, err)
	}
	if _, err := s.getAccountByTenantID("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown tenant id")
	}

	// Credential round-trip.
	if err := s.AddWebauthnCredential(now, acc.AccountNumber, "cred-1", []byte(`{"v":1}`), "passkey"); err != nil {
		t.Fatalf("AddWebauthnCredential: %v", err)
	}
	blobs, err := s.WebauthnCredentialBlobs(acc.AccountNumber)
	if err != nil || len(blobs) != 1 || string(blobs[0]) != `{"v":1}` {
		t.Fatalf("WebauthnCredentialBlobs: %v %v", blobs, err)
	}
	if err := s.UpdateWebauthnCredential("cred-1", []byte(`{"v":2}`)); err != nil {
		t.Fatalf("UpdateWebauthnCredential: %v", err)
	}
	blobs, _ = s.WebauthnCredentialBlobs(acc.AccountNumber)
	if string(blobs[0]) != `{"v":2}` {
		t.Fatalf("update not persisted: %s", blobs[0])
	}

	// Device creation mints a usable refresh.
	dev, refresh, err := s.CreateDeviceForAccount(now, acc.AccountNumber, "passkey")
	if err != nil || dev.JTI == "" || refresh == "" {
		t.Fatalf("CreateDeviceForAccount: %v %v", dev, err)
	}
}

func TestStore_WebauthnSessionOneTimeAndExpiry(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	// Live session: taken once, gone on replay.
	if err := s.PutWebauthnSession("sid", "000001", "login", []byte("data"), now.Add(time.Minute)); err != nil {
		t.Fatalf("PutWebauthnSession: %v", err)
	}
	acc, purpose, data, err := s.TakeWebauthnSession(now, "sid")
	if err != nil || acc != "000001" || purpose != "login" || string(data) != "data" {
		t.Fatalf("TakeWebauthnSession: %q %q %q %v", acc, purpose, data, err)
	}
	if _, _, _, err := s.TakeWebauthnSession(now, "sid"); err == nil {
		t.Fatal("replay should fail (one-time)")
	}

	// Expired session is rejected (and consumed).
	if err := s.PutWebauthnSession("old", "000001", "login", []byte("x"), now.Add(-time.Second)); err != nil {
		t.Fatalf("PutWebauthnSession: %v", err)
	}
	if _, _, _, err := s.TakeWebauthnSession(now, "old"); err == nil {
		t.Fatal("expired session should be rejected")
	}
}

func TestWebAuthn_LoginBegin(t *testing.T) {
	s := testStore(t)
	wa := testWebAuthn(t, s)
	now := time.Unix(1_700_000_000, 0).UTC()
	acc, err := s.CreateAccount(now, "owner@example.com", "active", nil)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// A registered credential is required for BeginLogin to have something to allow.
	cred := webauthn.Credential{ID: []byte("cred-1")}
	blob, _ := json.Marshal(cred)
	if err := s.AddWebauthnCredential(now, acc.AccountNumber, base64url(cred.ID), blob, "passkey"); err != nil {
		t.Fatalf("AddWebauthnCredential: %v", err)
	}

	r := chi.NewRouter()
	wa.Register(r)

	// Known instance → options + session_id.
	body, _ := json.Marshal(loginBeginReq{Instance: acc.TenantID})
	req := httptest.NewRequest(http.MethodPost, "/passkey/login/begin", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login/begin status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["session_id"] == nil || resp["publicKey"] == nil {
		t.Fatalf("missing session_id/publicKey: %v", resp)
	}

	// Unknown instance → uniform 401 (no enumeration).
	body2, _ := json.Marshal(loginBeginReq{Instance: "nope-nope"})
	req2 := httptest.NewRequest(http.MethodPost, "/passkey/login/begin", bytes.NewReader(body2))
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unknown instance status = %d, want 401", rec2.Code)
	}
}

// TestCORSMiddleware_ExactHostMatch: legit prod + loopback origins are reflected;
// unanchored look-alikes (localhost.evil.com, cloud.hygur.ai.evil.com) are NOT.
func TestCORSMiddleware_ExactHostMatch(t *testing.T) {
	mw := CORSMiddleware([]string{"https://cloud.hygur.ai", "https://console.hygur.ai"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	acao := func(origin string) string {
		req := httptest.NewRequest(http.MethodGet, "/passkey/count", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Access-Control-Allow-Origin")
	}

	for _, o := range []string{
		"https://cloud.hygur.ai", "https://console.hygur.ai",
		"http://localhost", "http://localhost:5173", "http://127.0.0.1:8420",
	} {
		if got := acao(o); got != o {
			t.Errorf("legit origin %q: ACAO=%q, want it reflected", o, got)
		}
	}

	for _, o := range []string{
		"http://localhost.evil.com", "https://cloud.hygur.ai.evil.com",
		"https://evil.com", "http://127.0.0.1.evil.com", "http://localhost@evil.com",
		"https://localhost", // https loopback is not a configured dev origin
	} {
		if got := acao(o); got != "" {
			t.Errorf("look-alike origin %q: ACAO=%q, want empty (rejected)", o, got)
		}
	}
}

// TestWebAuthn_DesktopHandoffServerIssuesState: the server generates the one-time
// `state` (client value ignored); a claim with the server-issued state succeeds
// once; a replay or a forged/unknown state is rejected.
func TestWebAuthn_DesktopHandoffServerIssuesState(t *testing.T) {
	s := testStore(t)
	wa := testWebAuthn(t, s)
	now := time.Now()
	acc, err := s.CreateAccount(now, "owner@example.com", "active", nil)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	dev, _, err := s.CreateDeviceForAccount(now, acc.AccountNumber, "web")
	if err != nil {
		t.Fatalf("CreateDeviceForAccount: %v", err)
	}
	tok, err := wa.svc.mintAccess(now, acc, dev)
	if err != nil {
		t.Fatalf("mintAccess: %v", err)
	}

	r := chi.NewRouter()
	wa.Register(r)

	// Handoff: the client sends a bogus/absent state; the server issues its own.
	body, _ := json.Marshal(desktopReq{State: "client-chosen-should-be-ignored"})
	req := httptest.NewRequest(http.MethodPost, "/desktop/handoff", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handoff status = %d, body %s", rec.Code, rec.Body.String())
	}
	var hr struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hr); err != nil {
		t.Fatalf("decode handoff: %v", err)
	}
	if !hr.OK || hr.State == "" {
		t.Fatalf("handoff must return a server-issued state, got %+v", hr)
	}
	if hr.State == "client-chosen-should-be-ignored" {
		t.Fatal("server must NOT echo the client-supplied state")
	}

	claim := func(state string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(desktopReq{State: state})
		rq := httptest.NewRequest(http.MethodPost, "/desktop/claim", bytes.NewReader(b))
		rc := httptest.NewRecorder()
		r.ServeHTTP(rc, rq)
		return rc
	}

	// Server-issued state → claim succeeds and returns the tenant bundle.
	okRec := claim(hr.State)
	if okRec.Code != http.StatusOK {
		t.Fatalf("valid claim status = %d, body %s", okRec.Code, okRec.Body.String())
	}
	var bundle tokenResp
	if err := json.Unmarshal(okRec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.AccessToken == "" || bundle.TenantID != acc.TenantID {
		t.Fatalf("claim bundle wrong: %+v", bundle)
	}

	// Replay of the same (already consumed) state → rejected.
	if replay := claim(hr.State); replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed state status = %d, want 401", replay.Code)
	}
	// A forged/unknown state the server never issued → rejected.
	if forged := claim("deadbeefdeadbeefdeadbeefdeadbeef"); forged.Code != http.StatusUnauthorized {
		t.Fatalf("forged state status = %d, want 401", forged.Code)
	}
}

func TestWebAuthn_RegisterBeginRequiresToken(t *testing.T) {
	s := testStore(t)
	wa := testWebAuthn(t, s)
	r := chi.NewRouter()
	wa.Register(r)

	req := httptest.NewRequest(http.MethodPost, "/passkey/register/begin", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("register/begin without token = %d, want 401", rec.Code)
	}
}
