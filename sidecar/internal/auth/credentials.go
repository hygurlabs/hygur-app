// Package auth handles authentication token generation and credential management.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Credential storage constants.
const (
	// CredentialsDirName is the name of the directory storing credentials.
	CredentialsDirName = "credentials"

	// CredentialFileExt is the file extension for credential files.
	CredentialFileExt = ".enc"

	// CredentialFilePerms are the permissions for credential files (owner read/write only).
	CredentialFilePerms = 0600

	// CredentialDirPerms are the permissions for the credentials directory.
	CredentialDirPerms = 0700

	// LLMCredentialID is the reserved connector-credential id under which the
	// LLM provider API key (the Authorization bearer token for hosted runtimes
	// like Mistral/OpenAI) is stored. It reuses the connector-credential
	// mechanism (encrypted at rest as connector_llm.enc) rather than living in
	// config.yaml, mirroring the rule that secrets never touch the config file.
	LLMCredentialID = "llm"

	// LLMCredentialField is the field name holding the API key inside the LLM
	// connector credential.
	LLMCredentialField = "api_key"
)

// Credential errors.
var (
	// ErrCredentialNotFound is returned when requested credentials don't exist.
	ErrCredentialNotFound = errors.New("credential not found")

	// ErrInvalidCredentialKey is returned when the encryption key is invalid.
	ErrInvalidCredentialKey = errors.New("invalid credential key: must be set via HYGUR_CRED_KEY environment variable")

	// ErrDecryptionFailed is returned when credential decryption fails.
	ErrDecryptionFailed = errors.New("credential decryption failed")

	// ErrEncryptionFailed is returned when credential encryption fails.
	ErrEncryptionFailed = errors.New("credential encryption failed")
)

// CredentialStore manages encrypted credential storage.
// It is safe for concurrent use.
type CredentialStore struct {
	baseDir string
	key     []byte // 32-byte AES-256 key derived from HYGUR_CRED_KEY
	mu      sync.RWMutex
}

// StoredCredential represents a saved mail credential.
// Passwords and tokens are encrypted at rest.
type StoredCredential struct {
	Source       string `json:"source"`                  // "proton" or "gmail"
	Username     string `json:"username,omitempty"`      // For Proton IMAP
	Password     string `json:"password,omitempty"`      // For Proton IMAP (encrypted)
	RefreshToken string `json:"refresh_token,omitempty"` // For Gmail OAuth (encrypted)
	ClientID     string `json:"client_id,omitempty"`     // For Gmail OAuth
	ClientSecret string `json:"client_secret,omitempty"` // For Gmail OAuth (encrypted)
}

// CredentialInfo represents credential metadata without sensitive data.
type CredentialInfo struct {
	Source   string `json:"source"`
	Username string `json:"username,omitempty"` // For Proton, shows username; for Gmail, shows "OAuth"
}

// MailAccountCredential represents the persisted credentials for a single
// mail account (one user mailbox). The same struct is used for both Proton
// IMAP and Gmail OAuth — fields not relevant to the provider stay empty.
type MailAccountCredential struct {
	AccountID    string `json:"account_id"`              // canonical identifier, typically email address
	Provider     string `json:"provider"`                // "proton" | "gmail"
	Email        string `json:"email,omitempty"`         // human-friendly label; usually equals AccountID
	Username     string `json:"username,omitempty"`      // Proton IMAP username
	Password     string `json:"password,omitempty"`      // Proton IMAP password
	RefreshToken string `json:"refresh_token,omitempty"` // Gmail OAuth refresh token
	ClientID     string `json:"client_id,omitempty"`     // Gmail OAuth client id
	ClientSecret string `json:"client_secret,omitempty"` // Gmail OAuth client secret
	CreatedAt    string `json:"created_at,omitempty"`    // RFC3339
}

// MailAccountInfo is the public listing entry: no secrets.
type MailAccountInfo struct {
	AccountID string `json:"account_id"`
	Provider  string `json:"provider"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at,omitempty"`
}

// mailAccountKey returns the on-disk key for a mail account credential.
// account IDs may contain "@" or other characters — filepath.Base sanitises
// path traversal but allows arbitrary email-address-shaped strings.
func mailAccountKey(accountID string) string {
	return "mailacct_" + filepath.Base(accountID)
}

