package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/rs/zerolog"
)

// ErrSyncInProgress est retourné par TriggerSync si une sync est déjà en cours
// pour le connecteur demandé.
var ErrSyncInProgress = fmt.Errorf("sync already in progress for this connector")

// instanceMeta stocke le typeID et le displayName d'une instance dynamique.
type instanceMeta struct {
	TypeID      string
	DisplayName string
}

// Manager gère le cycle de vie de tous les connecteurs.
// Thread-safe via mu.
type Manager struct {
	mu             sync.RWMutex
	connectors     map[string]Connector       // instanceID → instance
	configs        map[string]ConnectorConfig // instanceID → config persistée
	factories      map[string]func() Connector // typeID → factory fn
	metas          map[string]instanceMeta    // instanceID → (typeID, displayName)
	credStore      *auth.CredentialStore      // stockage sécurisé des secrets
	scheduler      *Scheduler                 // planification des syncs
	logger         zerolog.Logger
	startCtx       context.Context // contexte racine fourni par Start()
	syncInProgress map[string]bool // guard contre les syncs concurrents
}

// NewManager crée un Manager.
// Le credStore peut être nil si HYGUR_CRED_KEY n'est pas défini (mode dégradé).
func NewManager(credStore *auth.CredentialStore, logger zerolog.Logger) *Manager {
	m := &Manager{
		connectors:     make(map[string]Connector),
		configs:        make(map[string]ConnectorConfig),
		factories:      make(map[string]func() Connector),
		metas:          make(map[string]instanceMeta),
		credStore:      credStore,
		syncInProgress: make(map[string]bool),
		logger:         logger.With().Str("component", "plugin-manager").Logger(),
	}
	m.scheduler = NewScheduler(m, logger)
	return m
}

// Register enregistre un connecteur.
// Si le Manager est déjà démarré (Start() a été appelé) et que la config du
// connecteur est Enabled, le connecteur est initialisé et démarré immédiatement
// (hot-start).
// Retourne une erreur si l'ID est déjà enregistré.
func (m *Manager) Register(c Connector) error {
	m.mu.Lock()
	id := c.Info().ID
	if _, exists := m.connectors[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("connector %q already registered", id)
	}
	m.connectors[id] = c
	if _, ok := m.configs[id]; !ok {
		enabled := false
		if p, ok := c.(DefaultEnabledProvider); ok {
			enabled = p.EnabledByDefault()
		}
		m.configs[id] = ConnectorConfig{Enabled: enabled}
	}
	cfg := m.configs[id]
	ctx := m.startCtx // nil si Start() pas encore appelé
	m.mu.Unlock()

	// Hot-start : si le Manager tourne déjà et config enabled
	if ctx != nil && cfg.Enabled {
		m.initAndStart(ctx, id, c, cfg)
	}
	return nil
}

// Configure met à jour la configuration d'un connecteur et réinitialise
// celui-ci à chaud pour que le nouveau provider/credentials soient actifs
// immédiatement, sans redémarrage du sidecar.
func (m *Manager) Configure(id string, cfg ConnectorConfig) error {
	m.mu.Lock()
	_, exists := m.connectors[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("connector %q not found", id)
	}
	conn := m.connectors[id]
	m.configs[id] = cfg
	m.mu.Unlock()

	// Re-run Init (and Start if enabled) so that fields like activeSource,
	// refresh tokens, and mailbox settings are applied immediately.
	// Skip when the manager hasn't been started yet (startCtx is nil) — the
	// normal Start() call will pick up the stored config on its own.
	m.mu.RLock()
	ctx := m.startCtx
	m.mu.RUnlock()
	if ctx == nil {
		return nil
	}

	if cfg.Enabled {
		if err := m.ReinitConnector(id); err != nil {
			return err
		}
		m.triggerSyncAsync(id, "configure")
		return nil
	}

	// Disabled: still refresh the connector's in-memory config so config-driven
	// reads (ListMailboxes, health, schema fetches) use the latest saved values
	// — otherwise a corrected host/credential typed while disabled is ignored
	// until the connector is enabled. Init must not dial (only Start does), so
	// this is cheap and safe; failures are non-fatal (partial config).
	if conn != nil {
		if err := conn.Init(ctx, m.withSecrets(id, conn, cfg)); err != nil {
			m.logger.Debug().Err(err).Str("connector", id).Msg("config refresh init (non-fatal)")
		}
	}
	return nil
}

