// Package auth handles authentication token generation and management.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// TokenLength is the number of random bytes used to generate the token.
	// 32 bytes = 256 bits of entropy, resulting in a 64-character hex string.
	TokenLength = 32

	// TokenFileName is the name of the file storing the authentication token.
	// Named "token" (no dot prefix) — matches the macOS Application Support convention.
	TokenFileName = "token"

	// tokenFileNameLegacy is the old dot-prefixed name used before the macOS
	// data-dir migration. EnsureToken migrates it silently.
	tokenFileNameLegacy = ".hygur-token"

	// TokenFilePerms are the permissions for the token file (owner read/write only).
	TokenFilePerms = 0600

	// TokenDirPerms are the permissions for the token directory (owner read/write/execute only).
	TokenDirPerms = 0700
)

// ErrTokenGeneration indicates a failure to generate a cryptographic token.
var ErrTokenGeneration = errors.New("failed to generate token")

// GenerateToken generates a new cryptographically secure random token.
// Returns a 64-character hexadecimal string (32 bytes of entropy).
func GenerateToken() (string, error) {
	bytes := make([]byte, TokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTokenGeneration, err)
	}
	return hex.EncodeToString(bytes), nil
}

// EnsureToken reads an existing token from dataDir or creates a new one.
// The token is stored in a file named .hygur-token within dataDir.
// If the directory does not exist, it will be created with permissions 0700.
// The token file is created with permissions 0600.
func EnsureToken(dataDir string) (string, error) {
	// Ensure the data directory exists with secure permissions
	if err := os.MkdirAll(dataDir, TokenDirPerms); err != nil {
		return "", fmt.Errorf("creating data directory: %w", err)
	}

	// Check and fix directory permissions if needed
	if err := ensurePermissions(dataDir, TokenDirPerms); err != nil {
		return "", fmt.Errorf("setting directory permissions: %w", err)
	}

	tokenPath := filepath.Join(dataDir, TokenFileName)

	// Try to read existing token
	token, err := readToken(tokenPath)
	if err == nil {
		return token, nil
	}

	// If file doesn't exist, check the legacy .hygur-token name and migrate.
	if os.IsNotExist(err) {
		legacyPath := filepath.Join(dataDir, tokenFileNameLegacy)
		if legacyToken, legacyErr := readToken(legacyPath); legacyErr == nil {
			// Copy to canonical name so future reads use the new path.
			if writeErr := writeToken(tokenPath, legacyToken); writeErr == nil {
				return legacyToken, nil
			}
			// Write failed but we still have the token — use it.
			return legacyToken, nil
		}
	}

	// If file doesn't exist (neither canonical nor legacy), create a new token.
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading token file: %w", err)
	}

	// Generate and save new token
	token, err = GenerateToken()
	if err != nil {
		return "", err
	}

	if err := writeToken(tokenPath, token); err != nil {
		return "", fmt.Errorf("writing token file: %w", err)
	}

	return token, nil
}

// readToken reads the token from the specified file path.
func readToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	token := string(data)

	// Validate token format (should be 64 hex characters)
	if len(token) != TokenLength*2 {
		return "", fmt.Errorf("invalid token length: expected %d, got %d", TokenLength*2, len(token))
	}

	if _, err := hex.DecodeString(token); err != nil {
		return "", fmt.Errorf("invalid token format: %w", err)
	}

	return token, nil
}

// writeToken writes the token to the specified file path with secure permissions.
func writeToken(path string, token string) error {
	// Write with secure permissions from the start
	if err := os.WriteFile(path, []byte(token), TokenFilePerms); err != nil {
		return err
	}

	// Double-check permissions are set correctly (defense in depth)
	return ensurePermissions(path, TokenFilePerms)
}

// ensurePermissions verifies and sets the correct permissions on a file or directory.
func ensurePermissions(path string, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	currentPerm := info.Mode().Perm()
	if currentPerm != perm {
		return os.Chmod(path, perm)
	}

	return nil
}

// ValidateTokenFormat checks if a token has the correct format.
// It does not verify if the token is the actual stored token.
func ValidateTokenFormat(token string) bool {
	if len(token) != TokenLength*2 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

// CompareTokens compares two tokens in constant time to prevent timing attacks.
// Returns true if the tokens match, false otherwise.
func CompareTokens(provided, expected string) bool {
	// Length check must be done carefully - we still do constant-time comparison
	// even if lengths differ to avoid leaking length information through timing
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
