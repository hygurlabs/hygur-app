package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hygur/sidecar/internal/plugin"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Minimal stub connector that implements SecretFieldProvider
// ---------------------------------------------------------------------------

type stubSecretConnector struct {
	id         string
	secretKeys []string
}

func (s *stubSecretConnector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{ID: s.id, Name: s.id}
}
func (s *stubSecretConnector) Capabilities() plugin.Capabilities                      { return plugin.Capabilities{} }
func (s *stubSecretConnector) ConfigSchema() plugin.ConfigSchema                      { return plugin.ConfigSchema{} }
func (s *stubSecretConnector) Health() plugin.HealthStatus                            { return plugin.HealthStatus{} }
func (s *stubSecretConnector) Init(_ context.Context, _ plugin.ConnectorConfig) error { return nil }
func (s *stubSecretConnector) Start(_ context.Context) error                          { return nil }
func (s *stubSecretConnector) Stop(_ context.Context) error                           { return nil }

// SecretFieldKeys satisfies plugin.SecretFieldProvider.
func (s *stubSecretConnector) SecretFieldKeys() []string { return s.secretKeys }

// ---------------------------------------------------------------------------
// Minimal stub connector without SecretFieldProvider
// ---------------------------------------------------------------------------

type stubPlainConnector struct {
	id string
}

func (s *stubPlainConnector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{ID: s.id, Name: s.id}
}
func (s *stubPlainConnector) Capabilities() plugin.Capabilities                      { return plugin.Capabilities{} }
func (s *stubPlainConnector) ConfigSchema() plugin.ConfigSchema                      { return plugin.ConfigSchema{} }
func (s *stubPlainConnector) Health() plugin.HealthStatus                            { return plugin.HealthStatus{} }
func (s *stubPlainConnector) Init(_ context.Context, _ plugin.ConnectorConfig) error { return nil }
func (s *stubPlainConnector) Start(_ context.Context) error                          { return nil }
func (s *stubPlainConnector) Stop(_ context.Context) error                           { return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestHandler(t *testing.T, configPath string, connectors ...plugin.Connector) *ConnectorHandler {
	t.Helper()
	m := plugin.NewManager(nil, zerolog.Nop())
	for _, c := range connectors {
		require.NoError(t, m.Register(c))
	}
	return NewConnectorHandler(m, nil, configPath, zerolog.Nop())
}

// readSavedConnectorSettings reads back what was written to configPath and
// returns the per-connector settings map.
func readSavedConnectorSettings(t *testing.T, configPath string) map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	connectors, ok := raw["connectors"]
	if !ok {
		return nil
	}
	result := make(map[string]map[string]any)
	for id, v := range connectors.(map[string]any) {
		if m, ok := v.(map[string]any); ok {
			settings, _ := m["settings"].(map[string]any)
			result[id] = settings
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestPersistAllConfigs_StripsSecretFields verifies that keys declared by
// SecretFieldProvider are removed from the YAML output even when they are
// present in the in-memory connector config.
func TestPersistAllConfigs_StripsSecretFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	conn := &stubSecretConnector{
		id:         "mail",
		secretKeys: []string{"password", "client_secret"},
	}
	h := newTestHandler(t, configPath, conn)

	// Inject settings that include secret fields into the manager's in-memory state.
	require.NoError(t, h.manager.Configure("mail", plugin.ConnectorConfig{
		Enabled: true,
		Settings: map[string]string{
			"provider":      "proton",
			"username":      "alice@pm.me",
			"password":      "super-secret",
			"client_secret": "oauth-secret",
			"mailbox":       "All Mail",
		},
	}))

	require.NoError(t, h.persistAllConfigs())

	saved := readSavedConnectorSettings(t, configPath)
	mailSettings := saved["mail"]
	require.NotNil(t, mailSettings)

	// Non-secret fields must be present.
	assert.Equal(t, "proton", mailSettings["provider"])
	assert.Equal(t, "alice@pm.me", mailSettings["username"])
	assert.Equal(t, "All Mail", mailSettings["mailbox"])

	// Secret fields must be absent.
	assert.NotContains(t, mailSettings, "password", "password must not be persisted to yaml")
	assert.NotContains(t, mailSettings, "client_secret", "client_secret must not be persisted to yaml")
}

// TestPersistAllConfigs_PlainConnector_AllFieldsPersisted verifies that when a
// connector does not implement SecretFieldProvider, all settings are kept.
func TestPersistAllConfigs_PlainConnector_AllFieldsPersisted(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	conn := &stubPlainConnector{id: "files"}
	h := newTestHandler(t, configPath, conn)

	require.NoError(t, h.manager.Configure("files", plugin.ConnectorConfig{
		Enabled: true,
		Settings: map[string]string{
			"root_path": "/home/alice/docs",
			"token":     "should-be-kept",
		},
	}))

	require.NoError(t, h.persistAllConfigs())

	saved := readSavedConnectorSettings(t, configPath)
	filesSettings := saved["files"]
	require.NotNil(t, filesSettings)

	assert.Equal(t, "/home/alice/docs", filesSettings["root_path"])
	assert.Equal(t, "should-be-kept", filesSettings["token"])
}

// TestPersistAllConfigs_DoesNotMutateManagerState verifies that stripping
// secrets from the YAML output does NOT affect the in-memory connector config
// stored in the Manager (the connector must still have the password in memory
// so it can use it for the active session).
func TestPersistAllConfigs_DoesNotMutateManagerState(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	conn := &stubSecretConnector{
		id:         "mail",
		secretKeys: []string{"password"},
	}
	h := newTestHandler(t, configPath, conn)

	originalSettings := map[string]string{
		"provider": "proton",
		"password": "my-password",
	}
	require.NoError(t, h.manager.Configure("mail", plugin.ConnectorConfig{
		Enabled:  true,
		Settings: originalSettings,
	}))

	require.NoError(t, h.persistAllConfigs())

	// The in-memory config must still hold the password.
	cfg, ok := h.manager.GetConfig("mail")
	require.True(t, ok)
	assert.Equal(t, "my-password", cfg.Settings["password"],
		"persistAllConfigs must not mutate in-memory connector settings")
}