// NewCredentialStore creates a new credential store.
// The baseDir should be the Hygur data directory (e.g., ~/.hygur).
// The encryption key is read from HYGUR_CRED_KEY. When that variable is not
// set, a machine-local key is generated once and persisted to baseDir/.cred_key
// with 0600 permissions so credentials can still survive restarts without any
// user setup.
func NewCredentialStore(baseDir string) (*CredentialStore, error) {
	keyStr := os.Getenv("HYGUR_CRED_KEY")
	if keyStr == "" {
		derived, err := loadOrCreateMachineKey(baseDir)
		if err != nil {
			return nil, fmt.Errorf("credential key unavailable: %w", err)
		}
		keyStr = derived
	}

	hash := sha256.Sum256([]byte(keyStr))

	credDir := filepath.Join(baseDir, CredentialsDirName)
	if err := os.MkdirAll(credDir, CredentialDirPerms); err != nil {
		return nil, fmt.Errorf("creating credentials directory: %w", err)
	}

	if err := os.Chmod(credDir, CredentialDirPerms); err != nil {
		return nil, fmt.Errorf("setting credentials directory permissions: %w", err)
	}

	return &CredentialStore{
		baseDir: credDir,
		key:     hash[:],
	}, nil
}

// loadOrCreateMachineKey returns a hex-encoded 32-byte key persisted next to
// the data directory. The file is created on first use with 0600 permissions.
func loadOrCreateMachineKey(baseDir string) (string, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", fmt.Errorf("creating data directory: %w", err)
	}
	keyPath := filepath.Join(baseDir, ".cred_key")
	if data, err := os.ReadFile(keyPath); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if trimmed != "" {
			return trimmed, nil
		}
	}

	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generating credential key: %w", err)
	}
	key := hex.EncodeToString(raw)
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		return "", fmt.Errorf("writing credential key: %w", err)
	}
	_ = os.Chmod(keyPath, 0o600)
	return key, nil
}

// SaveMailCredential saves or updates a mail credential for the given source.
// For Proton: requires username and password.
// For Gmail: requires refreshToken, and optionally clientID/clientSecret.
func (s *CredentialStore) SaveMailCredential(source, username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred := StoredCredential{
		Source:   source,
		Username: username,
		Password: password,
	}

	return s.saveCredential(cred)
}

// SaveGmailCredential saves Gmail OAuth credentials including the refresh token.
func (s *CredentialStore) SaveGmailCredential(refreshToken, clientID, clientSecret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred := StoredCredential{
		Source:       "gmail",
		RefreshToken: refreshToken,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}

	return s.saveCredential(cred)
}

// GetMailCredential retrieves the credential for the given source.
// Returns ErrCredentialNotFound if no credential exists.
func (s *CredentialStore) GetMailCredential(source string) (username, password string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, err := s.loadCredential(source)
	if err != nil {
		return "", "", err
	}

	return cred.Username, cred.Password, nil
}

// GetGmailCredential retrieves Gmail OAuth credentials.
// Returns ErrCredentialNotFound if no credential exists.
func (s *CredentialStore) GetGmailCredential() (refreshToken, clientID, clientSecret string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, err := s.loadCredential("gmail")
	if err != nil {
		return "", "", "", err
	}

	return cred.RefreshToken, cred.ClientID, cred.ClientSecret, nil
}

// DeleteMailCredential removes the credential for the given source.
// Returns nil if the credential doesn't exist.
func (s *CredentialStore) DeleteMailCredential(source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.credentialPath(source)
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting credential: %w", err)
	}
	return nil
}

// ListCredentials returns information about all saved credentials.
// Does not include sensitive data like passwords or tokens.
func (s *CredentialStore) ListCredentials() ([]CredentialInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CredentialInfo{}, nil
		}
		return nil, fmt.Errorf("reading credentials directory: %w", err)
	}

	var creds []CredentialInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), CredentialFileExt) {
			continue
		}

		source := strings.TrimSuffix(entry.Name(), CredentialFileExt)
		cred, err := s.loadCredential(source)
		if err != nil {
			continue // Skip invalid credentials
		}

		info := CredentialInfo{
			Source: cred.Source,
		}
		if cred.Source == "gmail" {
			info.Username = "OAuth"
		} else {
			info.Username = cred.Username
		}
		creds = append(creds, info)
	}

	return creds, nil
}

// HasCredential checks if a credential exists for the given source.
func (s *CredentialStore) HasCredential(source string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.credentialPath(source)
	_, err := os.Stat(filePath)
	return err == nil
}

// credentialPath returns the file path for a source's credential.
func (s *CredentialStore) credentialPath(source string) string {
	// Sanitize source name to prevent path traversal
	safeName := filepath.Base(source)
	return filepath.Join(s.baseDir, safeName+CredentialFileExt)
}

