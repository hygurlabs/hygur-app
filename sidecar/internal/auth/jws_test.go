package auth

import (
	"errors"
	"testing"
	"time"
)

func mustKeypair(t *testing.T) (pubPEM, privPEM string) {
	t.Helper()
	pub, priv, err := GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPEM: %v", err)
	}
	return pub, priv
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pubPEM, privPEM := mustKeypair(t)
	priv, err := ParseEd25519PrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("parse priv: %v", err)
	}
	pub, err := ParseEd25519PublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	claims := DeviceClaims{Sub: "u1", Acc: "a1", Dev: "d1", Jti: "j1", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix()}
	tok, err := SignDeviceToken(priv, claims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	got, err := VerifyDeviceToken(pub, tok, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Sub != "u1" || got.Acc != "a1" || got.Dev != "d1" || got.Jti != "j1" {
		t.Fatalf("claims mismatch: %+v", got)
	}
}

func TestVerifyExpired(t *testing.T) {
	pubPEM, privPEM := mustKeypair(t)
	priv, _ := ParseEd25519PrivateKeyPEM(privPEM)
	pub, _ := ParseEd25519PublicKeyPEM(pubPEM)

	now := time.Unix(1_700_000_000, 0)
	tok, _ := SignDeviceToken(priv, DeviceClaims{Sub: "u1", Exp: now.Add(-time.Second).Unix()})
	if _, err := VerifyDeviceToken(pub, tok, now); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestVerifyWrongKey(t *testing.T) {
	_, privPEM := mustKeypair(t)
	priv, _ := ParseEd25519PrivateKeyPEM(privPEM)
	otherPubPEM, _ := mustKeypair(t)
	otherPub, _ := ParseEd25519PublicKeyPEM(otherPubPEM)

	now := time.Unix(1_700_000_000, 0)
	tok, _ := SignDeviceToken(priv, DeviceClaims{Sub: "u1", Exp: now.Add(time.Hour).Unix()})
	if _, err := VerifyDeviceToken(otherPub, tok, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestVerifyTampered(t *testing.T) {
	pubPEM, privPEM := mustKeypair(t)
	priv, _ := ParseEd25519PrivateKeyPEM(privPEM)
	pub, _ := ParseEd25519PublicKeyPEM(pubPEM)

	now := time.Unix(1_700_000_000, 0)
	tok, _ := SignDeviceToken(priv, DeviceClaims{Sub: "u1", Exp: now.Add(time.Hour).Unix()})
	// Flip the last char of the signature segment.
	tampered := tok[:len(tok)-1]
	if tok[len(tok)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}
	if _, err := VerifyDeviceToken(pub, tampered, now); err == nil {
		t.Fatal("expected verification to fail on a tampered token")
	}
}

func TestVerifyMalformed(t *testing.T) {
	pubPEM, _ := mustKeypair(t)
	pub, _ := ParseEd25519PublicKeyPEM(pubPEM)
	if _, err := VerifyDeviceToken(pub, "not-a-jwt", time.Unix(1, 0)); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("expected ErrMalformedToken, got %v", err)
	}
}

func TestParseRejectsNonEd25519(t *testing.T) {
	if _, err := ParseEd25519PublicKeyPEM("garbage"); err == nil {
		t.Fatal("expected error parsing garbage public key")
	}
	if _, err := ParseEd25519PrivateKeyPEM("garbage"); err == nil {
		t.Fatal("expected error parsing garbage private key")
	}
}
