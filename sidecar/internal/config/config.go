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

	// Mail configures mail-sync behaviour.
	Mail MailConfig `mapstructure:"mail" yaml:"mail,omitempty"`

	// Auth configures request authentication (local single-token vs remote
	// per-device JWT). Defaults to "local" so the bundled/embedded mode is
	// unchanged.
	Auth AuthConfig `mapstructure:"auth" yaml:"auth,omitempty"`

	// Prose configures the deterministic prose-cleanup pass applied to
	// user-facing outputs (mail drafts, chronicle, positions, follow-up report).
	Prose ProseConfig `mapstructure:"prose" yaml:"prose,omitempty"`
}

// ProseConfig configures the deterministic prose-cleanup pass (internal/prose).
type ProseConfig struct {
	// Tidy enables the cleanup (preamble stripping + French typography). Default
	// true; conservative (never rewrites meaning). Kill-switch: HYGUR_PROSE_TIDY=false.
	Tidy bool `mapstructure:"tidy" yaml:"tidy,omitempty"`
}

// AuthConfig configures how the server authenticates requests.
//
//   - mode "local"  (default): a single static token in X-Hygur-Token. This is
//     the loopback trust model used by the embedded/desktop mode.
//   - mode "remote": per-device EdDSA JWTs, verified against PublicKey, with
//     expiry and a jti revocation list checked locally. Used when the server is
//     exposed beyond loopback (self-host / Hygur Cloud).
type AuthConfig struct {
	Mode string `mapstructure:"mode" yaml:"mode,omitempty"`
	// PublicKey is the PEM-encoded Ed25519 public key verifying device tokens.
	// Required when mode == "remote".
	PublicKey string `mapstructure:"public_key" yaml:"public_key,omitempty"`
	// PrivateKey is the PEM-encoded Ed25519 private key used ONLY by the
	// `issue-token` CLI for self-hosted issuance. Never needed to serve.
	PrivateKey string `mapstructure:"private_key" yaml:"private_key,omitempty"`
	// RevokedJTIs lists token ids to reject even when otherwise valid.
	RevokedJTIs []string `mapstructure:"revoked_jtis" yaml:"revoked_jtis,omitempty"`
}

