package mail_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hygur/sidecar/internal/auth"
	mailconn "github.com/hygur/sidecar/internal/connectors/mail"
	"github.com/hygur/sidecar/internal/store"
)

func TestMigrateLegacyCredentials_Proton(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "migrate-proton")

	credStore, err := auth.NewCredentialStore(tempDir)
	require.NoError(t, err)
	require.NoError(t, credStore.SaveMailCredential("proton", "alice@proton.me", "bridge-pass"))

	db, err := store.NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Insert a legacy-shaped knowledge item.
	require.NoError(t, db.InsertKnowledgeItem(context.Background(), &store.KnowledgeItem{
		ContentID:      "mail:proton:thread-1",
		SourceType:     "email",
		Title:          "legacy",
		NormalizedText: "x",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}))

	res, err := mailconn.MigrateLegacyCredentials(context.Background(), credStore, db.SQLDB(), zerolog.Nop())
	require.NoError(t, err)
	assert.True(t, res.ProtonMigrated)
	assert.Equal(t, "alice@proton.me", res.ProtonAccountID)
	assert.Equal(t, int64(1), res.KnowledgeItemsMoved)

	// New account is registered in the store.
	got, err := credStore.GetMailAccountCredential("alice@proton.me")
	require.NoError(t, err)
	assert.Equal(t, "proton", got.Provider)
	assert.Equal(t, "alice@proton.me", got.Username)

	// Legacy entry preserved (rollback safety).
	username, _, err := credStore.GetMailCredential("proton")
	require.NoError(t, err)
	assert.Equal(t, "alice@proton.me", username)

	// Knowledge item content_id rewritten.
	item, err := db.GetKnowledgeItem(context.Background(), "mail:alice@proton.me:thread-1")
	require.NoError(t, err)
	require.NotNil(t, item, "expected migrated item to exist under new content_id")
	old, _ := db.GetKnowledgeItem(context.Background(), "mail:proton:thread-1")
	assert.Nil(t, old, "expected old content_id to be gone after rewrite")

	// Idempotency: a second call must not re-migrate.
	res2, err := mailconn.MigrateLegacyCredentials(context.Background(), credStore, db.SQLDB(), zerolog.Nop())
	require.NoError(t, err)
	assert.False(t, res2.ProtonMigrated, "second migration should be a no-op")
}

func TestMigrateLegacyCredentials_NoLegacyData(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("HYGUR_CRED_KEY", "migrate-empty")
	credStore, err := auth.NewCredentialStore(tempDir)
	require.NoError(t, err)

	res, err := mailconn.MigrateLegacyCredentials(context.Background(), credStore, nil, zerolog.Nop())
	require.NoError(t, err)
	assert.False(t, res.ProtonMigrated)
	assert.False(t, res.GmailMigrated)
}

func TestMigrateLegacyCredentials_NilStore(t *testing.T) {
	res, err := mailconn.MigrateLegacyCredentials(context.Background(), nil, nil, zerolog.Nop())
	require.NoError(t, err)
	assert.False(t, res.ProtonMigrated)
}
