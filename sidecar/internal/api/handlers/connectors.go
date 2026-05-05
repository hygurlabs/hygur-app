package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/events"
	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/rs/zerolog"
)

// ConnectorHandler handles connector-related API endpoints.
type ConnectorHandler struct {
	manager    *plugin.Manager
	credStore  *auth.CredentialStore
	configPath string
	logger     zerolog.Logger
	broker     *events.Broker
}

// NewConnectorHandler creates a new ConnectorHandler.
func NewConnectorHandler(manager *plugin.Manager, credStore *auth.CredentialStore, configPath string, logger zerolog.Logger) *ConnectorHandler {
	return &ConnectorHandler{
		manager:    manager,
		credStore:  credStore,
		configPath: configPath,
		logger:     logger.With().Str("handler", "connectors").Logger(),
	}
}

// SetBroker sets the event broker for broadcasting connector events.
func (h *ConnectorHandler) SetBroker(broker *events.Broker) {
	h.broker = broker
}

// ConnectorSummary is returned by List for each registered connector.
type ConnectorSummary struct {
	Info    plugin.ConnectorInfo `json:"info"`
	Enabled bool                 `json:"enabled"`
	Health  plugin.HealthStatus  `json:"health"`
}

// ConnectorDetail is returned by Get for a single connector.
type ConnectorDetail struct {
	Info         plugin.ConnectorInfo   `json:"info"`
	Capabilities plugin.Capabilities    `json:"capabilities"`
	ConfigSchema plugin.ConfigSchema    `json:"config_schema"`
	Config       plugin.ConnectorConfig `json:"config"`
	Health       plugin.HealthStatus    `json:"health"`
}

// List handles GET /connectors.
func (h *ConnectorHandler) List(w http.ResponseWriter, r *http.Request) {
	infos := h.manager.ListInfos()
	health := h.manager.AllHealth()

	summaries := make([]ConnectorSummary, 0, len(infos))
	for _, info := range infos {
		cfg, _ := h.manager.GetConfig(info.ID)
		summaries = append(summaries, ConnectorSummary{
			Info:    info,
			Enabled: cfg.Enabled,
			Health:  health[info.ID],
		})
	}

	writeConnectorJSON(w, http.StatusOK, summaries)
}

// Get handles GET /connectors/{id}.
func (h *ConnectorHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	c, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", "connector not found")
		return
	}

	cfg, _ := h.manager.GetConfig(id)

	detail := ConnectorDetail{
		Info:         c.Info(),
		Capabilities: c.Capabilities(),
		ConfigSchema: c.ConfigSchema(),
		Config:       cfg,
		Health:       c.Health(),
	}
	writeConnectorJSON(w, http.StatusOK, detail)
}

// Configure handles PUT /connectors/{id}/config.
func (h *ConnectorHandler) Configure(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	var cfg plugin.ConnectorConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeConnectorError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	if err := h.manager.Configure(id, cfg); err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("configure failed")
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	if err := h.persistAllConfigs(); err != nil {
		h.logger.Warn().Err(err).Msg("failed to persist connector configs")
	}

	if cfg.Enabled {
		if err := h.manager.ReinitConnector(id); err != nil {
			h.logger.Warn().Err(err).Str("connector", id).Msg("reinit after configure failed")
		}
	}

	writeConnectorJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Enable handles POST /connectors/{id}/enable.
func (h *ConnectorHandler) Enable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	if err := h.manager.EnableConnector(id); err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("enable failed")
		writeConnectorError(w, http.StatusUnprocessableEntity, "ENABLE_FAILED", err.Error())
		return
	}

	if err := h.persistAllConfigs(); err != nil {
		h.logger.Warn().Err(err).Msg("failed to persist connector configs")
	}

	writeConnectorJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
}

// Disable handles POST /connectors/{id}/disable.
func (h *ConnectorHandler) Disable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	if err := h.manager.DisableConnector(id); err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("disable failed")
		writeConnectorError(w, http.StatusUnprocessableEntity, "DISABLE_FAILED", err.Error())
		return
	}

	if err := h.persistAllConfigs(); err != nil {
		h.logger.Warn().Err(err).Msg("failed to persist connector configs")
	}

	writeConnectorJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// Sync handles POST /connectors/{id}/sync.