// Start initialise et démarre tous les connecteurs actifs.
// Stocke le ctx racine pour pouvoir activer des connecteurs à chaud.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.startCtx = ctx
	connectors := make(map[string]Connector, len(m.connectors))
	configs := make(map[string]ConnectorConfig, len(m.configs))
	for id, c := range m.connectors {
		connectors[id] = c
		configs[id] = m.configs[id]
	}
	m.mu.Unlock()

	for id, c := range connectors {
		cfg := configs[id]
		if !cfg.Enabled {
			continue
		}
		m.initAndStart(ctx, id, c, cfg)
	}

	m.scheduler.Start(ctx)
	return nil
}

// withSecrets merges the connector's stored secret fields (from the credential
// store) into cfg.Settings, so connectors that read secrets from their config
// (e.g. IMAP/CalDAV password) receive them at Init without each needing direct
// credential-store access. Secrets are deliberately kept out of config.yaml;
// this re-injects them in-memory only, right before Init.
func (m *Manager) withSecrets(id string, c Connector, cfg ConnectorConfig) ConnectorConfig {
	sp, ok := c.(SecretFieldProvider)
	if !ok || m.credStore == nil {
		return cfg
	}
	keys := sp.SecretFieldKeys()
	if len(keys) == 0 {
		return cfg
	}
	creds, err := m.credStore.GetConnectorCredential(id)
	if err != nil || len(creds) == 0 {
		return cfg
	}
	merged := make(map[string]string, len(cfg.Settings)+len(keys))
	for k, v := range cfg.Settings {
		merged[k] = v
	}
	for _, key := range keys {
		if v := creds[key]; v != "" {
			merged[key] = v
		}
	}
	cfg.Settings = merged
	return cfg
}

// initAndStart initialise et démarre un connecteur unique.
// Non-fatal : loggue les erreurs sans faire planter le Manager.
func (m *Manager) initAndStart(ctx context.Context, id string, c Connector, cfg ConnectorConfig) {
	cfg = m.withSecrets(id, c, cfg)
	if err := c.Init(ctx, cfg); err != nil {
		m.logger.Error().Str("connector", id).Err(err).Msg("init failed")
		return
	}
	if err := c.Start(ctx); err != nil {
		m.logger.Error().Str("connector", id).Err(err).Msg("start failed")
		return
	}
	m.logger.Info().Str("connector", id).Msg("connector started")
	if _, ok := c.(Syncer); ok && cfg.Schedule != "" {
		if err := m.scheduler.Add(id, cfg.Schedule); err != nil {
			m.logger.Warn().Str("connector", id).Err(err).Msg("scheduling failed")
		}
	}
}

// EnableConnector active un connecteur à chaud (sans redémarrage du Manager).
// Met à jour la config persistée, appelle Init + Start.
func (m *Manager) EnableConnector(id string) error {
	m.mu.Lock()
	c, ok := m.connectors[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("connector %q not found", id)
	}
	cfg := m.configs[id]
	cfg.Enabled = true
	m.configs[id] = cfg
	ctx := m.startCtx
	m.mu.Unlock()

	if ctx == nil {
		return fmt.Errorf("manager not started")
	}
	m.initAndStart(ctx, id, c, cfg)
	m.triggerSyncAsync(id, "enable")
	return nil
}