// saveCredential encrypts and saves a credential.
func (s *CredentialStore) saveCredential(cred StoredCredential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshaling credential: %w", err)
	}

	encrypted, err := s.encrypt(data)
	if err != nil {
		return err
	}

	filePath := s.credentialPath(cred.Source)
	if err := os.WriteFile(filePath, encrypted, CredentialFilePerms); err != nil {
		return fmt.Errorf("writing credential file: %w", err)
	}

	// Ensure file permissions
	if err := os.Chmod(filePath, CredentialFilePerms); err != nil {
		return fmt.Errorf("setting credential file permissions: %w", err)
	}

	return nil
}

// loadCredential loads and decrypts a credential.
func (s *CredentialStore) loadCredential(source string) (*StoredCredential, error) {
	filePath := s.credentialPath(source)
	encrypted, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("reading credential file: %w", err)
	}

	data, err := s.decrypt(encrypted)
	if err != nil {
		return nil, err
	}

	var cred StoredCredential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("unmarshaling credential: %w", err)
	}

	return &cred, nil
}

// encrypt encrypts data using AES-256-GCM.
func (s *CredentialStore) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("%w: generating nonce: %v", ErrEncryptionFailed, err)
	}

	// Prepend nonce to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// decrypt decrypts data using AES-256-GCM.
func (s *CredentialStore) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("%w: ciphertext too short", ErrDecryptionFailed)
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return plaintext, nil
}

// SaveMailAccountCredential persists credentials for a single mail account.
// AccountID must be non-empty and is used to key the on-disk file.
// CreatedAt is set automatically on first save.
func (s *CredentialStore) SaveMailAccountCredential(cred MailAccountCredential) error {
	if cred.AccountID == "" {
		return fmt.Errorf("account_id is required")
	}
	if cred.Provider == "" {
		return fmt.Errorf("provider is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Preserve CreatedAt across updates if already present.
	key := mailAccountKey(cred.AccountID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	if existing, err := os.ReadFile(filePath); err == nil {
		if plain, derr := s.decrypt(existing); derr == nil {
			var prev MailAccountCredential
			if json.Unmarshal(plain, &prev) == nil && prev.CreatedAt != "" {
				cred.CreatedAt = prev.CreatedAt
			}
		}
	}
	if cred.CreatedAt == "" {
		cred.CreatedAt = nowRFC3339()
	}

	data, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("marshaling mail account credential: %w", err)
	}
	encrypted, err := s.encrypt(data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filePath, encrypted, CredentialFilePerms); err != nil {
		return fmt.Errorf("writing mail account credential: %w", err)
	}
	if err := os.Chmod(filePath, CredentialFilePerms); err != nil {
		return fmt.Errorf("setting mail account credential permissions: %w", err)
	}
	return nil
}

// GetMailAccountCredential loads credentials for a specific account.
func (s *CredentialStore) GetMailAccountCredential(accountID string) (*MailAccountCredential, error) {
	if accountID == "" {
		return nil, fmt.Errorf("account_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := mailAccountKey(accountID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	encrypted, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("reading mail account credential: %w", err)
	}
	plain, err := s.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	var cred MailAccountCredential
	if err := json.Unmarshal(plain, &cred); err != nil {
		return nil, fmt.Errorf("unmarshaling mail account credential: %w", err)
	}
	return &cred, nil
}

// ListMailAccounts returns metadata for all stored mail account credentials.
// Secrets are stripped.
func (s *CredentialStore) ListMailAccounts() ([]MailAccountInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []MailAccountInfo{}, nil
		}
		return nil, fmt.Errorf("reading credentials directory: %w", err)
	}

	const prefix = "mailacct_"
	var accounts []MailAccountInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, CredentialFileExt) {
			continue
		}
		base := strings.TrimSuffix(name, CredentialFileExt)
		if !strings.HasPrefix(base, prefix) {
			continue
		}
		accountID := strings.TrimPrefix(base, prefix)
		cred, err := s.loadMailAccountUnlocked(accountID)
		if err != nil {
			continue
		}
		email := cred.Email
		if email == "" {
			email = cred.AccountID
		}
		accounts = append(accounts, MailAccountInfo{
			AccountID: cred.AccountID,
			Provider:  cred.Provider,
			Email:     email,
			CreatedAt: cred.CreatedAt,
		})
	}
	return accounts, nil
}

// DeleteMailAccount removes a stored mail account credential.
// Returns nil if the file does not exist.
func (s *CredentialStore) DeleteMailAccount(accountID string) error {
	if accountID == "" {
		return fmt.Errorf("account_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := mailAccountKey(accountID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting mail account credential: %w", err)
	}
	return nil
}

