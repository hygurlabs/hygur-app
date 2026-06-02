package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// ConfigHandler exposes GET /config and PATCH /config so the macOS app can
// read and update the tunable subset of config.yaml (local LLM endpoints,
// retrieval options, brief schedule, log level) without touching the file
// directly. Connector sections are preserved untouched on every write.
type ConfigHandler struct {
	cfg        *config.Config
	configPath string
	mu         sync.RWMutex
	onChange   func(*config.Config)
	logger     zerolog.Logger
}

func NewConfigHandler(cfg *config.Config, configPath string, logger zerolog.Logger) *ConfigHandler {
	return &ConfigHandler{cfg: cfg, configPath: configPath, logger: logger}
}

// SetOnChange registers a callback fired after a successful PATCH so runtime
// components (e.g. the mail connector) can pick up changes without a restart.
func (h *ConfigHandler) SetOnChange(fn func(*config.Config)) { h.onChange = fn }

// MARK: - Response / request shapes

type ConfigResponse struct {
	LMStudio   LMStudioCfgResp   `json:"lm_studio"`
	Logging    LoggingCfgResp    `json:"logging"`
	DailyBrief DailyBriefCfgResp `json:"daily_brief"`
	Retrieval  RetrievalCfgResp  `json:"retrieval"`
	Mail       MailCfgResp       `json:"mail"`
}

type MailCfgResp struct {
	ReconcileDeletions bool `json:"reconcile_deletions"`
}

