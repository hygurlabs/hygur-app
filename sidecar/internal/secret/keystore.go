// Package secret stores small local secrets — today the SQLCipher database key
// — in the OS keychain (macOS Keychain, Windows Credential Manager, Linux Secret
// Service) via go-keyring. It is used only on the local desktop: the cloud
// server supplies the key out-of-band via the HYGUR_DB_KEY environment variable
// and never touches the keychain.
package secret

import "github.com/zalando/go-keyring"

const (
	keychainService = "hygur"
	dbKeyAccount    = "db-key"
)

// KeyStore is the minimal surface the handlers depend on, so tests inject a fake
// instead of touching the real OS keychain.
type KeyStore interface {
	// DBKey returns the stored SQLCipher key and whether one is present.
	DBKey() (string, bool)
	// SetDBKey stores (or replaces) the SQLCipher key.
	SetDBKey(key string) error
}

// Keychain is the production KeyStore backed by the OS keychain. A missing
// backend (e.g. a headless Linux server with no Secret Service) is treated as
// "no key" rather than an error — the cloud path uses HYGUR_DB_KEY instead.
type Keychain struct{}

// DBKey reads the SQLCipher key from the OS keychain.
func (Keychain) DBKey() (string, bool) {
	v, err := keyring.Get(keychainService, dbKeyAccount)
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

// SetDBKey writes the SQLCipher key to the OS keychain.
func (Keychain) SetDBKey(key string) error {
	return keyring.Set(keychainService, dbKeyAccount, key)
}