// MailConfig configures mail-sync behaviour.
type MailConfig struct {
	// ReconcileDeletions, when true, removes from the knowledge base any
	// previously-indexed mail of an account that is no longer present after a
	// FULL (unbounded) sync sweep — i.e. messages deleted/spammed on the server
	// stop polluting retrieval. Opt-in (default false) because a misconfigured
	// partial sync could otherwise purge valid items; the reconcile only runs
	// when the sweep had no thread limit.
	ReconcileDeletions bool `mapstructure:"reconcile_deletions" yaml:"reconcile_deletions"`

	// NotifyRecencyDays bounds "important mail" notifications to recently
	// RECEIVED mail: a mail whose actual date (mail_date) is older than this
	// many days never triggers a notification, even when freshly indexed (e.g.
	// during a backfill). Default 14; set <= 0 to disable the recency gate.
	NotifyRecencyDays int `mapstructure:"notify_recency_days" yaml:"notify_recency_days,omitempty"`
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

	// AuthorityRerank enables the M2 authority re-score (boost confirmed+current,
	// demote superseded, surface conflicts). Default true; a no-op until decision/
	// conflict records exist. Kill-switch: HYGUR_RETRIEVAL_AUTHORITY_RERANK=false.
	AuthorityRerank bool `mapstructure:"authority_rerank" yaml:"authority_rerank,omitempty"`

	// AttentionRerank enables the P-2 attention re-score (small boost for often/
	// recently-cited items, from the item_access bus). Default true; a no-op until
	// the bus has data. Kill-switch: HYGUR_RETRIEVAL_ATTENTION_RERANK=false.
	AttentionRerank bool `mapstructure:"attention_rerank" yaml:"attention_rerank,omitempty"`

	// ImminenceRerank enables the P-2 imminence re-score (small boost for items tied
	// to a soon-due recurring obligation). Default true; a no-op until an imminent-ids
	// provider is wired and returns a non-empty set. Kill-switch: HYGUR_RETRIEVAL_IMMINENCE_RERANK=false.
	ImminenceRerank bool `mapstructure:"imminence_rerank" yaml:"imminence_rerank,omitempty"`

	// EntityIndex enables the brick-1 associative entity lens: EntitySearch also
	// folds in items whose cached claims mention the queried entity (claim-grounded
	// recall + precision) on top of the surface match. Default true; a no-op until
	// the entity_mentions index is populated. Kill-switch: HYGUR_RETRIEVAL_ENTITY_INDEX=false.
	EntityIndex bool `mapstructure:"entity_index" yaml:"entity_index,omitempty"`

	// EntitySynonymy enables the brick-2 synonymy expansion: the queried entity is
	// embedded and matched by cosine against entity_vectors so surface-different
	// mentions (e.g. an anglicism and its French equivalent) resolve to one lookup
	// set. Requires EntityIndex. Default true; a no-op until entity_vectors is
	// populated. Kill-switch: HYGUR_RETRIEVAL_ENTITY_SYNONYMY=false.
	EntitySynonymy bool `mapstructure:"entity_synonymy" yaml:"entity_synonymy,omitempty"`

	// EntitySynonymyThreshold is the minimum cosine similarity for a stored entity
	// to count as a synonym of the queried one. Default 0.60 — calibrated on the
	// home corpus with nomic-embed-text-v2: bare entity-string embeddings need a
	// lower bar than passage embeddings (0.80 was inert, self-match only; 0.60
	// resolves real surface variants — "Tesla"/"Tesla Belgium BV" — with no
	// observed cross-entity false merges). Re-calibrate if the embedding model changes.
	EntitySynonymyThreshold float64 `mapstructure:"entity_synonymy_threshold" yaml:"entity_synonymy_threshold,omitempty"`
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

	// APIKey is the bearer token sent as `Authorization: Bearer <key>` on every
	// OpenAI-compatible request (chat, models, embeddings). Hosted providers
	// (Mistral, OpenAI, Together…) require it; local runtimes (LM Studio, Ollama,
	// vLLM, llama.cpp) leave it empty. It is a SECRET: populated at startup from
	// the HYGUR_LMSTUDIO_API_KEY env var or the encrypted credential store, and
	// NEVER written to config.yaml (yaml:"-") — secrets live in the
	// CredentialStore, mirroring the connector-credential rule above.
	APIKey string `mapstructure:"api_key" yaml:"-"`

	// EmbeddingURL is the embeddings endpoint. Optional — falls back to URL.
	EmbeddingURL string `mapstructure:"embedding_url"`

	// ModelDefault is the default chat model used when none is specified.
	ModelDefault string `mapstructure:"model_default"`

	// IndexingURL is an optional separate chat endpoint for the lightweight
	// ingestion/extraction model (Tier 2 NER). Falls back to URL when empty.
	IndexingURL string `mapstructure:"indexing_url"`

	// ModelIndexing is the chat model used for ingestion-time extraction
	// (Tier 2 NER). A small fast model (e.g. minicpm5: ~1000 tok/s on many
	// connections vs the big model's ~50 tok/s on a few) trades some accuracy
	// for throughput on bulk indexing. Falls back to ModelDefault when empty.
	ModelIndexing string `mapstructure:"model_indexing"`

	// EmbeddingModel is the model used for generating embeddings.
	// Defaults to "text-embedding-nomic-embed-text-v1.5" if not set.
	EmbeddingModel string `mapstructure:"embedding_model"`

	// VisionURL is an optional OpenAI-compatible endpoint serving a multimodal
	// (vision) model — used to OCR scanned PDFs and images without a system
	// Tesseract install (portable: just another local model over HTTP). Falls
	// back to URL when empty.
	VisionURL string `mapstructure:"vision_url"`

	// VisionModel is the model id sent to VisionURL (e.g. "nemotron-omni").
	// Falls back to ModelDefault when empty.
	VisionModel string `mapstructure:"vision_model"`

	// NoChatTemplateKwargs omits chat_template_kwargs (e.g. {"enable_thinking":
	// false}) from chat requests. Set true for hosted backends that reject unknown
	// request fields — e.g. Gemma on Infomaniak, OpenAI, Mistral. Leave false for
	// vLLM/SGLang (Qwen3, Nemotron) which NEED enable_thinking:false to suppress
	// reasoning. Env: HYGUR_LM_STUDIO_NO_CHAT_TEMPLATE_KWARGS.
	NoChatTemplateKwargs bool `mapstructure:"no_chat_template_kwargs"`

	// MaxCompletionTokens makes chat requests carry `max_completion_tokens` instead
	// of the legacy `max_tokens` — required by backends (Infomaniak, newer OpenAI)
	// whose schema only accepts the former. Default false (vLLM/LM Studio/Sparky).
	MaxCompletionTokens bool `mapstructure:"max_completion_tokens"`
	// ReasoningEffort, when non-empty ("none"|"low"|"medium"|"high"), is sent as the
	// OpenAI `reasoning_effort` on chat requests — "none" disables thinking on a
	// reasoning model (the Infomaniak way, replacing chat_template_kwargs). Leave
	// empty for models that reject it (e.g. gemma → 400). Per-endpoint.
	ReasoningEffort string `mapstructure:"reasoning_effort"`
	// IndexingReasoningEffort is ReasoningEffort for the separate indexing model
	// (e.g. "none" for a reasoning-capable Tier-2 model like Nemotron-Nano).
	IndexingReasoningEffort string `mapstructure:"indexing_reasoning_effort"`
	// RerankURL + RerankModel enable a dedicated reranker (Cohere-shaped /rerank,
	// e.g. Infomaniak serving bge-reranker-v2-m3). When both set, retrieval reranks
	// there instead of via the chat LLM. Empty = LLM reranking (Sparky/local).
	RerankURL   string `mapstructure:"rerank_url"`
	RerankModel string `mapstructure:"rerank_model"`

	// EmbeddingMaxTokens is the per-input token budget enforced before sending
	// requests to the embedding endpoint. Inputs that exceed it are truncated
	// so they never trigger a 500 "input too large" from servers whose
	// physical batch size is smaller than the chunker's output. Default 512.
	EmbeddingMaxTokens int `mapstructure:"embedding_max_tokens"`

	// EmbeddingBatchSize is how many texts are sent per /v1/embeddings request
	// during bulk indexing. Larger = fewer round-trips (big win when the server
	// batches on GPU). Default 32; capped at 128. Lower it if your embedding
	// server rejects large batches.
	EmbeddingBatchSize int `mapstructure:"embedding_batch_size"`

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