//
// Modes:
//   - Synchronous (default): runs the sync inline and returns SyncResult.
//   - Async (?async=true): kicks off the sync in a goroutine, returns
//     202 Accepted with {job_id}, and publishes progress on the event broker.
//     The job_id is the connector id appended with a UTC nanosecond timestamp
//     so multiple concurrent invocations can be tracked independently.
func (h *ConnectorHandler) Sync(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	var opts plugin.SyncOptions
	// Body is optional; ignore decode errors (empty body is valid).
	_ = json.NewDecoder(r.Body).Decode(&opts)

	if r.URL.Query().Get("async") == "true" {
		h.runAsyncSync(id, opts)
		writeConnectorJSON(w, http.StatusAccepted, map[string]string{
			"status":  "accepted",
			"job_id":  fmt.Sprintf("%s-%d", id, time.Now().UTC().UnixNano()),
			"message": "sync started in background; subscribe to events for progress",
		})
		return
	}

	// Broadcast "running" event
	if h.broker != nil {
		h.broker.PublishWithType(events.EventTypeSync, events.StatusRunning, id, "sync started", nil)
	}

	result, err := h.manager.TriggerSync(r.Context(), id, opts)
	if err != nil {
		if errors.Is(err, plugin.ErrSyncInProgress) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "SYNC_IN_PROGRESS"},
			})
			return
		}
		// Handle timeout errors
		if isTimeoutError(err) {
			h.logger.Error().Err(err).Str("connector", id).Msg("sync timed out")
			if h.broker != nil {
				h.broker.PublishWithType(events.EventTypeSync, events.StatusFailed, id, "sync timed out", nil)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "GATEWAY_TIMEOUT", "message": "sync timed out"},
			})
			return
		}
		h.logger.Error().Err(err).Str("connector", id).Msg("sync failed")
		if h.broker != nil {
			h.broker.PublishWithType(events.EventTypeSync, events.StatusFailed, id, "sync failed", map[string]any{"error": err.Error()})
		}
		writeConnectorError(w, http.StatusInternalServerError, "SYNC_FAILED", err.Error())
		return
	}

	// Broadcast "completed" event
	if h.broker != nil {
		h.broker.PublishWithType(events.EventTypeSync, events.StatusCompleted, id, "sync completed", map[string]any{
			"processed": result.Processed,
			"skipped":   result.Skipped,
			"failed":    result.Failed,
		})
	}

	writeConnectorJSON(w, http.StatusOK, result)
}

// runAsyncSync executes a sync in a detached goroutine and publishes
// progress events to the broker. The detached context uses context.Background
// so the sync survives the originating HTTP request's lifecycle.
func (h *ConnectorHandler) runAsyncSync(id string, opts plugin.SyncOptions) {
	go func() {
		// Use Background — the HTTP request context returned 202 already.
		// A reasonable upper bound prevents runaway syncs; 10 minutes lines
		// up with the existing TriggerSync timeout used by the manager.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if h.broker != nil {
			h.broker.PublishWithType(events.EventTypeSync, events.StatusRunning, id, "sync started", map[string]any{
				"async":      true,
				"account_id": opts.AccountID,
			})
		}
		result, err := h.manager.TriggerSync(ctx, id, opts)
		if err != nil {
			h.logger.Error().Err(err).Str("connector", id).Msg("async sync failed")
			if h.broker != nil {
				h.broker.PublishWithType(events.EventTypeSync, events.StatusFailed, id, "sync failed", map[string]any{
					"error":      err.Error(),
					"account_id": opts.AccountID,
				})
			}
			return
		}
		if h.broker != nil {
			h.broker.PublishWithType(events.EventTypeSync, events.StatusCompleted, id, "sync completed", map[string]any{
				"processed":  result.Processed,
				"skipped":    result.Skipped,
				"failed":     result.Failed,
				"account_id": opts.AccountID,
			})
		}
	}()
}

// Health handles GET /connectors/{id}/health.
func (h *ConnectorHandler) Health(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	c, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", "connector not found")
		return
	}

	writeConnectorJSON(w, http.StatusOK, c.Health())
}

// AuthURL handles GET /connectors/{id}/auth/url.
func (h *ConnectorHandler) AuthURL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	c, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", "connector not found")
		return
	}

	authenticator, ok := c.(plugin.Authenticator)
	if !ok {
		writeConnectorError(w, http.StatusUnprocessableEntity, "NOT_SUPPORTED", "connector does not support authentication")
		return
	}

	url, err := authenticator.AuthURL(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("auth url failed")
		writeConnectorError(w, http.StatusInternalServerError, "AUTH_URL_FAILED", err.Error())
		return
	}

	writeConnectorJSON(w, http.StatusOK, map[string]string{"url": url})
}

// AuthCallbackRequest is the body expected by AuthCallback.
type AuthCallbackRequest struct {
	Code string `json:"code"`
}

