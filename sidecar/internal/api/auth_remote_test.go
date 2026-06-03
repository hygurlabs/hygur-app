package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

// Wiring test for P1.2 remote auth: with a JWT authenticator installed, the
// auth middleware rejects unauthenticated requests and, on a valid device
// token, lets the request through with the resolved Identity in context.
func TestAuthMiddleware_RemoteJWT(t *testing.T) {
	logger := zerolog.Nop()
	server := NewServer(&config.Config{}, logger, "unused-local-token")

	pubPEM, privPEM, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	priv, _ := auth.ParseEd25519PrivateKeyPEM(privPEM)
	pub, _ := auth.ParseEd25519PublicKeyPEM(pubPEM)
	server.SetAuthenticator(auth.JWTAuth{PublicKey: pub})

	tok, err := auth.SignDeviceToken(priv, auth.DeviceClaims{
		Sub: "u9", Acc: "a9", Dev: "d9", Jti: "jx",
		Exp: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	var gotID auth.Identity
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotID = auth.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := server.authMiddleware(next)

	// No credentials → 401, handler not reached.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("no token: handler should not be reached")
	}

	// Valid device JWT → 200, identity propagated to the handler.
	called = false
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("valid token: expected 200, got %d", rec2.Code)
	}
	if !called {
		t.Fatal("valid token: handler should be reached")
	}
	if gotID.UserID != "u9" || gotID.AccountID != "a9" || gotID.DeviceID != "d9" {
		t.Fatalf("valid token: identity not propagated, got %+v", gotID)
	}
}

// In local (default) mode the static token still authenticates and yields the
// single LocalIdentity — non-regression for the embedded/desktop path.
func TestAuthMiddleware_LocalIdentityDefault(t *testing.T) {
	logger := zerolog.Nop()
	server := NewServer(&config.Config{}, logger, "tok-123")

	var gotID auth.Identity
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = auth.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := server.authMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Hygur-Token", "tok-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotID != auth.LocalIdentity {
		t.Fatalf("expected LocalIdentity, got %+v", gotID)
	}
}
