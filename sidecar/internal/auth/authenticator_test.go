package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalTokenAuth(t *testing.T) {
	a := LocalTokenAuth{Token: "secret-token"}

	// Missing header.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := a.Authenticate(r); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("missing: expected ErrMissingToken, got %v", err)
	}

	// Wrong token.
	r.Header.Set("X-Hygur-Token", "nope")
	if _, err := a.Authenticate(r); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong: expected ErrInvalidToken, got %v", err)
	}

	// Correct token → LocalIdentity.
	r.Header.Set("X-Hygur-Token", "secret-token")
	id, err := a.Authenticate(r)
	if err != nil {
		t.Fatalf("valid: unexpected error %v", err)
	}
	if id != LocalIdentity {
		t.Fatalf("valid: expected LocalIdentity, got %+v", id)
	}
}

func TestJWTAuth(t *testing.T) {
	pubPEM, privPEM := mustKeypair(t)
	priv, _ := ParseEd25519PrivateKeyPEM(privPEM)
	pub, _ := ParseEd25519PublicKeyPEM(pubPEM)

	now := time.Unix(1_700_000_000, 0)
	mint := func(c DeviceClaims) string {
		tok, err := SignDeviceToken(priv, c)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}
	valid := mint(DeviceClaims{Sub: "u1", Acc: "a1", Dev: "d1", Jti: "j1", Exp: now.Add(time.Hour).Unix()})

	a := JWTAuth{PublicKey: pub, Now: func() time.Time { return now }}

	// Valid via Authorization: Bearer.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+valid)
	id, err := a.Authenticate(r)
	if err != nil {
		t.Fatalf("bearer valid: %v", err)
	}
	if id.UserID != "u1" || id.AccountID != "a1" || id.DeviceID != "d1" {
		t.Fatalf("bearer valid: identity mismatch %+v", id)
	}

	// Valid via X-Hygur-Token (clients that reuse the existing header).
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("X-Hygur-Token", valid)
	if _, err := a.Authenticate(r2); err != nil {
		t.Fatalf("header valid: %v", err)
	}

	// Missing.
	if _, err := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("missing: expected ErrMissingToken, got %v", err)
	}

	// Expired.
	expired := mint(DeviceClaims{Sub: "u1", Exp: now.Add(-time.Hour).Unix()})
	re := httptest.NewRequest(http.MethodGet, "/", nil)
	re.Header.Set("Authorization", "Bearer "+expired)
	if _, err := a.Authenticate(re); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired: expected ErrInvalidToken, got %v", err)
	}

	// Revoked jti.
	revoked := JWTAuth{PublicKey: pub, Revoked: map[string]bool{"j1": true}, Now: func() time.Time { return now }}
	rr := httptest.NewRequest(http.MethodGet, "/", nil)
	rr.Header.Set("Authorization", "Bearer "+valid)
	if _, err := revoked.Authenticate(rr); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked: expected ErrInvalidToken, got %v", err)
	}
}