// AuthCallback handles POST /connectors/{id}/auth/callback.
func (h *ConnectorHandler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	c, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", "connector not found")
		return
	}

	authenticator, ok := c.(plugin.Authenticator)
	if !ok {
		writeConnectorError(w, http.StatusUnprocessableEntity, "NOT_SUPPORTED", "connector does not support authentication")
		return
	}

	var req AuthCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeConnectorError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	if err := authenticator.ExchangeCode(r.Context(), req.Code); err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("auth callback failed")
		writeConnectorError(w, http.StatusInternalServerError, "AUTH_CALLBACK_FAILED", err.Error())
		return
	}

	writeConnectorJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})
}

// SaveCredentials handles PUT /connectors/{id}/credentials.
func (h *ConnectorHandler) SaveCredentials(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	if h.credStore == nil {
		writeConnectorError(w, http.StatusServiceUnavailable, "CREDENTIAL_STORE_UNAVAILABLE", "credential store not configured")
		return
	}

	var fields map[string]string
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeConnectorError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}

	if err := h.credStore.SaveConnectorCredential(id, fields); err != nil {
		h.logger.Error().Err(err).Str("connector", id).Msg("save credentials failed")
		writeConnectorError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	if err := h.manager.ReinitConnector(id); err != nil {
		h.logger.Warn().Err(err).Str("connector", id).Msg("reinit after credentials save failed")
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Instance endpoints (multi-compte)
// ---------------------------------------------------------------------------

// CreateInstanceRequest is the body expected by CreateInstance.
type CreateInstanceRequest struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Settings    map[string]string `json:"settings"`
	Schedule    string            `json:"schedule"`
	Enabled     bool              `json:"enabled"`
}

// ListInstances handles GET /connectors/instances.
func (h *ConnectorHandler) ListInstances(w http.ResponseWriter, r *http.Request) {
	instances := h.manager.ListInstances()
	writeConnectorJSON(w, http.StatusOK, instances)
}

// CreateInstance handles POST /connectors/{type}/instances.
func (h *ConnectorHandler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "type")
	if !validConnectorID(typeID) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_TYPE", "invalid connector type")
		return
	}
	if !h.manager.HasFactory(typeID) {
		writeConnectorError(w, http.StatusNotFound, "TYPE_NOT_FOUND", "connector type not found or does not support multiple instances")
		return
	}

	var req CreateInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeConnectorError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON")
		return
	}
	if req.ID == "" {
		writeConnectorError(w, http.StatusBadRequest, "MISSING_ID", "instance id is required")
		return
	}
	if !validConnectorID(req.ID) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid instance id")
		return
	}

	cfg := plugin.ConnectorConfig{
		Enabled:  req.Enabled,
		Settings: req.Settings,
		Schedule: req.Schedule,
	}
	if err := h.manager.CreateInstance(typeID, req.ID, req.DisplayName, cfg); err != nil {
		h.logger.Error().Err(err).Str("type", typeID).Str("instance", req.ID).Msg("create instance failed")
		writeConnectorError(w, http.StatusConflict, "CREATE_FAILED", err.Error())
		return
	}

	if err := h.persistAllInstanceConfigs(); err != nil {
		h.logger.Warn().Err(err).Msg("failed to persist instance configs")
	}

	writeConnectorJSON(w, http.StatusCreated, map[string]string{"status": "created", "id": req.ID})
}

// DeleteInstance handles DELETE /connectors/instances/{instanceID}.
func (h *ConnectorHandler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceID")
	if !validConnectorID(instanceID) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid instance id")
		return
	}

	if err := h.manager.DeleteInstance(instanceID); err != nil {
		h.logger.Error().Err(err).Str("instance", instanceID).Msg("delete instance failed")
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	if err := h.persistAllInstanceConfigs(); err != nil {
		h.logger.Warn().Err(err).Msg("failed to persist instance configs")
	}

	w.WriteHeader(http.StatusNoContent)
}

// persistAllInstanceConfigs persists all dynamic instances to config.yaml.
func (h *ConnectorHandler) persistAllInstanceConfigs() error {
	instances := h.manager.ListInstances()
	cfgInstances := make([]config.ConnectorInstanceConfig, 0, len(instances))
	for _, inst := range instances {
		// Skip instances that have the same ID as their typeID — those are
		// registered via Register() (legacy path) and persisted by persistAllConfigs.
		if inst.InstanceID == inst.TypeID {
			continue
		}
		cfg, _ := h.manager.GetConfig(inst.InstanceID)
		safeSettings := make(map[string]string, len(cfg.Settings))
		for k, v := range cfg.Settings {
			safeSettings[k] = v
		}
		if conn, ok := h.manager.Get(inst.InstanceID); ok {
			if sp, ok := conn.(plugin.SecretFieldProvider); ok {
				for _, key := range sp.SecretFieldKeys() {
					delete(safeSettings, key)
				}
			}
		}
		cfgInstances = append(cfgInstances, config.ConnectorInstanceConfig{
			ID:          inst.InstanceID,
			TypeName:    inst.TypeID,
			DisplayName: inst.DisplayName,
			Enabled:     cfg.Enabled,
			Settings:    safeSettings,
			Schedule:    cfg.Schedule,
		})
	}
	return config.SaveConnectorInstancesConfig(h.configPath, cfgInstances)
}