// ReinitConnector re-runs Init + Start on an already-enabled connector using the
// currently stored configuration. This is called after credentials are written
// to the credential store so the connector picks them up without a full
// restart of the sidecar.
func (m *Manager) ReinitConnector(id string) error {
	m.mu.Lock()
	c, ok := m.connectors[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("connector %q not found", id)
	}
	cfg := m.configs[id]
	ctx := m.startCtx
	m.mu.Unlock()

	if ctx == nil {
		return fmt.Errorf("manager not started")
	}
	if !cfg.Enabled {
		// Not running, so there's nothing to Stop/Start — but still refresh the
		// in-memory config WITH secrets. ReinitConnector is called right after
		// credentials are saved (PUT /credentials), and the UI persists config
		// BEFORE credentials; without this, a disabled connector's cfg.Settings
		// never receives its stored password, so config-driven reads
		// (ListMailboxes "Load folders", health checks) see an empty secret.
		// Init must not dial — only Start does — so this is cheap.
		if err := c.Init(ctx, m.withSecrets(id, c, cfg)); err != nil {
			m.logger.Debug().Err(err).Str("connector", id).Msg("reinit refresh init (non-fatal)")
		}
		return nil
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Stop(stopCtx); err != nil {
		m.logger.Debug().Str("connector", id).Err(err).Msg("stop during reinit")
	}

	m.initAndStart(ctx, id, c, cfg)
	return nil
}

// DisableConnector arrête un connecteur à chaud et le retire du scheduler.
func (m *Manager) DisableConnector(id string) error {
	m.mu.Lock()
	c, ok := m.connectors[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("connector %q not found", id)
	}
	cfg := m.configs[id]
	cfg.Enabled = false
	m.configs[id] = cfg
	m.mu.Unlock()

	m.scheduler.Remove(id)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Stop(stopCtx); err != nil {
		m.logger.Warn().Str("connector", id).Err(err).Msg("stop error during disable")
	}
	m.logger.Info().Str("connector", id).Msg("connector disabled")
	return nil
}

// Stop arrête tous les connecteurs et le scheduler.
func (m *Manager) Stop(ctx context.Context) {
	m.scheduler.Stop()

	m.mu.RLock()
	defer m.mu.RUnlock()

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for id, c := range m.connectors {
		if m.configs[id].Enabled {
			wg.Add(1)
			go func(id string, c Connector) {
				defer wg.Done()
				if err := c.Stop(stopCtx); err != nil {
					m.logger.Warn().Str("connector", id).Err(err).Msg("stop error")
				}
			}(id, c)
		}
	}
	wg.Wait()
}

// TriggerSync déclenche une synchronisation manuelle pour un connecteur.
// Retourne ErrSyncInProgress si une sync est déjà en cours pour cet ID.
func (m *Manager) TriggerSync(ctx context.Context, id string, opts SyncOptions) (*SyncResult, error) {
	m.mu.Lock()
	c, ok := m.connectors[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("connector %q not found", id)
	}
	if m.syncInProgress[id] {
		m.mu.Unlock()
		return nil, ErrSyncInProgress
	}
	m.syncInProgress[id] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.syncInProgress, id)
		m.mu.Unlock()
	}()

	syncer, ok := c.(Syncer)
	if !ok {
		return nil, fmt.Errorf("connector %q does not support sync", id)
	}

	return syncer.Sync(ctx, opts)
}

// triggerSyncAsync fires an incremental sync in the background. Used by
// Configure and EnableConnector so the user sees freshly-indexed content
// immediately on save instead of waiting for the next cron tick.
//
// Failures are logged and not surfaced — the scheduled sync (or a manual
// retry) will catch transient errors. ErrSyncInProgress is silently
// ignored: a sync is already covering the work.
func (m *Manager) triggerSyncAsync(id, reason string) {
	m.mu.RLock()
	c, ok := m.connectors[id]
	parent := m.startCtx
	m.mu.RUnlock()
	if !ok || parent == nil {
		return
	}
	if _, ok := c.(Syncer); !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		result, err := m.TriggerSync(ctx, id, SyncOptions{})
		if err != nil {
			if !errors.Is(err, ErrSyncInProgress) {
				m.logger.Warn().
					Str("connector", id).
					Str("reason", reason).
					Err(err).
					Msg("auto-sync failed")
			}
			return
		}
		m.logger.Info().
			Str("connector", id).
			Str("reason", reason).
			Int("indexed", result.Processed).
			Int("skipped", result.Skipped).
			Int("errors", result.Failed).
			Dur("duration", result.Duration).
			Msg("auto-sync completed")
	}()
}

// Get retourne un connecteur par ID.
func (m *Manager) Get(id string) (Connector, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.connectors[id]
	return c, ok
}

// ListInfos retourne les infos de tous les connecteurs enregistrés.
func (m *Manager) ListInfos() []ConnectorInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]ConnectorInfo, 0, len(m.connectors))
	for _, c := range m.connectors {
		infos = append(infos, c.Info())
	}
	return infos
}