// loadMailAccountUnlocked is the lock-free body shared by GetMailAccountCredential
// and ListMailAccounts. The caller MUST hold s.mu (read or write).
func (s *CredentialStore) loadMailAccountUnlocked(accountID string) (*MailAccountCredential, error) {
	key := mailAccountKey(accountID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	encrypted, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	plain, err := s.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	var cred MailAccountCredential
	if err := json.Unmarshal(plain, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

// nowRFC3339 returns the current time in RFC3339 format. Extracted so tests
// can assert on the format without colliding with time.Now().
func nowRFC3339() string {
	return timeNow().UTC().Format("2006-01-02T15:04:05Z07:00")
}

// timeNow is overridable in tests.
var timeNow = func() time.Time { return time.Now() }

// connectorCredentialKey retourne la clé de stockage pour un connecteur donné.
// filepath.Base() empêche les attaques par path traversal.
func connectorCredentialKey(connectorID string) string {
	return "connector_" + filepath.Base(connectorID)
}

// SaveConnectorCredential enregistre les credentials chiffrés pour un connecteur.
// Les fields sont les clés du ConfigSchema de type FieldSecret.
// Le connectorID est sanitisé via filepath.Base pour prévenir le path traversal.
//
// La fonction FUSIONNE les nouveaux champs avec les credentials existants au lieu
// de les écraser. Cela évite que pushAllSecretsToSidecar() côté macOS ne détruise
// les tokens OAuth (refresh_token, etc.) en envoyant uniquement le marqueur UI
// gmail_oauth="connected".
func (s *CredentialStore) SaveConnectorCredential(connectorID string, fields map[string]string) error {
	connectorID = filepath.Base(connectorID)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Read existing credentials to merge — exclusive lock permits the read.
	key := connectorCredentialKey(connectorID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	merged := make(map[string]string)
	if existing, err := os.ReadFile(filePath); err == nil {
		if plain, err := s.decrypt(existing); err == nil {
			_ = json.Unmarshal(plain, &merged)
		}
	}
	for k, v := range fields {
		merged[k] = v
	}

	data, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshaling connector credential: %w", err)
	}

	encrypted, err := s.encrypt(data)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filePath, encrypted, CredentialFilePerms); err != nil {
		return fmt.Errorf("writing connector credential file: %w", err)
	}
	if err := os.Chmod(filePath, CredentialFilePerms); err != nil {
		return fmt.Errorf("setting connector credential file permissions: %w", err)
	}
	return nil
}

// GetConnectorCredential récupère les credentials d'un connecteur.
// Retourne ErrCredentialNotFound si aucun credential n'existe pour cet ID.
// Le connectorID est sanitisé via filepath.Base pour prévenir le path traversal.
func (s *CredentialStore) GetConnectorCredential(connectorID string) (map[string]string, error) {
	connectorID = filepath.Base(connectorID)
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := connectorCredentialKey(connectorID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	encrypted, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCredentialNotFound
		}
		return nil, fmt.Errorf("reading connector credential file: %w", err)
	}

	data, err := s.decrypt(encrypted)
	if err != nil {
		return nil, err
	}

	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("unmarshaling connector credential: %w", err)
	}
	return fields, nil
}

// DeleteConnectorCredential supprime les credentials d'un connecteur.
// Retourne nil si le credential n'existe pas.
// Le connectorID est sanitisé via filepath.Base pour prévenir le path traversal.
func (s *CredentialStore) DeleteConnectorCredential(connectorID string) error {
	connectorID = filepath.Base(connectorID)
	s.mu.Lock()
	defer s.mu.Unlock()

	key := connectorCredentialKey(connectorID)
	filePath := filepath.Join(s.baseDir, key+CredentialFileExt)
	err := os.Remove(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting connector credential: %w", err)
	}
	return nil
}

// Utility functions for standalone use without a store instance.

// deriveKey derives a 32-byte key from the HYGUR_CRED_KEY environment variable.
func deriveKey() ([]byte, error) {
	keyStr := os.Getenv("HYGUR_CRED_KEY")
	if keyStr == "" {
		return nil, ErrInvalidCredentialKey
	}
	hash := sha256.Sum256([]byte(keyStr))
	return hash[:], nil
}

// EncryptString encrypts a string value and returns hex-encoded ciphertext.
func EncryptString(plaintext string) (string, error) {
	key, err := deriveKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEncryptionFailed, err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("%w: generating nonce: %v", ErrEncryptionFailed, err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// DecryptString decrypts a hex-encoded ciphertext and returns the plaintext.
func DecryptString(hexCiphertext string) (string, error) {
	key, err := deriveKey()
	if err != nil {
		return "", err
	}

	ciphertext, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", fmt.Errorf("%w: invalid hex encoding", ErrDecryptionFailed)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("%w: ciphertext too short", ErrDecryptionFailed)
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return string(plaintext), nil
}
