// Package config handles application configuration loading and validation.
package config

import (
	"time"
)

// Config holds the application configuration.
type Config struct {
	// Server contains HTTP server settings.
	Server ServerConfig `mapstructure:"server"`

	// LMStudio contains LM Studio connection settings.
	LMStudio LMStudioConfig `mapstructure:"lm_studio"`

	// Store contains database settings.
	Store StoreConfig `mapstructure:"store"`

	// Logging contains logging settings.
	Logging LoggingConfig `mapstructure:"logging"`

	// DataDir is the directory for storing application data (tokens, database, etc.).
	// Defaults to ~/.hygur if not set.
	DataDir string `mapstructure:"data_dir"`

	// Connectors contient les réglages persistés des connecteurs par ID.
	Connectors map[string]ConnectorSettings `mapstructure:"connectors" yaml:"connectors,omitempty"`

	// ConnectorInstances contient les instances dynamiques (multi-compte).
	// Chaque entry a un instanceID unique, un typeID (ex: "imap") et ses settings.
	ConnectorInstances []ConnectorInstanceConfig `mapstructure:"connector_instances" yaml:"connector_instances,omitempty"`

	// Retrieval tunes the search-time scoring pipeline.
	Retrieval RetrievalConfig `mapstructure:"retrieval" yaml:"retrieval,omitempty"`

	// DailyBrief configures the scheduled daily activity digest task.
	DailyBrief DailyBriefConfig `mapstructure:"daily_brief" yaml:"daily_brief,omitempty"`
}

// DailyBriefConfig configures the scheduled daily activity digest task.
type DailyBriefConfig struct {
	// Enabled is opt-in. When false, the task is registered but never runs.
	Enabled bool `mapstructure:"enabled" yaml:"enabled"`
	// HourLocal is the wall-clock hour the brief runs in the host's local
	// time zone, formatted "HH:MM". Default "08:00".
	HourLocal string `mapstructure:"hour_local" yaml:"hour_local,omitempty"`
	// MaxItems caps how many recent items are fed to the LLM. Default 80.
	MaxItems int `mapstructure:"max_items" yaml:"max_items,omitempty"`
	// LookbackHours sets the activity window for which freshly-indexed items
	// are considered. Default 168 (7 days) — the daily brief catches up on
	// yesterday's late-evening mail as well as today's morning batch.
	LookbackHours int `mapstructure:"lookback_hours" yaml:"lookback_hours,omitempty"`
	// MaxItemAgeDays drops items whose intrinsic date (canonical_date /
	// mail_date) is older than this many days, even if they were indexed
	// recently. Without this filter a backfill of 2024 emails would surface
	// in today's brief as if it were fresh. Default 180 (6 months); 0 to
	// disable the filter.
	MaxItemAgeDays int `mapstructure:"max_item_age_days" yaml:"max_item_age_days,omitempty"`
}

// RetrievalConfig tunes the search-time scoring pipeline.
type RetrievalConfig struct {
	// TemporalScoringMode selects how recency is blended with semantic similarity.
	// "additive"      → final = sem*(1-Wr) + recency*Wr  (Wr=0.5 with marker, 0.3 default)
	// "multiplicative" → legacy: final = sem * freshness_factor (60-day half-life, floor 0.4)
	// Default: "additive".
	TemporalScoringMode string `mapstructure:"temporal_scoring_mode" yaml:"temporal_scoring_mode,omitempty"`

	// CurrentStateFilterDays is the lookback window applied when the query is
	// detected as a "current state" question (amounts due, balances, etc.).
	// 0 disables the pre-filter. Default: 90.
	CurrentStateFilterDays int `mapstructure:"current_state_filter_days" yaml:"current_state_filter_days,omitempty"`

	// UseLLMIntent enables the LLM intent classifier (factual_entity / topic /
	// temporal / conversational). When true, the classifier runs in parallel
	// with embedding generation; on timeout/error the search falls back to the
	// vector-only path so latency hiccups never break retrieval. Default false.
	UseLLMIntent bool `mapstructure:"use_llm_intent" yaml:"use_llm_intent,omitempty"`

	// UseJudge enables the LLM relevance judge as a post-filter on every
	// branch. Drops results scoring below JudgeKeepThreshold; if all are
	// dropped the search abstains (empty result set). Default false because
	// the judge adds 1-3s per query.
	UseJudge bool `mapstructure:"use_judge" yaml:"use_judge,omitempty"`

	// EntitySearchFallback controls behavior when EntitySearch (factual_entity
	// path) returns no hit or only weak hits. true → fall back to vector.
	// false → return empty (explicit abstention). Default true.
	EntitySearchFallback bool `mapstructure:"entity_search_fallback" yaml:"entity_search_fallback,omitempty"`

	// EntitySearchMinScore is the minimum top-result score required to commit
	// to the EntitySearch result set. Below it, fallback or abstention applies.
	// Default 0.5.
	EntitySearchMinScore float64 `mapstructure:"entity_search_min_score" yaml:"entity_search_min_score,omitempty"`
}

