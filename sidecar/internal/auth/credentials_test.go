package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialStore_SaveAndGetMailCredential(t *testing.T) {
	// Set up test environment
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key-for-unit-tests")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a credential
	err = store.SaveMailCredential("proton", "user@proton.me", "secret-password")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Retrieve it
	username, password, err := store.GetMailCredential("proton")
	if err != nil {
		t.Fatalf("failed to get credential: %v", err)
	}

	if username != "user@proton.me" {
		t.Errorf("expected username 'user@proton.me', got '%s'", username)
	}
	if password != "secret-password" {
		t.Errorf("expected password 'secret-password', got '%s'", password)
	}
}

func TestCredentialStore_SaveAndGetGmailCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key-for-gmail")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save Gmail credential
	err = store.SaveGmailCredential("refresh-token-123", "client-id-456", "client-secret-789")
	if err != nil {
		t.Fatalf("failed to save Gmail credential: %v", err)
	}

	// Retrieve it
	refreshToken, clientID, clientSecret, err := store.GetGmailCredential()
	if err != nil {
		t.Fatalf("failed to get Gmail credential: %v", err)
	}

	if refreshToken != "refresh-token-123" {
		t.Errorf("expected refresh token 'refresh-token-123', got '%s'", refreshToken)
	}
	if clientID != "client-id-456" {
		t.Errorf("expected client ID 'client-id-456', got '%s'", clientID)
	}
	if clientSecret != "client-secret-789" {
		t.Errorf("expected client secret 'client-secret-789', got '%s'", clientSecret)
	}
}

func TestCredentialStore_DeleteMailCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key-for-delete")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a credential
	err = store.SaveMailCredential("proton", "user@proton.me", "password")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Verify it exists
	if !store.HasCredential("proton") {
		t.Fatal("expected credential to exist")
	}

	// Delete it
	err = store.DeleteMailCredential("proton")
	if err != nil {
		t.Fatalf("failed to delete credential: %v", err)
	}

	// Verify it's gone
	if store.HasCredential("proton") {
		t.Error("expected credential to be deleted")
	}

	// Attempting to get it should return ErrCredentialNotFound
	_, _, err = store.GetMailCredential("proton")
	if err != ErrCredentialNotFound {
		t.Errorf("expected ErrCredentialNotFound, got %v", err)
	}
}

func TestCredentialStore_DeleteNonExistent(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Deleting non-existent credential should not error
	err = store.DeleteMailCredential("nonexistent")
	if err != nil {
		t.Errorf("expected no error when deleting non-existent credential, got %v", err)
	}
}

func TestCredentialStore_ListCredentials(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key-for-list")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Initially empty
	creds, err := store.ListCredentials()
	if err != nil {
		t.Fatalf("failed to list credentials: %v", err)
	}
	if len(creds) != 0 {
		t.Errorf("expected 0 credentials, got %d", len(creds))
	}

	// Add some credentials
	err = store.SaveMailCredential("proton", "user@proton.me", "password")
	if err != nil {
		t.Fatalf("failed to save proton credential: %v", err)
	}

	err = store.SaveGmailCredential("refresh-token", "client-id", "client-secret")
	if err != nil {
		t.Fatalf("failed to save gmail credential: %v", err)
	}

	// List again
	creds, err = store.ListCredentials()
	if err != nil {
		t.Fatalf("failed to list credentials: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(creds))
	}

	// Verify contents (no sensitive data)
	credMap := make(map[string]CredentialInfo)
	for _, c := range creds {
		credMap[c.Source] = c
	}

	protonCred, ok := credMap["proton"]
	if !ok {
		t.Fatal("expected proton credential in list")
	}
	if protonCred.Username != "user@proton.me" {
		t.Errorf("expected proton username 'user@proton.me', got '%s'", protonCred.Username)
	}

	gmailCred, ok := credMap["gmail"]
	if !ok {
		t.Fatal("expected gmail credential in list")
	}
	if gmailCred.Username != "OAuth" {
		t.Errorf("expected gmail username 'OAuth', got '%s'", gmailCred.Username)
	}
}

func TestCredentialStore_HasCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Initially doesn't have credential
	if store.HasCredential("proton") {
		t.Error("expected no proton credential initially")
	}

	// Save credential
	err = store.SaveMailCredential("proton", "user", "pass")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Now it should exist
	if !store.HasCredential("proton") {
		t.Error("expected proton credential to exist")
	}
}

func TestCredentialStore_OverwriteCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save initial credential
	err = store.SaveMailCredential("proton", "user1", "password1")
	if err != nil {
		t.Fatalf("failed to save initial credential: %v", err)
	}

	// Overwrite with new credential
	err = store.SaveMailCredential("proton", "user2", "password2")
	if err != nil {
		t.Fatalf("failed to save updated credential: %v", err)
	}

	// Verify it was updated
	username, password, err := store.GetMailCredential("proton")
	if err != nil {
		t.Fatalf("failed to get credential: %v", err)
	}

	if username != "user2" {
		t.Errorf("expected username 'user2', got '%s'", username)
	}
	if password != "password2" {
		t.Errorf("expected password 'password2', got '%s'", password)
	}
}

