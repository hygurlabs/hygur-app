package edge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the edge agent's persisted configuration (~/.hygur-edge/config.json),
// edited via the local config UI. Holds the cloud connection + which local
// sources to push. The Proton password is stored here (file mode 0600).
type Config struct {
	Mode           string `json:"mode"`            // "cloud" = thin client (proxy + push); "" / "local" = full local engine
	Server         string `json:"server"`          // https://<tenant>.hygur.ai
	Token          string `json:"token"`           // device JWT
	Folder         string `json:"folder"`          // Files source (empty = off)
	ProtonUser     string `json:"proton_user"`     // empty = Proton off
	ProtonPassword string `json:"proton_password"` // Proton Bridge app password
	ProtonMailbox  string `json:"proton_mailbox"`  // e.g. "All Mail"
	IntervalSecs   int    `json:"interval_secs"`   // background loop; 0 = manual only
	BackfillCount  int    `json:"backfill_count"`  // mails fetched per folder on its first sync (0 = default)
}

// DefaultBackfillCount is how many recent mails a folder pulls on its first sync
// (before switching to incremental). Applied when Config.BackfillCount is unset.
const DefaultBackfillCount = 200

// Backfill returns the effective per-folder backfill count (the default when unset).
func (c *Config) Backfill() int {
	if c.BackfillCount > 0 {
		return c.BackfillCount
	}
	return DefaultBackfillCount
}

// DefaultConfigPath is ~/.hygur-edge/config.json (falls back to CWD-relative).
func DefaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".hygur-edge", "config.json")
	}
	return ".hygur-edge-config.json"
}

// StateDir is the directory holding the config + per-source watermarks.
func StateDir(configPath string) string { return filepath.Dir(configPath) }

// LoadConfig reads the config, returning a zero Config (not an error) when absent.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{ProtonMailbox: "All Mail"}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config (0600, creating the dir).
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadWatermark returns the persisted last-sync time, or zero if absent/invalid.
// RFC3339Nano: second-precision would truncate below sub-second file mtimes,
// re-pushing unchanged files forever.
func ReadWatermark(path string) time.Time {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// WriteWatermark persists t (creating the dir).
func WriteWatermark(path string, t time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(t.UTC().Format(time.RFC3339Nano)), 0o600)
}

// FolderState maps a mailbox name → its last-synced watermark. A folder ABSENT
// from the map has never been synced → it gets a most-recent-N backfill; a folder
// present syncs incrementally from its watermark. This per-folder model means a
// newly-checked folder backfills its history independently of the others.
type FolderState map[string]time.Time

// ReadFolderState loads the per-folder watermark map (empty when absent/invalid).
func ReadFolderState(path string) FolderState {
	st := FolderState{}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var raw map[string]string
	if json.Unmarshal(b, &raw) != nil {
		return st
	}
	for mbox, s := range raw {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			st[mbox] = t
		}
	}
	return st
}

// WriteFolderState persists the per-folder watermark map (0600, creating the dir).
func WriteFolderState(path string, st FolderState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw := make(map[string]string, len(st))
	for mbox, t := range st {
		raw[mbox] = t.UTC().Format(time.RFC3339Nano)
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
