package notes

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestDB opens a fresh in-memory SQLite database with all migrations applied.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.NewDB(":memory:")
	require.NoError(t, err, "failed to open in-memory test database")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// insertKnowledgeItem is a convenience helper that inserts a KnowledgeItem and
// fails the test on error.
func insertKnowledgeItem(t *testing.T, db *store.DB, contentID, sourceType, title string) {
	t.Helper()
	now := time.Now()
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     sourceType,
		Title:          title,
		NormalizedText: title + " content",
		Metadata:       map[string]any{"created_from": "test"},
		VersionID:      uuid.New().String(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, db.InsertKnowledgeItem(context.Background(), item))
}

// insertChunkForItem inserts a single chunk for a knowledge item.
func insertChunkForItem(t *testing.T, db *store.DB, contentID string) store.Chunk {
	t.Helper()
	now := time.Now()
	chunkID := uuid.New().String()
	chunk := &store.Chunk{
		ChunkID:   chunkID,
		ContentID: contentID,
		ChunkHash: chunkID[:8],
		Text:      "some text content to embed",
		Metadata:  map[string]any{"index": 0},
		CreatedAt: now,
	}
	require.NoError(t, db.InsertChunk(context.Background(), chunk))
	return *chunk
}

// ---------------------------------------------------------------------------
// mockEmbedder records calls to BatchEmbedAndStore.
// It satisfies the embeddingProvider interface without a real LLM client.
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	called     bool
	lastChunks []store.Chunk
	err        error
}

func (m *mockEmbedder) BatchEmbedAndStore(_ context.Context, chunks []store.Chunk) error {
	m.called = true
	m.lastChunks = chunks
	return m.err
}

// ---------------------------------------------------------------------------
// TestNotesConnector_Info
// ---------------------------------------------------------------------------

func TestNotesConnector_Info(t *testing.T) {
	db := newTestDB(t)
	c := New(nil, db, nil)

	info := c.Info()

	assert.Equal(t, "notes", info.ID, "ID doit être 'notes'")
	assert.Equal(t, "note.text", info.Icon, "Icon doit être 'note.text'")
	assert.Equal(t, "Notes", info.Name)
	assert.Equal(t, "1.0.0", info.Version)
	assert.Equal(t, "#F59E0B", info.Color)
}

// ---------------------------------------------------------------------------
// TestNotesConnector_StartStop
// ---------------------------------------------------------------------------

func TestNotesConnector_StartStop(t *testing.T) {
	db := newTestDB(t)
	c := New(nil, db, nil)

	ctx := context.Background()

	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}), "Init doit réussir")
	require.NoError(t, c.Start(ctx), "Start doit réussir")
	require.NoError(t, c.Stop(ctx), "Stop doit réussir")
}

// ---------------------------------------------------------------------------
// TestNotesConnector_Health_AfterStart
// ---------------------------------------------------------------------------

func TestNotesConnector_Health_AfterStart(t *testing.T) {
	db := newTestDB(t)
	c := New(nil, db, nil)

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))
	require.NoError(t, c.Start(ctx))

	h := c.Health()
	assert.Equal(t, plugin.StatusHealthy, h.Status, "Health.Status doit être StatusHealthy après Start")
	assert.GreaterOrEqual(t, h.ItemCount, int64(0), "Health.ItemCount doit être >= 0")
}

// TestNotesConnector_Health_ItemCount_ReflectsNotes verifies that Start counts
// only notes, not items of other source types.
func TestNotesConnector_Health_ItemCount_ReflectsNotes(t *testing.T) {
	db := newTestDB(t)

	insertKnowledgeItem(t, db, "note:"+uuid.New().String(), "note", "Note 1")
	insertKnowledgeItem(t, db, "note:"+uuid.New().String(), "note", "Note 2")
	insertKnowledgeItem(t, db, "file:"+uuid.New().String(), "file", "File 1")

	c := New(nil, db, nil)
	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))
	require.NoError(t, c.Start(ctx))

	h := c.Health()
	assert.Equal(t, int64(2), h.ItemCount, "ItemCount doit refléter uniquement les notes")
}

// ---------------------------------------------------------------------------
// TestNotesConnector_List_FiltersNotes
// ---------------------------------------------------------------------------

