package config

import (
	"path/filepath"
	"testing"
)

// Regression (P2.2): HYGUR_AUTH_* env vars must bind. viper's AutomaticEnv only
// env-binds keys it already knows via a registered default — so auth.public_key
// / auth.private_key need empty defaults in setDefaults, or remote mode can't be
// configured by environment (the symptom was a crash-loop: "auth.public_key is
// required when auth.mode is remote" despite HYGUR_AUTH_PUBLIC_KEY being set).
func TestAuthEnvBinding(t *testing.T) {
	t.Setenv("HYGUR_AUTH_MODE", "remote")
	t.Setenv("HYGUR_AUTH_PUBLIC_KEY", "PEM-PLACEHOLDER")

	// A non-existent config path → defaults + env only (no stray config.yaml).
	cfg, err := LoadWithOptions(&LoadOptions{ConfigPath: filepath.Join(t.TempDir(), "nope.yaml")})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Auth.Mode != "remote" {
		t.Fatalf("auth.mode = %q, want remote", cfg.Auth.Mode)
	}
	if cfg.Auth.PublicKey != "PEM-PLACEHOLDER" {
		t.Fatalf("auth.public_key did not bind from HYGUR_AUTH_PUBLIC_KEY (got %q)", cfg.Auth.PublicKey)
	}
}
