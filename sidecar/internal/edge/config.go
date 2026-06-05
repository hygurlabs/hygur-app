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
	Server         string `json:"server"`          // https://<tenant>.hygur.ai
	Token          string `json:"token"`           // device JWT
	Folder         string `json:"folder"`          // Files source (empty = off)
	ProtonUser     string `json:"proton_user"`     // empty = Proton off
	ProtonPassword string `json:"proton_password"` // Proton Bridge app password
	ProtonMailbox  string `json:"proton_mailbox"`  // e.g. "All Mail"
	IntervalSecs   int    `json:"interval_secs"`   // background loop; 0 = manual only
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