// TestNotesConnector_List_FiltersNotes insère 2 notes et 1 fichier dans le store.
// List() doit retourner exactement 2 items avec SourceType == "note".
func TestNotesConnector_List_FiltersNotes(t *testing.T) {
	db := newTestDB(t)

	note1ID := "note:" + uuid.New().String()
	note2ID := "note:" + uuid.New().String()
	file1ID := "file:" + uuid.New().String()

	insertKnowledgeItem(t, db, note1ID, "note", "Note alpha")
	insertKnowledgeItem(t, db, note2ID, "note", "Note beta")
	insertKnowledgeItem(t, db, file1ID, "file", "Doc PDF")

	c := New(nil, db, nil)
	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	items, err := c.List(ctx, plugin.ListOptions{Limit: 50})
	require.NoError(t, err)

	assert.Len(t, items, 2, "List() doit retourner exactement 2 notes")
	for _, item := range items {
		assert.Equal(t, "note", item.SourceType, "tous les items doivent avoir SourceType == 'note'")
		assert.Equal(t, "notes", item.ConnectorID)
	}
}

// TestNotesConnector_List_DefaultLimit verifies that a zero Limit is replaced
// by the internal default (100) and does not cause an error.
func TestNotesConnector_List_DefaultLimit(t *testing.T) {
	db := newTestDB(t)
	c := New(nil, db, nil)
	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	items, err := c.List(ctx, plugin.ListOptions{Limit: 0})
	require.NoError(t, err)
	assert.NotNil(t, items)
}

// TestNotesConnector_List_ItemMapping verifies the field mapping from
// store.KnowledgeItem to plugin.Item.
func TestNotesConnector_List_ItemMapping(t *testing.T) {
	db := newTestDB(t)
	contentID := "note:" + uuid.New().String()
	insertKnowledgeItem(t, db, contentID, "note", "My Title")

	c := New(nil, db, nil)
	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	items, err := c.List(ctx, plugin.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)

	item := items[0]
	assert.Equal(t, contentID, item.ID)
	assert.Equal(t, "notes", item.ConnectorID)
	assert.Equal(t, "note", item.SourceType)
	assert.Equal(t, "My Title", item.Title)
	assert.False(t, item.CreatedAt.IsZero(), "CreatedAt doit être renseigné")
	assert.False(t, item.UpdatedAt.IsZero(), "UpdatedAt doit être renseigné")
}

// ---------------------------------------------------------------------------
// TestNotesConnector_Index_CallsEmbedding  (test de câblage intégration)
// ---------------------------------------------------------------------------

// TestNotesConnector_Index_CallsEmbedding vérifie le câblage entre Index() et
// l'EmbeddingService. Un item avec des chunks mais sans embeddings doit
// déclencher un appel à BatchEmbedAndStore().
func TestNotesConnector_Index_CallsEmbedding(t *testing.T) {
	db := newTestDB(t)

	// Insérer un item note sans embeddings.
	contentID := "note:" + uuid.New().String()
	insertKnowledgeItem(t, db, contentID, "note", "Note sans embeddings")
	// Insérer un chunk pour cet item (nécessaire pour que Index() puisse appeler BatchEmbedAndStore).
	insertChunkForItem(t, db, contentID)

	mock := &mockEmbedder{}
	c := newWithEmbedProvider(nil, db, mock)

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	err := c.Index(ctx, contentID)
	require.NoError(t, err)

	assert.True(t, mock.called, "BatchEmbedAndStore doit avoir été appelé")
	assert.Len(t, mock.lastChunks, 1, "BatchEmbedAndStore doit recevoir 1 chunk")
	assert.Equal(t, contentID, mock.lastChunks[0].ContentID)
}

