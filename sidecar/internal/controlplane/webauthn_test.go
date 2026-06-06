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