type LMStudioCfgResp struct {
	URL                string `json:"url"`
	EmbeddingURL       string `json:"embedding_url"`
	IndexingURL        string `json:"indexing_url"`
	ModelDefault       string `json:"model_default"`
	ModelIndexing      string `json:"model_indexing"`
	EmbeddingModel     string `json:"embedding_model"`
	EmbeddingMaxTokens int    `json:"embedding_max_tokens"`
	EmbeddingBatchSize int    `json:"embedding_batch_size"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	MaxRetries         int    `json:"max_retries"`
}

type LoggingCfgResp struct {
	Level string `json:"level"`
}

type DailyBriefCfgResp struct {
	Enabled       bool   `json:"enabled"`
	HourLocal     string `json:"hour_local"`
	MaxItems      int    `json:"max_items"`
	LookbackHours int    `json:"lookback_hours"`
}

type RetrievalCfgResp struct {
	UseLLMIntent         bool    `json:"use_llm_intent"`
	UseJudge             bool    `json:"use_judge"`
	TemporalScoringMode  string  `json:"temporal_scoring_mode"`
	EntitySearchFallback bool    `json:"entity_search_fallback"`
	EntitySearchMinScore float64 `json:"entity_search_min_score"`
}

// PatchConfigRequest — all fields optional; only provided fields are applied.
type PatchConfigRequest struct {
	LMStudio   *PatchLMStudio   `json:"lm_studio,omitempty"`
	Logging    *PatchLogging    `json:"logging,omitempty"`
	DailyBrief *PatchDailyBrief `json:"daily_brief,omitempty"`
	Retrieval  *PatchRetrieval  `json:"retrieval,omitempty"`
	Mail       *PatchMail       `json:"mail,omitempty"`
}

type PatchMail struct {
	ReconcileDeletions *bool `json:"reconcile_deletions,omitempty"`
}

type PatchLMStudio struct {
	URL                *string `json:"url,omitempty"`
	EmbeddingURL       *string `json:"embedding_url,omitempty"`
	IndexingURL        *string `json:"indexing_url,omitempty"`
	ModelDefault       *string `json:"model_default,omitempty"`
	ModelIndexing      *string `json:"model_indexing,omitempty"`
	EmbeddingModel     *string `json:"embedding_model,omitempty"`
	EmbeddingMaxTokens *int    `json:"embedding_max_tokens,omitempty"`
	EmbeddingBatchSize *int    `json:"embedding_batch_size,omitempty"`
	TimeoutSeconds     *int    `json:"timeout_seconds,omitempty"`
	MaxRetries         *int    `json:"max_retries,omitempty"`
}

type PatchLogging struct {
	Level *string `json:"level,omitempty"`
}

type PatchDailyBrief struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	HourLocal     *string `json:"hour_local,omitempty"`
	MaxItems      *int    `json:"max_items,omitempty"`
	LookbackHours *int    `json:"lookback_hours,omitempty"`
}

type PatchRetrieval struct {
	UseLLMIntent         *bool    `json:"use_llm_intent,omitempty"`
	UseJudge             *bool    `json:"use_judge,omitempty"`
	TemporalScoringMode  *string  `json:"temporal_scoring_mode,omitempty"`
	EntitySearchFallback *bool    `json:"entity_search_fallback,omitempty"`
	EntitySearchMinScore *float64 `json:"entity_search_min_score,omitempty"`
}

// MARK: - Handlers

// GetConfig returns the tunable configuration subset as JSON.
// GET /config
func (h *ConfigHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	resp := ConfigResponse{
		LMStudio: LMStudioCfgResp{
			URL:                h.cfg.LMStudio.URL,
			EmbeddingURL:       h.cfg.LMStudio.EmbeddingURL,
			IndexingURL:        h.cfg.LMStudio.IndexingURL,
			ModelDefault:       h.cfg.LMStudio.ModelDefault,
			ModelIndexing:      h.cfg.LMStudio.ModelIndexing,
			EmbeddingModel:     h.cfg.LMStudio.EmbeddingModel,
			EmbeddingMaxTokens: h.cfg.LMStudio.EmbeddingMaxTokens,
			EmbeddingBatchSize: h.cfg.LMStudio.EmbeddingBatchSize,
			TimeoutSeconds:     int(h.cfg.LMStudio.Timeout.Seconds()),
			MaxRetries:         h.cfg.LMStudio.MaxRetries,
		},
		Logging: LoggingCfgResp{
			Level: h.cfg.Logging.Level,
		},
		DailyBrief: DailyBriefCfgResp{
			Enabled:       h.cfg.DailyBrief.Enabled,
			HourLocal:     h.cfg.DailyBrief.HourLocal,
			MaxItems:      h.cfg.DailyBrief.MaxItems,
			LookbackHours: h.cfg.DailyBrief.LookbackHours,
		},
		Retrieval: RetrievalCfgResp{
			UseLLMIntent:         h.cfg.Retrieval.UseLLMIntent,
			UseJudge:             h.cfg.Retrieval.UseJudge,
			TemporalScoringMode:  h.cfg.Retrieval.TemporalScoringMode,
			EntitySearchFallback: h.cfg.Retrieval.EntitySearchFallback,
			EntitySearchMinScore: h.cfg.Retrieval.EntitySearchMinScore,
		},
		Mail: MailCfgResp{
			ReconcileDeletions: h.cfg.Mail.ReconcileDeletions,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// PatchConfig applies a partial update to the config and persists it.
// PATCH /config
func (h *ConfigHandler) PatchConfig(w http.ResponseWriter, r *http.Request) {
	var body PatchConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Apply patches to in-memory config.
	if lm := body.LMStudio; lm != nil {
		if lm.URL != nil {
			h.cfg.LMStudio.URL = *lm.URL
		}
		if lm.EmbeddingURL != nil {
			h.cfg.LMStudio.EmbeddingURL = *lm.EmbeddingURL
		}
		if lm.IndexingURL != nil {
			h.cfg.LMStudio.IndexingURL = *lm.IndexingURL
		}
		if lm.ModelDefault != nil {
			h.cfg.LMStudio.ModelDefault = *lm.ModelDefault
		}
		if lm.ModelIndexing != nil {
			h.cfg.LMStudio.ModelIndexing = *lm.ModelIndexing
		}
		if lm.EmbeddingModel != nil {
			h.cfg.LMStudio.EmbeddingModel = *lm.EmbeddingModel
		}
		if lm.EmbeddingMaxTokens != nil {
			h.cfg.LMStudio.EmbeddingMaxTokens = *lm.EmbeddingMaxTokens
		}
		if lm.EmbeddingBatchSize != nil {
			h.cfg.LMStudio.EmbeddingBatchSize = *lm.EmbeddingBatchSize
		}
		if lm.TimeoutSeconds != nil {
			h.cfg.LMStudio.Timeout = time.Duration(*lm.TimeoutSeconds) * time.Second
		}
		if lm.MaxRetries != nil {
			h.cfg.LMStudio.MaxRetries = *lm.MaxRetries
		}
	}
	if lo := body.Logging; lo != nil {
		if lo.Level != nil {
			h.cfg.Logging.Level = *lo.Level
		}
	}
	if db := body.DailyBrief; db != nil {
		if db.Enabled != nil {
			h.cfg.DailyBrief.Enabled = *db.Enabled
		}
		if db.HourLocal != nil {
			h.cfg.DailyBrief.HourLocal = *db.HourLocal
		}
		if db.MaxItems != nil {
			h.cfg.DailyBrief.MaxItems = *db.MaxItems
		}
		if db.LookbackHours != nil {
			h.cfg.DailyBrief.LookbackHours = *db.LookbackHours
		}
	}
	if rt := body.Retrieval; rt != nil {
		if rt.UseLLMIntent != nil {
			h.cfg.Retrieval.UseLLMIntent = *rt.UseLLMIntent
		}
		if rt.UseJudge != nil {
			h.cfg.Retrieval.UseJudge = *rt.UseJudge
		}
		if rt.TemporalScoringMode != nil {
			h.cfg.Retrieval.TemporalScoringMode = *rt.TemporalScoringMode
		}
		if rt.EntitySearchFallback != nil {
			h.cfg.Retrieval.EntitySearchFallback = *rt.EntitySearchFallback
		}
		if rt.EntitySearchMinScore != nil {
			h.cfg.Retrieval.EntitySearchMinScore = *rt.EntitySearchMinScore
		}
	}

	if m := body.Mail; m != nil {
		if m.ReconcileDeletions != nil {
			h.cfg.Mail.ReconcileDeletions = *m.ReconcileDeletions
		}
	}

	if err := h.saveConfig(); err != nil {
		h.logger.Error().Err(err).Msg("config save failed")
		http.Error(w, `{"error":"failed to persist config"}`, http.StatusInternalServerError)
		return
	}

	// Notify runtime listeners (e.g. the mail connector picks up
	// reconcile_deletions without a restart).
	if h.onChange != nil {
		h.onChange(h.cfg)
	}

	h.logger.Info().Msg("config updated and persisted")
	w.WriteHeader(http.StatusNoContent)

	// Most settings (LLM model/endpoints, schedules, batch sizes) are captured
	// by components at startup, so a save only fully takes effect after a
	// restart — which is otherwise an invisible footgun (e.g. a stale model id
	// → silent 404s). Restart gracefully so the new config always applies: send
	// ourselves SIGTERM (→ the existing graceful-shutdown path), and the macOS
	// app respawns the sidecar (~1s) with the fresh config. The short delay lets
	// this 204 flush first. Opt out with HYGUR_NO_AUTORESTART for headless runs
	// where nothing respawns the process.
	if os.Getenv("HYGUR_NO_AUTORESTART") != "" {
		return
	}
	go func() {
		time.Sleep(750 * time.Millisecond)
		h.logger.Info().Msg("restarting sidecar to apply new configuration")
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}()
}

// saveConfig writes the current in-memory config back to disk atomically,
// preserving connector and connector_instances sections unchanged.
func (h *ConfigHandler) saveConfig() error {
	raw := make(map[string]any)
	if data, err := os.ReadFile(h.configPath); err == nil {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("config corrupt, refusing overwrite: %w", err)
		}
	}

	raw["lm_studio"] = map[string]any{
		"url":                  h.cfg.LMStudio.URL,
		"embedding_url":        h.cfg.LMStudio.EmbeddingURL,
		"indexing_url":         h.cfg.LMStudio.IndexingURL,
		"model_default":        h.cfg.LMStudio.ModelDefault,
		"model_indexing":       h.cfg.LMStudio.ModelIndexing,
		"embedding_model":      h.cfg.LMStudio.EmbeddingModel,
		"embedding_max_tokens": h.cfg.LMStudio.EmbeddingMaxTokens,
		"embedding_batch_size": h.cfg.LMStudio.EmbeddingBatchSize,
		"timeout":              h.cfg.LMStudio.Timeout.String(),
		"max_retries":          h.cfg.LMStudio.MaxRetries,
	}
	raw["logging"] = map[string]any{
		"level": h.cfg.Logging.Level,
	}
	raw["daily_brief"] = map[string]any{
		"enabled":        h.cfg.DailyBrief.Enabled,
		"hour_local":     h.cfg.DailyBrief.HourLocal,
		"max_items":      h.cfg.DailyBrief.MaxItems,
		"lookback_hours": h.cfg.DailyBrief.LookbackHours,
	}
	raw["retrieval"] = map[string]any{
		"temporal_scoring_mode":    h.cfg.Retrieval.TemporalScoringMode,
		"current_state_filter_days": h.cfg.Retrieval.CurrentStateFilterDays,
		"use_llm_intent":           h.cfg.Retrieval.UseLLMIntent,
		"use_judge":                h.cfg.Retrieval.UseJudge,
		"entity_search_fallback":   h.cfg.Retrieval.EntitySearchFallback,
		"entity_search_min_score":  h.cfg.Retrieval.EntitySearchMinScore,
	}
	raw["mail"] = map[string]any{
		"reconcile_deletions": h.cfg.Mail.ReconcileDeletions,
	}

	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := h.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, h.configPath)
}
