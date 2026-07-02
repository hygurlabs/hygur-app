package imap_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	imapconn "github.com/hygur/sidecar/internal/connectors/imap"
	"github.com/hygur/sidecar/internal/plugin"
)

// ---------------------------------------------------------------------------
// TestIMAPConnector_Info
// ---------------------------------------------------------------------------

func TestIMAPConnector_Info(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	info := c.Info()

	assert.Equal(t, "imap", info.ID)
	assert.Equal(t, "IMAP", info.Name)
	assert.Equal(t, "envelope", info.Icon)
	assert.Equal(t, "1.0.0", info.Version)
	assert.Contains(t, info.Tags, "email")
	assert.Contains(t, info.Tags, "imap")
	assert.NotEmpty(t, info.Description)
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Capabilities
// ---------------------------------------------------------------------------

func TestIMAPConnector_Capabilities(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	caps := c.Capabilities()

	assert.True(t, caps.CanSync, "CanSync must be true")
	assert.False(t, caps.CanSearch)
	assert.True(t, caps.NeedsAuth)
	assert.Equal(t, plugin.AuthPassword, caps.AuthType)
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_ConfigSchema
// ---------------------------------------------------------------------------

func TestIMAPConnector_ConfigSchema(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	schema := c.ConfigSchema()

	require.NotEmpty(t, schema.Groups)

	// Collect all field keys across groups.
	allKeys := map[string]plugin.ConfigField{}
	for _, g := range schema.Groups {
		for _, f := range g.Fields {
			allKeys[f.Key] = f
		}
	}

	// Required fields must be present.
	for _, key := range []string{"host", "username", "password", "port", "tls", "mailbox", "max_messages"} {
		_, ok := allKeys[key]
		assert.True(t, ok, "expected config field %q", key)
	}

	// password must be secret.
	assert.Equal(t, plugin.FieldSecret, allKeys["password"].Type, "password must be FieldSecret")

	// host and username must be required.
	assert.True(t, allKeys["host"].Required, "host must be required")
	assert.True(t, allKeys["username"].Required, "username must be required")

	// port must default to 993.
	assert.Equal(t, "993", allKeys["port"].Default)

	// mailbox must default to INBOX.
	assert.Equal(t, "INBOX", allKeys["mailbox"].Default)
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_SecretFieldKeys
// ---------------------------------------------------------------------------

func TestIMAPConnector_SecretFieldKeys(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	keys := c.SecretFieldKeys()
	assert.Contains(t, keys, "password", "password must be declared as a secret field")
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Configure_ValidatesRequiredFields
// ---------------------------------------------------------------------------

func TestIMAPConnector_Configure_ValidatesRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		cfg     plugin.ConnectorConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing host",
			cfg: plugin.ConnectorConfig{
				Settings: map[string]string{
					"username": "user@example.com",
					"password": "secret",
				},
			},
			wantErr: true,
			errMsg:  "host",
		},
		{
			name: "missing username",
			cfg: plugin.ConnectorConfig{
				Settings: map[string]string{
					"host":     "imap.example.com",
					"password": "secret",
				},
			},
			wantErr: true,
			errMsg:  "username",
		},
		{
			name: "valid minimal config",
			cfg: plugin.ConnectorConfig{
				Settings: map[string]string{
					"host":     "imap.example.com",
					"username": "user@example.com",
					"password": "secret",
				},
			},
			wantErr: false,
		},
		{
			name: "valid full config",
			cfg: plugin.ConnectorConfig{
				Settings: map[string]string{
					"host":         "imap.fastmail.com",
					"port":         "993",
					"username":     "user@fastmail.com",
					"password":     "app-password",
					"tls":          "true",
					"mailbox":      "INBOX",
					"max_messages": "200",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
			err := c.Init(context.Background(), tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)

				h := c.Health()
				assert.Equal(t, plugin.StatusUnconfigured, h.Status)
			} else {
				require.NoError(t, err)

				h := c.Health()
				// After a successful Init the connector moves to StatusConnecting
				// (no actual dial happens in Init).
				assert.Equal(t, plugin.StatusConnecting, h.Status)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Health_InitialState
// ---------------------------------------------------------------------------

func TestIMAPConnector_Health_InitialState(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	h := c.Health()

	assert.Equal(t, plugin.StatusUnconfigured, h.Status)
	assert.True(t, h.LastSync.IsZero())
	assert.Equal(t, int64(0), h.ItemCount)
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Stop_WhenNotStarted
// ---------------------------------------------------------------------------

func TestIMAPConnector_Stop_WhenNotStarted(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	err := c.Stop(context.Background())
	assert.NoError(t, err, "Stop must be a no-op when not started")
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Start_NoOp
// ---------------------------------------------------------------------------

func TestIMAPConnector_Start_NoOp(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	err := c.Start(context.Background())
	assert.NoError(t, err, "Start must be a no-op (connections are per-Sync)")
}

// ---------------------------------------------------------------------------
// TestIMAPConnector_Sync_NilDB_ReturnsError
// ---------------------------------------------------------------------------

func TestIMAPConnector_Sync_NilDB_ReturnsError(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	// Sync without Init → host is empty → immediate error.
	_, err := c.Sync(context.Background(), plugin.SyncOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host missing")
}

// ---------------------------------------------------------------------------
// Message parsing fixtures
// ---------------------------------------------------------------------------

// TestExtractPlainText_* exercises the internal body parser indirectly by
// verifying that the resulting KnowledgeItem contains the expected text.
// We can only test the exported Sync path end-to-end with a real IMAP server;
// however, we can test the structural validator via Init.

// TestIMAPConnector_Init_StatusConnecting ensures the health is StatusConnecting
// after a successful Init with required fields, confirming it progresses past
// StatusUnconfigured without performing any network IO.
func TestIMAPConnector_Init_StatusConnecting(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	err := c.Init(context.Background(), plugin.ConnectorConfig{
		Settings: map[string]string{
			"host":     "imap.gmail.com",
			"username": "test@gmail.com",
			"password": "app-pass",
		},
	})
	require.NoError(t, err)

	h := c.Health()
	assert.Equal(t, plugin.StatusConnecting, h.Status)
}

// TestIMAPConnector_Init_EmptyHost_Error validates that Init returns an error
// when the host setting is entirely absent.
func TestIMAPConnector_Init_EmptyHost_Error(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	err := c.Init(context.Background(), plugin.ConnectorConfig{
		Settings: map[string]string{
			"username": "user@example.com",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")
}

// TestIMAPConnector_Init_EmptyUsername_Error validates that Init returns an
// error when the username setting is absent (even if host is set).
func TestIMAPConnector_Init_EmptyUsername_Error(t *testing.T) {
	c := imapconn.New(nil, nil, nil, zerolog.Nop(), false)
	err := c.Init(context.Background(), plugin.ConnectorConfig{
		Settings: map[string]string{
			"host": "imap.example.com",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username")
}