// ConnectorInstanceConfig réglages persistés d'une instance dynamique de connecteur.
// Permet d'avoir plusieurs instances du même type (ex: deux comptes IMAP).
type ConnectorInstanceConfig struct {
	ID          string            `mapstructure:"id"           yaml:"id"`
	TypeName    string            `mapstructure:"type"         yaml:"type"`
	DisplayName string            `mapstructure:"display_name" yaml:"display_name,omitempty"`
	Enabled     bool              `mapstructure:"enabled"      yaml:"enabled"`
	Settings    map[string]string `mapstructure:"settings"     yaml:"settings,omitempty"`
	Schedule    string            `mapstructure:"schedule"     yaml:"schedule,omitempty"`
}

// ConnectorSettings réglages persistés d'un connecteur dans config.yaml.
// Les secrets ne sont JAMAIS ici — ils vivent dans auth.CredentialStore.
// Attention : ne pas appeler ConnectorConfig (clash avec plugin.ConnectorConfig).
type ConnectorSettings struct {
	Enabled  bool              `mapstructure:"enabled"            yaml:"enabled"`
	Settings map[string]string `mapstructure:"settings,omitempty" yaml:"settings,omitempty"`
	Schedule string            `mapstructure:"schedule,omitempty" yaml:"schedule,omitempty"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	// Host is the address to bind the server to.
	Host string `mapstructure:"host"`

	// Port is the port to listen on.
	Port int `mapstructure:"port"`

	// ReadTimeout is the maximum duration for reading the entire request.
	ReadTimeout time.Duration `mapstructure:"read_timeout"`

	// WriteTimeout is the maximum duration before timing out writes of the response.
	WriteTimeout time.Duration `mapstructure:"write_timeout"`

	// ShutdownTimeout is the maximum duration to wait for active connections to close.
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// LMStudioConfig holds OpenAI-compatible endpoint configuration.
//
// The sidecar accepts two independent endpoints — one for inference (chat
// completions, model listing) and one for embeddings — so users can point
// each at a different LM Studio, Ollama, llama.cpp, or vLLM instance on
// their local network. Provide only the scheme/host/port (e.g.
// "http://192.168.0.1:1234"); the sidecar appends the OpenAI-compatible
// path ("/v1/chat/completions", "/v1/embeddings", "/v1/models") itself.
//
// If EmbeddingURL is empty the inference URL is reused, preserving the
// single-endpoint setup used before this split.
type LMStudioConfig struct {
	// URL is the inference endpoint (chat completions and model listing).
	URL string `mapstructure:"url"`

	// EmbeddingURL is the embeddings endpoint. Optional — falls back to URL.
	EmbeddingURL string `mapstructure:"embedding_url"`

	// ModelDefault is the default chat model used when none is specified.
	ModelDefault string `mapstructure:"model_default"`

	// EmbeddingModel is the model used for generating embeddings.
	// Defaults to "text-embedding-nomic-embed-text-v1.5" if not set.
	EmbeddingModel string `mapstructure:"embedding_model"`

	// EmbeddingMaxTokens is the per-input token budget enforced before sending
	// requests to the embedding endpoint. Inputs that exceed it are truncated
	// so they never trigger a 500 "input too large" from servers whose
	// physical batch size is smaller than the chunker's output. Default 512.
	EmbeddingMaxTokens int `mapstructure:"embedding_max_tokens"`

	// Timeout is the maximum duration for inference (chat) API calls.
	Timeout time.Duration `mapstructure:"timeout"`

	// EmbeddingTimeout is the maximum duration per embedding batch request.
	// Embedding models on local hardware can take much longer than inference
	// for large batches, so this is intentionally a separate, larger value.
	// Defaults to 5 minutes when zero.
	EmbeddingTimeout time.Duration `mapstructure:"embedding_timeout"`

	// MaxRetries is the number of retry attempts for failed requests.
	MaxRetries int `mapstructure:"max_retries"`
}

// StoreConfig holds database configuration.
type StoreConfig struct {
	// Path is the file path to the SQLite database.
	Path string `mapstructure:"path"`
}

// LoggingConfig holds logging configuration.
type LoggingConfig struct {
	// Level is the minimum log level (debug, info, warn, error).
	Level string `mapstructure:"level"`
}
