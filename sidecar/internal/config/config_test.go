package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Create a temporary directory with no config file. We also redirect
	// HOME so Viper's "$HOME/.hygur" search path resolves to an empty dir
	// — without this the developer's real ~/.hygur/config.yaml leaks into
	// the test and overrides the defaults this test asserts on.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// Verify defaults
	if cfg.Server.Host != DefaultHost {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, DefaultHost)
	}
	if cfg.Server.Port != DefaultPort {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, DefaultPort)
	}
	if cfg.Server.ReadTimeout != DefaultReadTimeout {
		t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, DefaultReadTimeout)
	}
	if cfg.Server.WriteTimeout != DefaultWriteTimeout {
		t.Errorf("Server.WriteTimeout = %v, want %v", cfg.Server.WriteTimeout, DefaultWriteTimeout)
	}
	if cfg.Server.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("Server.ShutdownTimeout = %v, want %v", cfg.Server.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if cfg.LMStudio.URL != DefaultLMStudioURL {
		t.Errorf("LMStudio.URL = %q, want %q", cfg.LMStudio.URL, DefaultLMStudioURL)
	}
	if cfg.LMStudio.Timeout != DefaultLMStudioTimeout {
		t.Errorf("LMStudio.Timeout = %v, want %v", cfg.LMStudio.Timeout, DefaultLMStudioTimeout)
	}
	if cfg.LMStudio.MaxRetries != DefaultMaxRetries {
		t.Errorf("LMStudio.MaxRetries = %d, want %d", cfg.LMStudio.MaxRetries, DefaultMaxRetries)
	}
	if cfg.Store.Path != DefaultStorePath {
		t.Errorf("Store.Path = %q, want %q", cfg.Store.Path, DefaultStorePath)
	}
	if cfg.Logging.Level != DefaultLogLevel {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, DefaultLogLevel)
	}
}

func TestLoad_FromYAML(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `
server:
  host: "0.0.0.0"
  port: 9000
  read_timeout: 60s
  write_timeout: 60s
  shutdown_timeout: 20s

lm_studio:
  url: "http://custom:5000"
  model_default: "test-model"
  timeout: 240s
  max_retries: 5

store:
  path: "/custom/path/db.sqlite"

logging:
  level: "debug"
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := LoadWithOptions(&LoadOptions{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("LoadWithOptions() returned error: %v", err)
	}

	// Verify YAML values
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9000)
	}
	if cfg.Server.ReadTimeout != 60*time.Second {
		t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, 60*time.Second)
	}
	if cfg.LMStudio.URL != "http://custom:5000" {
		t.Errorf("LMStudio.URL = %q, want %q", cfg.LMStudio.URL, "http://custom:5000")
	}
	if cfg.LMStudio.ModelDefault != "test-model" {
		t.Errorf("LMStudio.ModelDefault = %q, want %q", cfg.LMStudio.ModelDefault, "test-model")
	}
	if cfg.LMStudio.Timeout != 240*time.Second {
		t.Errorf("LMStudio.Timeout = %v, want %v", cfg.LMStudio.Timeout, 240*time.Second)
	}
	if cfg.LMStudio.MaxRetries != 5 {
		t.Errorf("LMStudio.MaxRetries = %d, want %d", cfg.LMStudio.MaxRetries, 5)
	}
	if cfg.Store.Path != "/custom/path/db.sqlite" {
		t.Errorf("Store.Path = %q, want %q", cfg.Store.Path, "/custom/path/db.sqlite")
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	tmpDir := t.TempDir()

	configContent := `
server:
  host: "127.0.0.1"
  port: 8420
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s

lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: 3

store:
  path: "./data/hygur.db"

logging:
  level: "info"
`
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Set environment variables
	t.Setenv("HYGUR_SERVER_PORT", "9999")
	t.Setenv("HYGUR_LM_STUDIO_URL", "http://env-override:8000")
	t.Setenv("HYGUR_LOGGING_LEVEL", "debug")

	cfg, err := LoadWithOptions(&LoadOptions{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("LoadWithOptions() returned error: %v", err)
	}

	// Verify env overrides
	if cfg.Server.Port != 9999 {
		t.Errorf("Server.Port = %d, want %d (env override)", cfg.Server.Port, 9999)
	}
	if cfg.LMStudio.URL != "http://env-override:8000" {
		t.Errorf("LMStudio.URL = %q, want %q (env override)", cfg.LMStudio.URL, "http://env-override:8000")
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q (env override)", cfg.Logging.Level, "debug")
	}

	// Verify non-overridden values from YAML
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want %q (from YAML)", cfg.Server.Host, "127.0.0.1")
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		wantErrPart string
	}{
		{
			name: "invalid port too high",
			config: `
server:
  host: "127.0.0.1"
  port: 70000
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: 3
store:
  path: "./data/hygur.db"
logging:
  level: "info"
`,
			wantErrPart: "server.port must be between 1 and 65535",
		},
		{
			name: "invalid port zero",
			config: `
server:
  host: "127.0.0.1"
  port: 0
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: 3
store:
  path: "./data/hygur.db"
logging:
  level: "info"
`,
			wantErrPart: "server.port must be between 1 and 65535",
		},
		{
			name: "empty host",
			config: `
server:
  host: ""
  port: 8420
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: 3
store:
  path: "./data/hygur.db"
logging:
  level: "info"
`,
			wantErrPart: "server.host is required",
		},
		{
			name: "empty lm_studio url",
			config: `
server:
  host: "127.0.0.1"
  port: 8420
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: ""
  timeout: 120s
  max_retries: 3
store:
  path: "./data/hygur.db"
logging:
  level: "info"
`,
			wantErrPart: "lm_studio.url is required",
		},
		{
			name: "invalid log level",
			config: `
server:
  host: "127.0.0.1"
  port: 8420
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: 3
store:
  path: "./data/hygur.db"
logging:
  level: "verbose"
`,
			wantErrPart: "logging.level must be one of",
		},
		{
			name: "negative max_retries",
			config: `
server:
  host: "127.0.0.1"
  port: 8420
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
lm_studio:
  url: "http://localhost:1234"
  timeout: 120s
  max_retries: -1
store:
  path: "./data/hygur.db"
logging:
  level: "info"
`,
			wantErrPart: "lm_studio.max_retries must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(tt.config), 0644); err != nil {
				t.Fatalf("failed to write config file: %v", err)
			}

			_, err := LoadWithOptions(&LoadOptions{ConfigPath: configPath})
			if err == nil {
				t.Fatal("LoadWithOptions() expected error, got nil")
			}

			if !containsString(err.Error(), tt.wantErrPart) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrPart)
			}
		})
	}
}

func TestLoad_InvalidYAMLFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Write invalid YAML
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := LoadWithOptions(&LoadOptions{ConfigPath: configPath})
	if err == nil {
		t.Fatal("LoadWithOptions() expected error for invalid YAML, got nil")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