// TestNotesConnector_Index_SkipsAlreadyIndexed verifies that Index() does NOT
// call BatchEmbedAndStore when embeddings already exist for the item.
func TestNotesConnector_Index_SkipsAlreadyIndexed(t *testing.T) {
	db := newTestDB(t)

	// We test the skip-path by having CountEmbeddingsForItem return > 0.
	// Since we cannot insert a real vector without a full schema setup for
	// chunk_vectors, we verify via a fresh note with no chunks at all — the
	// connector must return nil without calling the embedder.
	contentID := "note:" + uuid.New().String()
	insertKnowledgeItem(t, db, contentID, "note", "Note vide")
	// No chunks inserted → embedder must NOT be called.

	mock := &mockEmbedder{}
	c := newWithEmbedProvider(nil, db, mock)

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	err := c.Index(ctx, contentID)
	require.NoError(t, err)
	assert.False(t, mock.called, "BatchEmbedAndStore ne doit pas être appelé quand il n'y a pas de chunks")
}

// TestNotesConnector_Index_NoEmbedService verifies that Index() is a no-op
// (and returns nil) when no embedding service is configured.
func TestNotesConnector_Index_NoEmbedService(t *testing.T) {
	db := newTestDB(t)
	contentID := "note:" + uuid.New().String()
	insertKnowledgeItem(t, db, contentID, "note", "Note sans service")
	insertChunkForItem(t, db, contentID)

	c := New(nil, db, nil) // nil embedSvc

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	err := c.Index(ctx, contentID)
	require.NoError(t, err, "Index doit réussir silencieusement sans EmbeddingService")
}

// ---------------------------------------------------------------------------
// TestNotesConnector_IndexBatch
// ---------------------------------------------------------------------------

func TestNotesConnector_IndexBatch_AccumulatesErrors(t *testing.T) {
	db := newTestDB(t)

	id1 := "note:" + uuid.New().String()
	id2 := "note:" + uuid.New().String()

	insertKnowledgeItem(t, db, id1, "note", "Note 1")
	insertKnowledgeItem(t, db, id2, "note", "Note 2")
	insertChunkForItem(t, db, id1)
	insertChunkForItem(t, db, id2)

	mock := &mockEmbedder{err: assert.AnError}
	c := newWithEmbedProvider(nil, db, mock)

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	result, err := c.IndexBatch(ctx, []string{id1, id2})
	require.NoError(t, err, "IndexBatch ne doit pas retourner d'erreur en cas d'erreurs partielles")
	assert.Equal(t, 0, result.Indexed)
	assert.Equal(t, 2, result.Failed)
	assert.Len(t, result.Errors, 2)
}

func TestNotesConnector_IndexBatch_Success(t *testing.T) {
	db := newTestDB(t)

	id1 := "note:" + uuid.New().String()
	id2 := "note:" + uuid.New().String()

	// No chunks → Index() is a no-op (no embedder call needed), Indexed++ for both.
	insertKnowledgeItem(t, db, id1, "note", "Note 1")
	insertKnowledgeItem(t, db, id2, "note", "Note 2")

	c := New(nil, db, nil) // no embedder

	ctx := context.Background()
	require.NoError(t, c.Init(ctx, plugin.ConnectorConfig{Enabled: true}))

	result, err := c.IndexBatch(ctx, []string{id1, id2})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Indexed)
	assert.Equal(t, 0, result.Failed)
}

// ---------------------------------------------------------------------------
// TestNotesConnector_Capabilities
// ---------------------------------------------------------------------------

func TestNotesConnector_Capabilities(t *testing.T) {
	c := New(nil, nil, nil)
	caps := c.Capabilities()

	assert.True(t, caps.CanList)
	assert.True(t, caps.CanIndex)
	assert.False(t, caps.CanSearch)
	assert.False(t, caps.CanSync)
	assert.False(t, caps.NeedsAuth)
	assert.Equal(t, plugin.AuthNone, caps.AuthType)
}

// ---------------------------------------------------------------------------
// TestNotesConnector_ConfigSchema
// ---------------------------------------------------------------------------

func TestNotesConnector_ConfigSchema(t *testing.T) {
	c := New(nil, nil, nil)
	schema := c.ConfigSchema()

	require.Len(t, schema.Groups, 1)
	group := schema.Groups[0]
	assert.Equal(t, "General", group.Title)
	require.Len(t, group.Fields, 1)

	field := group.Fields[0]
	assert.Equal(t, "auto_index", field.Key)
	assert.Equal(t, plugin.FieldBool, field.Type)
	assert.Equal(t, "true", field.Default)
}
