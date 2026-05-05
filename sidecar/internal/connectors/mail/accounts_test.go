package mail_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hygur/sidecar/internal/auth"
	mailconn "github.com/hygur/sidecar/internal/connectors/mail"
	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/diag"
	"github.com/hygur/sidecar/internal/plugin"
)

// fakeAccountConn is a stand-in MailConnector that lets us drive
// connect/verify behaviour without a real provider.
type fakeAccountConn struct {
	connected  bool
	connectErr error
	verifyErr  error
}

func (f *fakeAccountConn) Connect(_ context.Context) error {
	if f.connectErr != nil {
		return f.connectErr
	}
	f.connected = true
	return nil
}
func (f *fakeAccountConn) Disconnect() error { f.connected = false; return nil }
func (f *fakeAccountConn) IsConnected() bool { return f.connected }
func (f *fakeAccountConn) ListThreads(context.Context, mailpkg.ListOptions) ([]mailpkg.Thread, error) {
	return nil, nil
}
func (f *fakeAccountConn) GetThread(context.Context, string) (*mailpkg.Thread, error) {
	return nil, nil
}
func (f *fakeAccountConn) GetMessages(context.Context, string) ([]mailpkg.Message, error) {
	return nil, nil
}
func (f *fakeAccountConn) GetMessagesByThread(context.Context, *mailpkg.Thread) ([]mailpkg.Message, error) {
	return nil, nil
}

// Verify is the optional liveVerify hook detected by the registry.
func (f *fakeAccountConn) Verify(context.Context) error { return f.verifyErr }

func TestAccountRegistry_RegisterAndGet(t *testing.T) {
	r := mailconn.NewAccountRegistry()

	r.Register(&mailconn.AccountSession{AccountID: "alice@gmail.com", Provider: "gmail", Email: "alice@gmail.com"})
	r.Register(&mailconn.AccountSession{AccountID: "bob@proton.me", Provider: "proton", Email: "bob@proton.me"})

	got, err := r.Get("alice@gmail.com")
	require.NoError(t, err)
	assert.Equal(t, "gmail", got.Provider)

	all := r.All()
	assert.Len(t, all, 2)

	r.Unregister("alice@gmail.com")
	_, err = r.Get("alice@gmail.com")
	assert.ErrorIs(t, err, mailconn.ErrAccountNotFound)
}

func TestAccountRegistry_VerifyAccount_Healthy(t *testing.T) {
	r := mailconn.NewAccountRegistry()
	conn := &fakeAccountConn{}
	r.Register(&mailconn.AccountSession{
		AccountID: "alice@gmail.com",
		Provider:  "gmail",
		Conn:      conn,
		Health:    plugin.HealthStatus{Status: plugin.StatusUnconfigured},
	})

	sess, err := r.VerifyAccount(context.Background(), "alice@gmail.com")
	require.NoError(t, err)
	assert.Equal(t, plugin.StatusHealthy, sess.Health.Status)
	assert.Equal(t, diag.ReasonHealthy, sess.BriefReason)
	assert.WithinDuration(t, time.Now(), sess.LastVerify, 2*time.Second)
}

func TestAccountRegistry_VerifyAccount_AuthError(t *testing.T) {
	r := mailconn.NewAccountRegistry()
	conn := &fakeAccountConn{connectErr: mailpkg.ErrAuthFailed}
	r.Register(&mailconn.AccountSession{
		AccountID: "alice@gmail.com",
		Provider:  "gmail",
		Conn:      conn,
	})

	sess, err := r.VerifyAccount(context.Background(), "alice@gmail.com")
	require.NoError(t, err) // VerifyAccount always returns nil — error reflected in BriefReason.
	assert.Equal(t, diag.ReasonAuthIssue, sess.BriefReason)
	assert.Equal(t, plugin.StatusUnhealthy, sess.Health.Status)
}

func TestAccountRegistry_VerifyAccount_Cache30s(t *testing.T) {
	r := mailconn.NewAccountRegistry()
	conn := &fakeAccountConn{}
	r.Register(&mailconn.AccountSession{
		AccountID: "alice@gmail.com",
		Provider:  "gmail",
		Conn:      conn,
	})

	// First call hits Connect.
	_, err := r.VerifyAccount(context.Background(), "alice@gmail.com")
	require.NoError(t, err)
	require.True(t, conn.connected)

	// Force a state where another verify would fail — but cache must skip it.
	conn.verifyErr = errors.New("would fail if called")
	conn.connected = true // already connected → liveVerify branches into Verify()
	sess, err := r.VerifyAccount(context.Background(), "alice@gmail.com")
	require.NoError(t, err)
	assert.Equal(t, diag.ReasonHealthy, sess.BriefReason, "cached result should still be healthy")
}

func TestAccountRegistry_VerifyAccount_NotFound(t *testing.T) {
	r := mailconn.NewAccountRegistry()
	_, err := r.VerifyAccount(context.Background(), "ghost@nowhere.com")
	assert.ErrorIs(t, err, mailconn.ErrAccountNotFound)
}

func TestAccountRegistry_Snapshot_NoSecrets(t *testing.T) {
	r := mailconn.NewAccountRegistry()
	r.Register(&mailconn.AccountSession{
		AccountID:   "alice@gmail.com",
		Provider:    "gmail",
		Email:       "alice@gmail.com",
		Health:      plugin.HealthStatus{Status: plugin.StatusHealthy, LastSync: time.Now()},
		BriefReason: diag.ReasonHealthy,
		LastVerify:  time.Now(),
	})

	snap := r.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, "alice@gmail.com", snap[0].AccountID)
	assert.Equal(t, "gmail", snap[0].Provider)
	assert.Equal(t, string(diag.ReasonHealthy), snap[0].BriefReason)
}

func TestMailConnector_LoadAccountsFromCredStore(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "load-from-store-test")

	store, err := auth.NewCredentialStore(tempDir)
	require.NoError(t, err)

	require.NoError(t, store.SaveMailAccountCredential(auth.MailAccountCredential{
		AccountID:    "alice@gmail.com",
		Provider:     "gmail",
		Email:        "alice@gmail.com",
		RefreshToken: "rt",
		ClientID:     "cid",
		ClientSecret: "secret",
	}))
	require.NoError(t, store.SaveMailAccountCredential(auth.MailAccountCredential{
		AccountID: "bob@proton.me",
		Provider:  "proton",
		Email:     "bob@proton.me",
		Username:  "bob",
		Password:  "pwd",
	}))

	mc := mailconn.New(nil, nil, nil, nil, nil, store, zerolog.Nop())
	count, err := mc.LoadAccountsFromCredStore()
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	snap := mc.Accounts().Snapshot()
	require.Len(t, snap, 2)

	ids := []string{snap[0].AccountID, snap[1].AccountID}
	assert.Contains(t, ids, "alice@gmail.com")
	assert.Contains(t, ids, "bob@proton.me")
}
