package plugin

import (
	"context"
	"sync"
	"testing"

	"github.com/hygur/sidecar/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretConnector implements Connector + SecretFieldProvider and records the
// config it last received at Init — so a test can assert whether the manager
// injected credential-store secrets into cfg.Settings before Init.
type secretConnector struct {
	id string

	mu      sync.Mutex
	lastCfg ConnectorConfig
}

func (c *secretConnector) Info() ConnectorInfo        { return ConnectorInfo{ID: c.id, Name: c.id} }
func (c *secretConnector) Capabilities() Capabilities { return Capabilities{} }
func (c *secretConnector) ConfigSchema() ConfigSchema { return ConfigSchema{} }
func (c *secretConnector) Health() HealthStatus       { return HealthStatus{Status: StatusHealthy} }
func (c *secretConnector) Start(_ context.Context) error { return nil }
func (c *secretConnector) Stop(_ context.Context) error  { return nil }

func (c *secretConnector) Init(_ context.Context, cfg ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastCfg = cfg
	return nil
}

// SecretFieldProvider: the password is stored in the credential store, never in
// config.yaml — exactly like the IMAP/CalDAV connectors.
func (c *secretConnector) SecretFieldKeys() []string { return []string{"password"} }

func (c *secretConnector) password() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastCfg.Settings["password"]
}

func newTestCredStore(t *testing.T) *auth.CredentialStore {
	t.Helper()
	t.Setenv("HYGUR_CRED_KEY", "manager-secrets-test-key")
	cs, err := auth.NewCredentialStore(t.TempDir())
	require.NoError(t, err)
	return cs
}

// TestManager_ReinitConnector_Disabled_InjectsStoredSecret reproduces the IMAP
// "Load folders" bug: the UI persists config (PUT /config) BEFORE credentials
// (PUT /credentials), and the connector is not yet enabled. Saving credentials
// calls ReinitConnector; the connector must end up with the stored password in
// its in-memory cfg.Settings so config-driven reads (ListMailboxes) succeed —
// even though it stays disabled.
func TestManager_ReinitConnector_Disabled_InjectsStoredSecret(t *testing.T) {
	cs := newTestCredStore(t)
	m := NewManager(cs, nopLogger())

	conn := &secretConnector{id: "imap"}
	require.NoError(t, m.Register(conn))

	// Connector configured (host/username) but DISABLED — mirrors a user
	// filling the form without enabling the connector.
	m.mu.Lock()
	m.configs["imap"] = ConnectorConfig{
		Enabled:  false,
		Settings: map[string]string{"host": "imap.example.com", "username": "user"},
	}
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	// Credentials are saved AFTER config (the real handler order), then the
	// handler calls ReinitConnector.
	require.NoError(t, cs.SaveConnectorCredential("imap", map[string]string{"password": "s3cret"}))
	require.NoError(t, m.ReinitConnector("imap"))

	assert.Equal(t, "s3cret", conn.password(),
		"disabled connector must receive its stored password at Init via ReinitConnector")
}

// TestManager_initAndStart_InjectsStoredSecret covers the enabled path: a
// connector enabled with stored credentials must receive the password at Init.
func TestManager_EnableConnector_InjectsStoredSecret(t *testing.T) {
	cs := newTestCredStore(t)
	m := NewManager(cs, nopLogger())

	conn := &secretConnector{id: "imap"}
	require.NoError(t, m.Register(conn))

	m.mu.Lock()
	m.configs["imap"] = ConnectorConfig{
		Settings: map[string]string{"host": "imap.example.com", "username": "user"},
	}
	m.mu.Unlock()

	require.NoError(t, cs.SaveConnectorCredential("imap", map[string]string{"password": "s3cret"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, m.Start(ctx))

	require.NoError(t, m.EnableConnector("imap"))

	assert.Equal(t, "s3cret", conn.password(),
		"enabled connector must receive its stored password at Init")
}

// TestManager_withSecrets_NoCredStore_NoPanic guards the degraded mode where no
// credential store is configured (HYGUR_CRED_KEY unset): withSecrets must be a
// safe pass-through.
func TestManager_withSecrets_NoCredStore_PassThrough(t *testing.T) {
	m := NewManager(nil, nopLogger())
	conn := &secretConnector{id: "imap"}
	cfg := ConnectorConfig{Settings: map[string]string{"host": "h"}}
	got := m.withSecrets("imap", conn, cfg)
	assert.Equal(t, cfg, got)
}
