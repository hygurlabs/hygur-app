package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Default configuration values.
const (
	DefaultHost            = "127.0.0.1"
	DefaultPort            = 8420
	DefaultReadTimeout     = 30 * time.Second
	DefaultWriteTimeout    = 180 * time.Second // 3 minutes for SSE streaming
	DefaultShutdownTimeout = 10 * time.Second
	DefaultLMStudioURL     = "http://localhost:1234"
	DefaultLMStudioTimeout = 120 * time.Second
	DefaultMaxRetries      = 3
	// DefaultEmbeddingMaxTokens matches the physical batch size of most
	// OpenAI-compatible embedding backends (LM Studio, llama.cpp) so a dense
	// 1000-char chunk that tokenises to 500-700 tokens is truncated rather
	// than rejected.
	DefaultEmbeddingMaxTokens = 512
	DefaultStorePath          = "./data/hygur.db"
	DefaultLogLevel           = "info"
)

// ErrInvalidConfig indicates that the configuration is invalid.
var ErrInvalidConfig = errors.New("invalid configuration")

// Load reads configuration from config.yaml and environment variables.
// Environment variables with prefix HYGUR_ override YAML values.
// For example, HYGUR_SERVER_PORT overrides server.port.
func Load() (*Config, error) {
	return LoadWithOptions(nil)
}

// LoadOptions provides options for loading configuration.
type LoadOptions struct {
	// ConfigPath specifies a custom path to the config file.
	// If empty, searches current directory and $HOME/.hygur.
	ConfigPath string
}

// LoadWithOptions reads configuration with custom options.
func LoadWithOptions(opts *LoadOptions) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Configure config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if opts != nil && opts.ConfigPath != "" {
		v.SetConfigFile(opts.ConfigPath)
	} else {
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.hygur")
	}

	// Configure environment variables
	v.SetEnvPrefix("HYGUR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (optional - env vars and defaults still work)
	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		switch {
		case errors.As(err, &configFileNotFoundError):
			// Search-path lookup found nothing — use defaults.
		case errors.Is(err, os.ErrNotExist):
			// Specific config path requested but file doesn't exist yet
			// (first-launch scenario when bundled app has no config.yaml).
			// Use defaults; the file will be created when connectors are saved.
		default:
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// setDefaults configures default values for all settings.
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.host", DefaultHost)
	v.SetDefault("server.port", DefaultPort)
	v.SetDefault("server.read_timeout", DefaultReadTimeout)
	v.SetDefault("server.write_timeout", DefaultWriteTimeout)
	v.SetDefault("server.shutdown_timeout", DefaultShutdownTimeout)

	// LM Studio defaults
	v.SetDefault("lm_studio.url", DefaultLMStudioURL)
	// Empty default so viper's AutomaticEnv binds HYGUR_LMSTUDIO_API_KEY (it only
	// env-binds keys it already knows). The key is a secret: when set via env it
	// is the operator path (server/cloud); otherwise it comes from the credential
	// store at startup. It is never persisted to config.yaml.
	v.SetDefault("lm_studio.api_key", "")
	v.SetDefault("lm_studio.embedding_url", "")
	v.SetDefault("lm_studio.model_default", "")
	v.SetDefault("lm_studio.embedding_max_tokens", DefaultEmbeddingMaxTokens)
	v.SetDefault("lm_studio.timeout", DefaultLMStudioTimeout)
	v.SetDefault("lm_studio.max_retries", DefaultMaxRetries)

	// Store defaults
	v.SetDefault("store.path", DefaultStorePath)

	// Logging defaults
	v.SetDefault("logging.level", DefaultLogLevel)

	// Retrieval defaults
	v.SetDefault("retrieval.temporal_scoring_mode", "additive")
	v.SetDefault("retrieval.current_state_filter_days", 90)
	v.SetDefault("retrieval.use_llm_intent", false)
	v.SetDefault("retrieval.use_judge", false)
	v.SetDefault("retrieval.entity_search_fallback", true)
	v.SetDefault("retrieval.entity_search_min_score", 0.5)

	// DailyBrief defaults — opt-in. 48 h window so the brief catches the last
	// two days of activity rather than collapsing on a quiet 24 h.
	v.SetDefault("daily_brief.enabled", false)
	v.SetDefault("daily_brief.hour_local", "08:00")
	v.SetDefault("daily_brief.max_items", 50)
	v.SetDefault("daily_brief.lookback_hours", 48)

	// Mail: only notify on mail received within the last 14 days.
	v.SetDefault("mail.notify_recency_days", 14)
}

// SaveConnectorsConfig persiste la map des ConnectorSettings dans config.yaml
// sans écraser les autres sections. Écriture atomique via os.Rename.
func SaveConnectorsConfig(configPath string, connectors map[string]ConnectorSettings) error {
	// 1. Lire le YAML existant dans une map générique pour préserver toutes les sections
	raw := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("config file appears corrupt, refusing to overwrite: %w", err)
		}
	}

	// 2. Mettre à jour uniquement la section "connectors"
	raw["connectors"] = connectors

	// 3. Écriture atomique via fichier temporaire + rename
	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}
	return os.Rename(tmp, configPath)
}

// SaveConnectorInstancesConfig persiste la liste des ConnectorInstanceConfig dans
// config.yaml sans écraser les autres sections. Écriture atomique via os.Rename.
func SaveConnectorInstancesConfig(configPath string, instances []ConnectorInstanceConfig) error {
	raw := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("config file appears corrupt, refusing to overwrite: %w", err)
		}
	}

	raw["connector_instances"] = instances

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}
	return os.Rename(tmp, configPath)
}

// validate checks that required configuration values are present and valid.
func validate(cfg *Config) error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("%w: server.port must be between 1 and 65535, got %d", ErrInvalidConfig, cfg.Server.Port)
	}

	if cfg.Server.Host == "" {
		return fmt.Errorf("%w: server.host is required", ErrInvalidConfig)
	}

	if cfg.LMStudio.URL == "" {
		return fmt.Errorf("%w: lm_studio.url is required", ErrInvalidConfig)
	}

	if cfg.LMStudio.EmbeddingURL != "" {
		if !strings.HasPrefix(cfg.LMStudio.EmbeddingURL, "http://") &&
			!strings.HasPrefix(cfg.LMStudio.EmbeddingURL, "https://") {
			return fmt.Errorf("%w: lm_studio.embedding_url must start with http:// or https://", ErrInvalidConfig)
		}
	}

	if cfg.Store.Path == "" {
		return fmt.Errorf("%w: store.path is required", ErrInvalidConfig)
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[cfg.Logging.Level] {
		return fmt.Errorf("%w: logging.level must be one of debug, info, warn, error; got %q", ErrInvalidConfig, cfg.Logging.Level)
	}

	if cfg.Server.ReadTimeout <= 0 {
		return fmt.Errorf("%w: server.read_timeout must be positive", ErrInvalidConfig)
	}

	if cfg.Server.WriteTimeout <= 0 {
		return fmt.Errorf("%w: server.write_timeout must be positive", ErrInvalidConfig)
	}

	if cfg.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("%w: server.shutdown_timeout must be positive", ErrInvalidConfig)
	}

	if cfg.LMStudio.Timeout <= 0 {
		return fmt.Errorf("%w: lm_studio.timeout must be positive", ErrInvalidConfig)
	}

	if cfg.LMStudio.MaxRetries < 0 {
		return fmt.Errorf("%w: lm_studio.max_retries must be non-negative", ErrInvalidConfig)
	}

	return nil
}
