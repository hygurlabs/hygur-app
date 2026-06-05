package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/secret"
	"github.com/rs/zerolog"
)

// EncryptionHandler turns local at-rest encryption on for the desktop app. The
// key lives in the OS keychain (secret.KeyStore); the sidecar reads it at boot
// (see cmd/hygur) and store.Open migrates the plaintext DB on the first keyed
// run. When HYGUR_DB_KEY is supplied via the environment (cloud / tenant DEK),
// encryption is env-managed and not user-toggleable here.
type EncryptionHandler struct {
	keys       secret.KeyStore
	envManaged bool
	logger     zerolog.Logger
}

// NewEncryptionHandler builds the handler. envManaged is true when the key comes
// from HYGUR_DB_KEY (cloud), in which case Enable is a no-op.
func NewEncryptionHandler(keys secret.KeyStore, envManaged bool, logger zerolog.Logger) *EncryptionHandler {
	return &EncryptionHandler{
		keys:       keys,
		envManaged: envManaged,
		logger:     logger.With().Str("handler", "encryption").Logger(),
	}
}

// Status (GET /admin/db/encryption) reports whether at-rest encryption is on.
func (h *EncryptionHandler) Status(w http.ResponseWriter, r *http.Request) {
	_, ok := h.keys.DBKey()
	writeEncryptionJSON(w, http.StatusOK, map[string]any{
		"enabled":     ok || h.envManaged,
		"env_managed": h.envManaged,
	})
}

// Enable (POST /admin/db/encrypt) generates a 256-bit key and stores it in the
// OS keychain. The database is migrated to encrypted on the next restart.
func (h *EncryptionHandler) Enable(w http.ResponseWriter, r *http.Request) {
	if h.envManaged {
		writeEncryptionJSON(w, http.StatusOK, map[string]any{
			"status": "already_enabled", "restart_required": false,
		})
		return
	}
	if _, ok := h.keys.DBKey(); ok {
		writeEncryptionJSON(w, http.StatusOK, map[string]any{
			"status": "already_enabled", "restart_required": false,
		})
		return
	}
	key, err := randomHexKey(32)
	if err != nil {
		http.Error(w, "key generation failed", http.StatusInternalServerError)
		return
	}
	if err := h.keys.SetDBKey(key); err != nil {
		h.logger.Error().Err(err).Msg("failed to store DB key in keychain")
		http.Error(w, "could not store the key in the OS keychain", http.StatusInternalServerError)
		return
	}
	h.logger.Info().Msg("local encryption enabled; will migrate on next start")
	writeEncryptionJSON(w, http.StatusOK, map[string]any{
		"status": "staged", "restart_required": true,
	})
}

func randomHexKey(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeEncryptionJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
