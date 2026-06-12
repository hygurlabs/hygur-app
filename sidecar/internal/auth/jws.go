package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"time"
)

// Device tokens are JWT-compatible JWS using EdDSA (Ed25519). The control plane
// (P4) signs them with a private key; each instance verifies with the matching
// public key — locally, no per-request callback. For self-hosting, the
// `issue-token` CLI signs with a locally-held private key.
//
// Implemented with the standard library (no JWT dependency). Verification is
// hard-wired to Ed25519, so the classic "alg":"none" / alg-confusion attacks do
// not apply: the header's alg field is never trusted to pick the algorithm.

// DeviceClaims are the (minimal) claims carried by a device token.
type DeviceClaims struct {
	Sub string `json:"sub"` // user id
	Acc string `json:"acc"` // account id
	Dev string `json:"dev"` // device id
	Jti string `json:"jti"` // unique token id (revocation handle)
	Iat int64  `json:"iat"` // issued-at (unix)
	Exp int64  `json:"exp"` // expiry (unix); 0 = never
}

var b64 = base64.RawURLEncoding

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Token verification errors.
var (
	ErrMalformedToken = errors.New("malformed token")
	ErrBadSignature   = errors.New("bad token signature")
	ErrTokenExpired   = errors.New("token expired")
)

// SignDeviceToken mints an EdDSA-signed JWT for the given claims.
func SignDeviceToken(priv ed25519.PrivateKey, c DeviceClaims) (string, error) {
	h, err := json.Marshal(jwtHeader{Alg: "EdDSA", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + b64.EncodeToString(sig), nil
}

// VerifyDeviceToken checks the signature against pub and the expiry against now,
// then returns the decoded claims. `now` is injected for testability.
func VerifyDeviceToken(pub ed25519.PublicKey, token string, now time.Time) (DeviceClaims, error) {
	var claims DeviceClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, ErrMalformedToken
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return claims, ErrMalformedToken
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if !ed25519.Verify(pub, signingInput, sig) {
		return claims, ErrBadSignature
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return claims, ErrMalformedToken
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, ErrMalformedToken
	}
	if claims.Exp != 0 && !now.Before(time.Unix(claims.Exp, 0)) {
		return claims, ErrTokenExpired
	}
	return claims, nil
}

// NewJTI returns a random token id for revocation tracking.
func NewJTI() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read essentially never fails; fall back to a time-free constant
		// rather than panicking in a token-issuance path.
		return "jti-unavailable"
	}
	return hex.EncodeToString(b)
}

// GenerateEd25519KeyPEM creates a fresh keypair and returns both keys PEM-encoded
// (PKIX public, PKCS8 private). Used by the `gen-auth-key` CLI.
func GenerateEd25519KeyPEM() (pubPEM, privPEM string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", err
	}
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	return pubPEM, privPEM, nil
}

// ParseEd25519PublicKeyPEM decodes a PEM-encoded PKIX Ed25519 public key.
func ParseEd25519PublicKeyPEM(pemStr string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemStr)))
	if block == nil {
		return nil, errors.New("auth: no PEM block in public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("auth: public key is not Ed25519")
	}
	return pub, nil
}

// ParseEd25519PrivateKeyPEM decodes a PEM-encoded PKCS8 Ed25519 private key.
func ParseEd25519PrivateKeyPEM(pemStr string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemStr)))
	if block == nil {
		return nil, errors.New("auth: no PEM block in private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("auth: private key is not Ed25519")
	}
	return priv, nil
}