// AllHealth retourne l'état de santé de tous les connecteurs.
func (m *Manager) AllHealth() map[string]HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]HealthStatus, len(m.connectors))
	for id, c := range m.connectors {
		result[id] = c.Health()
	}
	return result
}

// GetConfig retourne la config d'un connecteur.
func (m *Manager) GetConfig(id string) (ConnectorConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.configs[id]
	return cfg, ok
}

// RegisterFactory enregistre une factory pour un type de connecteur.
// Utilisé pour les connecteurs multi-instances (ex: IMAP).
func (m *Manager) RegisterFactory(typeID string, factory func() Connector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[typeID] = factory
}

// HasFactory retourne true si une factory est enregistrée pour ce typeID.
func (m *Manager) HasFactory(typeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.factories[typeID]
	return ok
}

// CreateInstance crée une nouvelle instance d'un connecteur multi-instance.
// typeID doit avoir une factory enregistrée via RegisterFactory.
// instanceID doit être unique parmi toutes les instances.
func (m *Manager) CreateInstance(typeID, instanceID, displayName string, cfg ConnectorConfig) error {
	m.mu.Lock()
	factory, ok := m.factories[typeID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no factory registered for connector type %q", typeID)
	}
	if _, exists := m.connectors[instanceID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("instance %q already exists", instanceID)
	}
	conn := factory()
	m.connectors[instanceID] = conn
	m.configs[instanceID] = cfg
	m.metas[instanceID] = instanceMeta{TypeID: typeID, DisplayName: displayName}
	ctx := m.startCtx
	m.mu.Unlock()

	if ctx != nil && cfg.Enabled {
		m.initAndStart(ctx, instanceID, conn, cfg)
	}
	return nil
}

// DeleteInstance arrête et supprime une instance dynamique.
func (m *Manager) DeleteInstance(instanceID string) error {
	m.mu.Lock()
	conn, ok := m.connectors[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance %q not found", instanceID)
	}
	m.mu.Unlock()

	m.scheduler.Remove(instanceID)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Stop(stopCtx); err != nil {
		m.logger.Warn().Str("instance", instanceID).Err(err).Msg("stop error during delete")
	}

	m.mu.Lock()
	delete(m.connectors, instanceID)
	delete(m.configs, instanceID)
	delete(m.metas, instanceID)
	m.mu.Unlock()

	return nil
}

// HasInstance retourne true si l'instanceID est enregistré.
func (m *Manager) HasInstance(instanceID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.connectors[instanceID]
	return ok
}

// InstanceInfo regroupe les métadonnées d'une instance pour le listing.
type InstanceInfo struct {
	InstanceID  string         `json:"instance_id"`
	TypeID      string         `json:"type_id"`
	DisplayName string         `json:"display_name"`
	Info        ConnectorInfo  `json:"info"`
	Enabled     bool           `json:"enabled"`
	Health      HealthStatus   `json:"health"`
}

// ListInstances retourne toutes les instances enregistrées (types uniques + instances dynamiques).
func (m *Manager) ListInstances() []InstanceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]InstanceInfo, 0, len(m.connectors))
	for instanceID, conn := range m.connectors {
		meta := m.metas[instanceID]
		cfg := m.configs[instanceID]
		typeID := meta.TypeID
		if typeID == "" {
			typeID = instanceID // connecteurs enregistrés via Register() ont instanceID == typeID
		}
		displayName := meta.DisplayName
		if displayName == "" {
			displayName = conn.Info().Name
		}
		result = append(result, InstanceInfo{
			InstanceID:  instanceID,
			TypeID:      typeID,
			DisplayName: displayName,
			Info:        conn.Info(),
			Enabled:     cfg.Enabled,
			Health:      conn.Health(),
		})
	}
	// Deterministic order: m.connectors is a map (random iteration), which made
	// the Connectors list jump around between refreshes. Sort by type then
	// instance id so the list stays fixed regardless of startup/registration
	// order. Singleton connectors (instanceID == typeID) sort before their
	// dynamic "+"-added instances of the same type.
	sort.Slice(result, func(i, j int) bool {
		if result[i].TypeID != result[j].TypeID {
			return result[i].TypeID < result[j].TypeID
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	return result
}
