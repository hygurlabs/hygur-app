package auth

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Authenticator authenticates an HTTP request and resolves its Identity. The
// API server selects an implementation by config: LocalTokenAuth (loopback /
// single-user, the default) or JWTAuth (remote, per-device tokens).
type Authenticator interface {
	Authenticate(r *http.Request) (Identity, error)
}

// Sentinel errors so the middleware can preserve the existing client-facing
// messages (missing vs invalid) without leaking detail.
var (
	ErrMissingToken = errors.New("missing token")
	ErrInvalidToken = errors.New("invalid token")
)

// LocalTokenAuth is the loopback scheme: a single static token in the
// X-Hygur-Token header. Every authenticated request maps to LocalIdentity.
type LocalTokenAuth struct {
	Token string
}

// Authenticate implements Authenticator.
func (a LocalTokenAuth) Authenticate(r *http.Request) (Identity, error) {
	token := r.Header.Get("X-Hygur-Token")
	if token == "" {
		return Identity{}, ErrMissingToken
	}
	if !CompareTokens(token, a.Token) {
		return Identity{}, ErrInvalidToken
	}
	return LocalIdentity, nil
}

// JWTAuth is the remote scheme: per-device EdDSA tokens validated against a
// public key, with expiry and a jti revocation set checked locally. The token
// may arrive as `Authorization: Bearer <jwt>` or in X-Hygur-Token.
type JWTAuth struct {
	PublicKey ed25519.PublicKey
	// Revoked holds jti values to reject even if otherwise valid. May be nil.
	Revoked map[string]bool
	// Tenant, when non-empty, pins this server to a single tenant (pod-per-tenant
	// cloud): a token whose Acc claim differs is rejected. Defence in depth on top
	// of the subdomain→namespace routing.
	Tenant string
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// Authenticate implements Authenticator.
func (a JWTAuth) Authenticate(r *http.Request) (Identity, error) {
	raw := bearerOrHeaderToken(r)
	if raw == "" {
		return Identity{}, ErrMissingToken
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	claims, err := VerifyDeviceToken(a.PublicKey, raw, now())
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	if a.Revoked[claims.Jti] {
		return Identity{}, ErrInvalidToken
	}
	// Tenant pinning: a token minted for another tenant must never be accepted by
	// this pod, even with a valid signature. Opaque error — don't reveal the reason.
	if a.Tenant != "" && claims.Acc != a.Tenant {
		return Identity{}, ErrInvalidToken
	}
	return Identity{UserID: claims.Sub, AccountID: claims.Acc, DeviceID: claims.Dev}, nil
}

// bearerOrHeaderToken extracts a token from Authorization: Bearer <token> or,
// failing that, the X-Hygur-Token header.
func bearerOrHeaderToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return r.Header.Get("X-Hygur-Token")
}