func TestCredentialStore_MissingKey_GeneratesPersistentKey(t *testing.T) {
	tempDir := t.TempDir()

	os.Unsetenv("HYGUR_CRED_KEY")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("expected fallback key generation to succeed, got %v", err)
	}
	if store == nil {
		t.Fatal("expected a credential store instance")
	}

	keyPath := filepath.Join(tempDir, ".cred_key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected generated key file at %s, got %v", keyPath, err)
	}

	// A second invocation should reuse the same key so saved credentials remain
	// decryptable across restarts.
	first := store.key
	store2, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	if string(first) != string(store2.key) {
		t.Error("expected persistent key to be reused across opens")
	}
}

func TestCredentialStore_FilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a credential
	err = store.SaveMailCredential("proton", "user", "password")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Check directory permissions
	credDir := filepath.Join(tempDir, CredentialsDirName)
	dirInfo, err := os.Stat(credDir)
	if err != nil {
		t.Fatalf("failed to stat credentials directory: %v", err)
	}
	if dirInfo.Mode().Perm() != CredentialDirPerms {
		t.Errorf("expected directory permissions %o, got %o", CredentialDirPerms, dirInfo.Mode().Perm())
	}

	// Check file permissions
	filePath := filepath.Join(credDir, "proton"+CredentialFileExt)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("failed to stat credential file: %v", err)
	}
	if fileInfo.Mode().Perm() != CredentialFilePerms {
		t.Errorf("expected file permissions %o, got %o", CredentialFilePerms, fileInfo.Mode().Perm())
	}
}

func TestCredentialStore_EncryptionDecryption(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a credential
	err = store.SaveMailCredential("proton", "user", "secret-password-123")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Read raw file contents
	credDir := filepath.Join(tempDir, CredentialsDirName)
	filePath := filepath.Join(credDir, "proton"+CredentialFileExt)
	rawData, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read credential file: %v", err)
	}

	// Verify data is encrypted (should not contain plaintext password)
	if string(rawData) == "secret-password-123" {
		t.Error("password appears to be stored in plaintext")
	}

	// Data should not be valid JSON (encrypted)
	if rawData[0] == '{' {
		t.Error("data appears to be unencrypted JSON")
	}
}

func TestCredentialStore_WrongKeyDecryption(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "encryption-key-1")

	store1, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a credential with key 1
	err = store1.SaveMailCredential("proton", "user", "password")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// Try to read with different key
	t.Setenv("HYGUR_CRED_KEY", "encryption-key-2")
	store2, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store with key 2: %v", err)
	}

	// Should fail to decrypt
	_, _, err = store2.GetMailCredential("proton")
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestCredentialStore_PathTraversalPrevention(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Attempt path traversal in source name
	maliciousSource := "../../../etc/passwd"
	err = store.SaveMailCredential(maliciousSource, "user", "password")
	if err != nil {
		t.Fatalf("failed to save credential: %v", err)
	}

	// The file should be saved in the credentials directory, not outside
	credDir := filepath.Join(tempDir, CredentialsDirName)
	expectedPath := filepath.Join(credDir, "passwd"+CredentialFileExt)

	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Error("expected sanitized file to exist in credentials directory")
	}

	// Verify /etc/passwd was not created
	if _, err := os.Stat("/etc/passwd.enc"); err == nil {
		t.Error("path traversal was not prevented")
	}
}

