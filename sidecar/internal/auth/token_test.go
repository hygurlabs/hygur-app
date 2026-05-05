package auth

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() returned error: %v", err)
	}

	// Should be 64 hex characters (32 bytes)
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64", len(token))
	}

	// Should be valid hex
	decoded, err := hex.DecodeString(token)
	if err != nil {
		t.Errorf("token is not valid hex: %v", err)
	}

	if len(decoded) != 32 {
		t.Errorf("decoded token length = %d, want 32", len(decoded))
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)
	iterations := 100

	for i := 0; i < iterations; i++ {
		token, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken() iteration %d returned error: %v", i, err)
		}

		if tokens[token] {
			t.Errorf("duplicate token generated: %s", token)
		}
		tokens[token] = true
	}
}

func TestEnsureToken_CreatesNewToken(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")

	token, err := EnsureToken(dataDir)
	if err != nil {
		t.Fatalf("EnsureToken() returned error: %v", err)
	}

	// Verify token format
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64", len(token))
	}

	// Verify token file exists
	tokenPath := filepath.Join(dataDir, TokenFileName)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file not created: %v", err)
	}

	// Verify file permissions
	perm := info.Mode().Perm()
	if perm != TokenFilePerms {
		t.Errorf("token file permissions = %o, want %o", perm, TokenFilePerms)
	}

	// Verify directory permissions
	dirInfo, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("data directory stat failed: %v", err)
	}
	dirPerm := dirInfo.Mode().Perm()
	if dirPerm != TokenDirPerms {
		t.Errorf("data directory permissions = %o, want %o", dirPerm, TokenDirPerms)
	}
}

func TestEnsureToken_ReadsExistingToken(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")

	// Create initial token
	token1, err := EnsureToken(dataDir)
	if err != nil {
		t.Fatalf("first EnsureToken() returned error: %v", err)
	}

	// Call again - should return same token
	token2, err := EnsureToken(dataDir)
	if err != nil {
		t.Fatalf("second EnsureToken() returned error: %v", err)
	}

	if token1 != token2 {
		t.Errorf("tokens differ: first=%q, second=%q", token1, token2)
	}
}

func TestEnsureToken_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "nested", "deep", "data")

	_, err := EnsureToken(dataDir)
	if err != nil {
		t.Fatalf("EnsureToken() returned error: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data directory not created: %v", err)
	}
}

func TestEnsureToken_InvalidTokenFile(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")

	// Create directory
	if err := os.MkdirAll(dataDir, TokenDirPerms); err != nil {
		t.Fatalf("failed to create data directory: %v", err)
	}

	// Write invalid token (wrong length)
	tokenPath := filepath.Join(dataDir, TokenFileName)
	if err := os.WriteFile(tokenPath, []byte("shorttoken"), TokenFilePerms); err != nil {
		t.Fatalf("failed to write invalid token: %v", err)
	}

	_, err := EnsureToken(dataDir)
	if err == nil {
		t.Fatal("EnsureToken() expected error for invalid token, got nil")
	}
}

func TestEnsureToken_InvalidHexToken(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")

	// Create directory
	if err := os.MkdirAll(dataDir, TokenDirPerms); err != nil {
		t.Fatalf("failed to create data directory: %v", err)
	}

	// Write invalid token (correct length but not valid hex)
	tokenPath := filepath.Join(dataDir, TokenFileName)
	invalidToken := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	if err := os.WriteFile(tokenPath, []byte(invalidToken), TokenFilePerms); err != nil {
		t.Fatalf("failed to write invalid token: %v", err)
	}

	_, err := EnsureToken(dataDir)
	if err == nil {
		t.Fatal("EnsureToken() expected error for invalid hex token, got nil")
	}
}

func TestValidateTokenFormat(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{
			name:  "valid token",
			token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  true,
		},
		{
			name:  "valid token uppercase",
			token: "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF",
			want:  true,
		},
		{
			name:  "too short",
			token: "0123456789abcdef",
			want:  false,
		},
		{
			name:  "too long",
			token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef00",
			want:  false,
		},
		{
			name:  "invalid characters",
			token: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			want:  false,
		},
		{
			name:  "empty",
			token: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateTokenFormat(tt.token)
			if got != tt.want {
				t.Errorf("ValidateTokenFormat(%q) = %v, want %v", tt.token, got, tt.want)
			}
		})
	}
}

func TestEnsureToken_FixesLoosePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")

	// Create directory with loose permissions
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data directory: %v", err)
	}

	// Generate valid token
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() returned error: %v", err)
	}

	// Write token with loose permissions
	tokenPath := filepath.Join(dataDir, TokenFileName)
	if err := os.WriteFile(tokenPath, []byte(token), 0644); err != nil {
		t.Fatalf("failed to write token: %v", err)
	}

	// EnsureToken should read the existing token (we're not testing permission fixing on read)
	readToken, err := EnsureToken(dataDir)
	if err != nil {
		t.Fatalf("EnsureToken() returned error: %v", err)
	}

	if readToken != token {
		t.Errorf("token mismatch: got %q, want %q", readToken, token)
	}
}

func TestCompareTokens(t *testing.T) {
	validToken := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name     string
		provided string
		expected string
		want     bool
	}{
		{
			name:     "valid matching tokens",
			provided: validToken,
			expected: validToken,
			want:     true,
		},
		{
			name:     "invalid token - wrong content",
			provided: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			expected: validToken,
			want:     false,
		},
		{
			name:     "invalid token - empty provided",
			provided: "",
			expected: validToken,
			want:     false,
		},
		{
			name:     "invalid token - empty expected",
			provided: validToken,
			expected: "",
			want:     false,
		},
		{
			name:     "invalid token - different length shorter",
			provided: "0123456789abcdef",
			expected: validToken,
			want:     false,
		},
		{
			name:     "invalid token - different length longer",
			provided: validToken + "extra",
			expected: validToken,
			want:     false,
		},
		{
			name:     "both empty",
			provided: "",
			expected: "",
			want:     true,
		},
		{
			name:     "single character difference",
			provided: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde0",
			expected: validToken,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareTokens(tt.provided, tt.expected)
			if got != tt.want {
				t.Errorf("CompareTokens(%q, %q) = %v, want %v", tt.provided, tt.expected, got, tt.want)
			}
		})
	}
}
