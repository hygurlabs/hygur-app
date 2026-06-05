package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// BackupHandler serves a downloadable database snapshot and accepts an uploaded
// snapshot to restore on the next boot. Cross-surface (WebUI / Tauri / browser):
// the sidecar owns the file; the client just downloads or uploads bytes.
type BackupHandler struct {
	db     *store.DB
	dbPath string // live DB file (<dataDir>/hygur.db)
	dbKey  string // SQLCipher key when local encryption is on; "" = plaintext
	logger zerolog.Logger
}

// NewBackupHandler builds a BackupHandler. dbKey mirrors HYGUR_DB_KEY so the
// snapshot preserves the live DB's at-rest encryption.
func NewBackupHandler(db *store.DB, dbPath, dbKey string, logger zerolog.Logger) *BackupHandler {
	return &BackupHandler{
		db:     db,
		dbPath: dbPath,
		dbKey:  dbKey,
		logger: logger.With().Str("handler", "backup").Logger(),
	}
}

// Download (GET /admin/db/backup) streams a consistent snapshot of the DB,
// preserving its encryption state (plaintext→plaintext, keyed→keyed-same-key).
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	tmp := filepath.Join(os.TempDir(), "hygur-backup-"+uuid.NewString()+".db")
	_ = os.Remove(tmp)
	if err := h.db.BackupTo(r.Context(), tmp, h.dbKey); err != nil {
		h.logger.Error().Err(err).Msg("backup failed")
		http.Error(w, "backup failed", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp)

	f, err := os.Open(tmp)
	if err != nil {
		http.Error(w, "backup read failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "backup stat failed", http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("hygur-backup-%s.db", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	w.Header().Set("X-Hygur-Encrypted", fmt.Sprintf("%t", h.dbKey != ""))
	if _, err := io.Copy(w, f); err != nil {
		h.logger.Warn().Err(err).Msg("backup stream interrupted")
	}
}

// SaveLocal (POST /admin/db/backup/save) writes a snapshot to a discoverable
// local folder (~/Downloads, falling back to <dataDir>/backups) and returns its
// path. The desktop app's webview can't trigger a browser download, but the
// sidecar runs on the same machine — so it just writes the file. Remote clients
// use Download (streamed over HTTP) instead.
func (h *BackupHandler) SaveLocal(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	dir := backupDir(h.dbPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.logger.Error().Err(err).Str("dir", dir).Msg("cannot create backup folder")
		http.Error(w, "cannot create backup folder", http.StatusInternalServerError)
		return
	}
	name := fmt.Sprintf("hygur-backup-%s.db", time.Now().Format("20060102-150405"))
	dest := filepath.Join(dir, name)
	_ = os.Remove(dest) // VACUUM INTO requires the target not to exist
	if err := h.db.BackupTo(r.Context(), dest, h.dbKey); err != nil {
		h.logger.Error().Err(err).Msg("backup save failed")
		http.Error(w, "backup failed", http.StatusInternalServerError)
		return
	}
	h.logger.Info().Str("path", dest).Msg("backup saved locally")
	writeBackupJSON(w, http.StatusOK, map[string]any{
		"status":    "saved",
		"path":      dest,
		"encrypted": h.dbKey != "",
	})
}

// backupDir prefers ~/Downloads (where a user expects a saved file), falling
// back to a "backups" folder beside the live DB.
func backupDir(dbPath string) string {
	if home, err := os.UserHomeDir(); err == nil {
		dl := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
			return dl
		}
	}
	if dbPath != "" {
		return filepath.Join(filepath.Dir(dbPath), "backups")
	}
	return os.TempDir()
}

// Restore (POST /admin/db/restore, multipart field "file") validates the
// uploaded database and stages it for the next boot (store.ApplyPendingRestore
// swaps it in). The app must be restarted to apply.
func (h *BackupHandler) Restore(w http.ResponseWriter, r *http.Request) {
	if h.dbPath == "" {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(1 << 30); err != nil { // buffer up to 1 GiB to disk
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file'", http.StatusBadRequest)
		return
	}
	defer file.Close()

	pending := h.dbPath + ".restore-pending"
	out, err := os.OpenFile(pending, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		http.Error(w, "cannot stage restore", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		_ = os.Remove(pending)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	out.Close()

	// Validate before committing — a bad/foreign file must not brick the boot.
	if err := store.QuickCheck(r.Context(), pending, h.dbKey); err != nil {
		_ = os.Remove(pending)
		h.logger.Warn().Err(err).Msg("restore rejected: not a usable database")
		http.Error(w, "the uploaded file is not a valid Hygur database (or needs a different key)", http.StatusBadRequest)
		return
	}

	h.logger.Info().Msg("restore staged; will apply on next start")
	writeBackupJSON(w, http.StatusOK, map[string]any{
		"status":           "staged",
		"restart_required": true,
	})
}

func writeBackupJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