// persistAllConfigs reads all connector configs from the manager and writes them
// to config.yaml using SaveConnectorsConfig.
// Secret fields (declared via plugin.SecretFieldProvider) are stripped before
// writing so that passwords and OAuth tokens are never persisted in plain text.
func (h *ConnectorHandler) persistAllConfigs() error {
	infos := h.manager.ListInfos()
	settings := make(map[string]config.ConnectorSettings, len(infos))
	for _, info := range infos {
		cfg, ok := h.manager.GetConfig(info.ID)
		if !ok {
			continue
		}
		// Copy settings to avoid mutating the manager's in-memory state.
		safeSettings := make(map[string]string, len(cfg.Settings))
		for k, v := range cfg.Settings {
			safeSettings[k] = v
		}
		// Strip secret fields — never persist them to yaml.
		if connector, ok := h.manager.Get(info.ID); ok {
			if sp, ok := connector.(plugin.SecretFieldProvider); ok {
				for _, key := range sp.SecretFieldKeys() {
					delete(safeSettings, key)
				}
			}
		}
		settings[info.ID] = config.ConnectorSettings{
			Enabled:  cfg.Enabled,
			Settings: safeSettings,
			Schedule: cfg.Schedule,
		}
	}
	return config.SaveConnectorsConfig(h.configPath, settings)
}

// validConnectorID returns true when the ID is safe (no path traversal).
func validConnectorID(id string) bool {
	return id != "" && filepath.Base(id) == id
}

// ---------------------------------------------------------------------------
// Mailboxes / Labels endpoints
// ---------------------------------------------------------------------------

// Mailboxes handles GET /connectors/{id}/mailboxes.
// Returns available mailboxes/labels for the connector.
func (h *ConnectorHandler) Mailboxes(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "invalid connector id", "invalid connector id")
		return
	}

	conn, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "connector not found", "connector not found")
		return
	}

	// Try mailboxes first (for IMAP-based connectors like Proton)
	lister, ok := conn.(interface {
		ListMailboxes(ctx context.Context) ([]string, error)
	})
	if ok {
		mailboxes, err := lister.ListMailboxes(r.Context())
		if err != nil {
			writeConnectorError(w, http.StatusInternalServerError, "internal error", err.Error())
			return
		}
		writeConnectorJSON(w, http.StatusOK, mailboxes)
		return
	}

	// Fall back to labels (for Gmail and others)
	labeler, ok := conn.(interface {
		ListLabels(ctx context.Context) ([]mailpkg.Label, error)
	})
	if ok {
		labels, err := labeler.ListLabels(r.Context())
		if err != nil {
			writeConnectorError(w, http.StatusInternalServerError, "internal error", err.Error())
			return
		}
		// Convert labels to simple strings for the UI
		names := make([]string, len(labels))
		for i, l := range labels {
			names[i] = l.Name
		}
		writeConnectorJSON(w, http.StatusOK, names)
		return
	}

	writeConnectorError(w, http.StatusNotImplemented, "not supported", "connector does not support mailboxes")
}

// Labels handles GET /connectors/{id}/labels.
// Returns available Gmail labels for the connector.
func (h *ConnectorHandler) Labels(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validConnectorID(id) {
		writeConnectorError(w, http.StatusBadRequest, "INVALID_ID", "invalid connector id")
		return
	}

	conn, ok := h.manager.Get(id)
	if !ok {
		writeConnectorError(w, http.StatusNotFound, "NOT_FOUND", "connector not found")
		return
	}

	labeler, ok := conn.(interface {
		ListLabels(ctx context.Context) ([]mailpkg.Label, error)
	})
	if !ok {
		writeConnectorError(w, http.StatusNotImplemented, "NOTSUPPORTED", "connector does not support labels")
		return
	}

	labels, err := labeler.ListLabels(r.Context())
	if err != nil {
		writeConnectorError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}

	writeConnectorJSON(w, http.StatusOK, labels)
}

// writeConnectorJSON writes a JSON response with the given status code.
func writeConnectorJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeConnectorError writes a structured JSON error response.
func writeConnectorError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// isTimeoutError checks if an error is a timeout error.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}
