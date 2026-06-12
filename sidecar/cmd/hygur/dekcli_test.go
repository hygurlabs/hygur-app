package main

import (
	"encoding/base64"
	"testing"
)

// TestGenerateDEK checks the minted key is 256 bits of fresh entropy each call.
func TestGenerateDEK(t *testing.T) {
	k1, err := generateDEK()
	if err != nil {
		t.Fatalf("generateDEK: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(k1)
	if err != nil {
		t.Fatalf("DEK is not valid base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("DEK = %d bytes, want 32 (256-bit)", len(raw))
	}
	k2, err := generateDEK()
	if err != nil {
		t.Fatalf("generateDEK (2): %v", err)
	}
	if k1 == k2 {
		t.Fatal("two DEKs are identical — entropy source is broken")
	}
}