func TestEncryptString(t *testing.T) {
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	plaintext := "sensitive-data-123"
	encrypted, err := EncryptString(plaintext)
	if err != nil {
		t.Fatalf("failed to encrypt string: %v", err)
	}

	// Should be hex-encoded
	if encrypted == plaintext {
		t.Error("encrypted string should not equal plaintext")
	}

	// Should be able to decrypt
	decrypted, err := DecryptString(encrypted)
	if err != nil {
		t.Fatalf("failed to decrypt string: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("expected '%s', got '%s'", plaintext, decrypted)
	}
}

func TestDecryptString_InvalidHex(t *testing.T) {
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	_, err := DecryptString("not-valid-hex!")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestDecryptString_MissingKey(t *testing.T) {
	os.Unsetenv("HYGUR_CRED_KEY")

	_, err := DecryptString("aabbccdd")
	if err != ErrInvalidCredentialKey {
		t.Errorf("expected ErrInvalidCredentialKey, got %v", err)
	}
}

func TestEncryptString_MissingKey(t *testing.T) {
	os.Unsetenv("HYGUR_CRED_KEY")

	_, err := EncryptString("test")
	if err != ErrInvalidCredentialKey {
		t.Errorf("expected ErrInvalidCredentialKey, got %v", err)
	}
}

func TestCredentialStore_GetNonExistentCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	_, _, err = store.GetMailCredential("nonexistent")
	if err != ErrCredentialNotFound {
		t.Errorf("expected ErrCredentialNotFound, got %v", err)
	}

	_, _, _, err = store.GetGmailCredential()
	if err != ErrCredentialNotFound {
		t.Errorf("expected ErrCredentialNotFound for Gmail, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Generic connector credential tests (Phase 1 plugin system)
// ---------------------------------------------------------------------------

func TestCredentialStore_SaveAndGetConnectorCredential_RoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-connector-cred-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	fields := map[string]string{
		"username": "alice@example.com",
		"token":    "super-secret-token-abc123",
		"endpoint": "https://api.example.com",
	}

	if err := store.SaveConnectorCredential("notion", fields); err != nil {
		t.Fatalf("SaveConnectorCredential failed: %v", err)
	}

	got, err := store.GetConnectorCredential("notion")
	if err != nil {
		t.Fatalf("GetConnectorCredential failed: %v", err)
	}

	if len(got) != len(fields) {
		t.Fatalf("expected %d fields, got %d", len(fields), len(got))
	}
	for k, v := range fields {
		if got[k] != v {
			t.Errorf("field %q: expected %q, got %q", k, v, got[k])
		}
	}
}

func TestCredentialStore_ConnectorCredential_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-connector-traversal-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Attempt path traversal in connectorID.
	maliciousID := "../etc/passwd"
	fields := map[string]string{"key": "value"}

	if err := store.SaveConnectorCredential(maliciousID, fields); err != nil {
		t.Fatalf("SaveConnectorCredential failed: %v", err)
	}

	// filepath.Base("../etc/passwd") == "passwd"
	// File should be stored as connector_passwd.enc inside the credentials dir.
	credDir := filepath.Join(tempDir, CredentialsDirName)
	expectedPath := filepath.Join(credDir, "connector_passwd"+CredentialFileExt)

	if _, statErr := os.Stat(expectedPath); os.IsNotExist(statErr) {
		t.Errorf("expected sanitized file %q to exist", expectedPath)
	}

	// Verify retrieval works with the traversal ID (it normalizes internally).
	got, err := store.GetConnectorCredential(maliciousID)
	if err != nil {
		t.Fatalf("GetConnectorCredential with traversal ID failed: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("expected value %q, got %q", "value", got["key"])
	}
}

func TestCredentialStore_DeleteConnectorCredential_GetAfterDeleteReturnsNotFound(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-connector-delete-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	fields := map[string]string{"api_key": "top-secret"}

	// Save, then delete.
	if err := store.SaveConnectorCredential("linear", fields); err != nil {
		t.Fatalf("SaveConnectorCredential failed: %v", err)
	}
	if err := store.DeleteConnectorCredential("linear"); err != nil {
		t.Fatalf("DeleteConnectorCredential failed: %v", err)
	}

	// Get should return ErrCredentialNotFound.
	_, err = store.GetConnectorCredential("linear")
	if err != ErrCredentialNotFound {
		t.Errorf("expected ErrCredentialNotFound after delete, got %v", err)
	}
}

func TestCredentialStore_DeleteConnectorCredential_NonExistent_NoError(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-connector-del-ne-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	if err := store.DeleteConnectorCredential("nonexistent"); err != nil {
		t.Errorf("expected no error deleting non-existent connector credential, got %v", err)
	}
}

func TestCredentialStore_ConnectorCredential_DoesNotCollideWithMailCredential(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-no-collision-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Save a mail credential and a connector credential with the same base name.
	if err := store.SaveMailCredential("proton", "user@proton.me", "mailpass"); err != nil {
		t.Fatalf("SaveMailCredential failed: %v", err)
	}
	if err := store.SaveConnectorCredential("proton", map[string]string{"token": "conntoken"}); err != nil {
		t.Fatalf("SaveConnectorCredential failed: %v", err)
	}

	// Mail credential should be unaffected.
	username, password, err := store.GetMailCredential("proton")
	if err != nil {
		t.Fatalf("GetMailCredential failed after connector save: %v", err)
	}
	if username != "user@proton.me" || password != "mailpass" {
		t.Errorf("mail credential was overwritten: username=%q password=%q", username, password)
	}

	// Connector credential should return its own fields.
	fields, err := store.GetConnectorCredential("proton")
	if err != nil {
		t.Fatalf("GetConnectorCredential failed: %v", err)
	}
	if fields["token"] != "conntoken" {
		t.Errorf("connector credential returned wrong value: %q", fields["token"])
	}
}

func TestCredentialStore_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-encryption-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create credential store: %v", err)
	}

	// Run concurrent operations
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			// Mix of read and write operations
			if n%2 == 0 {
				_ = store.SaveMailCredential("proton", "user", "password")
			} else {
				_, _ = store.ListCredentials()
				_ = store.HasCredential("proton")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestCredentialStore_MailAccountCRUD(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "test-mail-account-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Save Gmail account
	if err := store.SaveMailAccountCredential(MailAccountCredential{
		AccountID:    "alice@gmail.com",
		Provider:     "gmail",
		Email:        "alice@gmail.com",
		RefreshToken: "rt-abc",
		ClientID:     "cid",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("save gmail account: %v", err)
	}

	// Save Proton account
	if err := store.SaveMailAccountCredential(MailAccountCredential{
		AccountID: "bob@proton.me",
		Provider:  "proton",
		Email:     "bob@proton.me",
		Username:  "bob@proton.me",
		Password:  "bridge-pass",
	}); err != nil {
		t.Fatalf("save proton account: %v", err)
	}

	// List
	accounts, err := store.ListMailAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}

	// Get Gmail
	got, err := store.GetMailAccountCredential("alice@gmail.com")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	if got.RefreshToken != "rt-abc" || got.Provider != "gmail" {
		t.Errorf("alice creds wrong: %+v", got)
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be set on first save")
	}
	originalCreatedAt := got.CreatedAt

	// Update Gmail keeps CreatedAt
	if err := store.SaveMailAccountCredential(MailAccountCredential{
		AccountID:    "alice@gmail.com",
		Provider:     "gmail",
		Email:        "alice@gmail.com",
		RefreshToken: "rt-new",
		ClientID:     "cid",
		ClientSecret: "secret",
	}); err != nil {
		t.Fatalf("update alice: %v", err)
	}
	got, _ = store.GetMailAccountCredential("alice@gmail.com")
	if got.RefreshToken != "rt-new" {
		t.Errorf("expected updated refresh token, got %s", got.RefreshToken)
	}
	if got.CreatedAt != originalCreatedAt {
		t.Errorf("CreatedAt should be preserved across updates: was %s, got %s", originalCreatedAt, got.CreatedAt)
	}

	// Delete
	if err := store.DeleteMailAccount("alice@gmail.com"); err != nil {
		t.Fatalf("delete alice: %v", err)
	}
	if _, err := store.GetMailAccountCredential("alice@gmail.com"); err != ErrCredentialNotFound {
		t.Errorf("expected not found after delete, got %v", err)
	}

	// Bob still present
	accounts, _ = store.ListMailAccounts()
	if len(accounts) != 1 || accounts[0].AccountID != "bob@proton.me" {
		t.Errorf("expected only bob remaining, got %+v", accounts)
	}

	// Validation errors
	if err := store.SaveMailAccountCredential(MailAccountCredential{Provider: "gmail"}); err == nil {
		t.Error("expected error when account_id missing")
	}
	if err := store.SaveMailAccountCredential(MailAccountCredential{AccountID: "x@y"}); err == nil {
		t.Error("expected error when provider missing")
	}
	if _, err := store.GetMailAccountCredential(""); err == nil {
		t.Error("expected error on empty account id")
	}
	if err := store.DeleteMailAccount(""); err == nil {
		t.Error("expected error on empty account id")
	}
}

func TestCredentialStore_MailAccountIsolatedFromConnectorAndLegacy(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "isolation-key")

	store, err := NewCredentialStore(tempDir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Mix legacy + connector + multi-account credentials.
	if err := store.SaveMailCredential("proton", "legacy-user", "legacy-pass"); err != nil {
		t.Fatalf("save legacy: %v", err)
	}
	if err := store.SaveConnectorCredential("mail", map[string]string{"refresh_token": "rt"}); err != nil {
		t.Fatalf("save connector: %v", err)
	}
	if err := store.SaveMailAccountCredential(MailAccountCredential{
		AccountID: "alice@gmail.com", Provider: "gmail", RefreshToken: "rt-new",
	}); err != nil {
		t.Fatalf("save account: %v", err)
	}

	// ListMailAccounts must only return mailacct_* entries.
	accounts, err := store.ListMailAccounts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(accounts) != 1 || accounts[0].AccountID != "alice@gmail.com" {
		t.Errorf("expected single account, got %+v", accounts)
	}

	// Legacy credential still readable.
	if _, _, err := store.GetMailCredential("proton"); err != nil {
		t.Errorf("legacy still readable, got %v", err)
	}

	// Verify file persistence: all three live in baseDir with distinct names.
	files, _ := os.ReadDir(filepath.Join(tempDir, CredentialsDirName))
	if len(files) != 3 {
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Name()
		}
		t.Errorf("expected 3 credential files, got %d: %v", len(files), names)
	}
}
