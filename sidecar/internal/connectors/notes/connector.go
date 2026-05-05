// Package notes implements the plugin.Connector interface for local notes.
// It wraps the CreateNoteTool and the knowledge store to expose note management
// through the unified plugin.Connector, plugin.Lister, and plugin.Indexer interfaces.
package notes

import (
	"context"
	"fmt"
	"sync"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
)

// embeddingProvider is a narrow interface over llm.EmbeddingService that the
// connector depends on. Using an interface here makes the connector testable
// with a mock without modifying the llm package.
type embeddingProvider interface {
	BatchEmbedAndStore(ctx context.Context, chunks []store.Chunk) error
}

// Compile-time assertions: NotesConnector must satisfy all declared interfaces.
var (
	_ plugin.Connector              = (*NotesConnector)(nil)
	_ plugin.Lister                 = (*NotesConnector)(nil)
	_ plugin.Indexer                = (*NotesConnector)(nil)
	_ plugin.DefaultEnabledProvider = (*NotesConnector)(nil)
)

// EnabledByDefault returns true so notes — which have no external dependencies
// or required configuration — are active out of the box on a fresh install.
func (c *NotesConnector) EnabledByDefault() bool { return true }

// defaultListLimit is used when ListOptions.Limit is 0.
const defaultListLimit = 100

// NotesConnector adapts the local notes pipeline (CreateNoteTool + store.DB)
// into the plugin.Connector family of interfaces.
type NotesConnector struct {
	createTool *tools.CreateNoteTool
	db         *store.DB
	embedSvc   embeddingProvider
	config     plugin.ConnectorConfig
	health     plugin.HealthStatus
	mu         sync.RWMutex
}

// New creates a new NotesConnector.
// createTool may be nil; it is only required when creating notes via the tool.
// db and embedSvc are required for List and Index operations.
// embedSvc satisfies *llm.EmbeddingService at the call site; the field is typed
// as embeddingProvider to allow test doubles without modifying the llm package.
func New(createTool *tools.CreateNoteTool, db *store.DB, embedSvc *llm.EmbeddingService) *NotesConnector {
	var ep embeddingProvider
	if embedSvc != nil {
		ep = embedSvc
	}
	return &NotesConnector{
		createTool: createTool,
		db:         db,
		embedSvc:   ep,
		health: plugin.HealthStatus{
			Status: plugin.StatusUnconfigured,
		},
	}
}

// newWithEmbedProvider is the internal constructor used in tests to inject an
// embeddingProvider mock without needing a real llm.Client.
func newWithEmbedProvider(createTool *tools.CreateNoteTool, db *store.DB, ep embeddingProvider) *NotesConnector {
	return &NotesConnector{
		createTool: createTool,
		db:         db,
		embedSvc:   ep,
		health: plugin.HealthStatus{
			Status: plugin.StatusUnconfigured,
		},
	}
}

// ---------------------------------------------------------------------------
// plugin.Connector — static metadata
// ---------------------------------------------------------------------------

// Info returns the static metadata for this connector.
func (c *NotesConnector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{
		ID:          "notes",
		Name:        "Notes",
		Description: "Local notes with semantic indexing",
		Version:     "1.0.0",
		Icon:        "note.text",
		Color:       "#F59E0B",
		Tags:        []string{"notes", "knowledge"},
	}
}

// Capabilities returns the set of operations this connector supports.
func (c *NotesConnector) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		CanList:      true,
		CanSearch:    false,
		CanSync:      false,
		CanIndex:     true,
		CanSummarize: false,
		CanAttach:    false,
		NeedsAuth:    false,
		AuthType:     plugin.AuthNone,
	}
}

// ConfigSchema returns the dynamic form schema for generating the UI.
// The single "General" group exposes an auto_index boolean option.
func (c *NotesConnector) ConfigSchema() plugin.ConfigSchema {
	return plugin.ConfigSchema{
		Groups: []plugin.ConfigGroup{
			{
				Title: "General",
				Fields: []plugin.ConfigField{
					{
						Key:         "auto_index",
						Type:        plugin.FieldBool,
						Label:       "Auto-index on creation",
						Description: "Automatically index new notes as they are created",
						Default:     "true",
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// plugin.Connector — lifecycle
// ---------------------------------------------------------------------------

// Init stores the configuration and marks the connector as healthy.
// No external connections are required for notes, so health is immediately set
// to StatusHealthy.
func (c *NotesConnector) Init(_ context.Context, cfg plugin.ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.config = cfg
	c.health.Status = plugin.StatusHealthy
	c.health.Message = ""
	return nil
}

// Start counts existing notes and updates health.ItemCount.
func (c *NotesConnector) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Use the source-type filtered query for an accurate notes-only count.
	items, err := c.db.ListKnowledgeItemsBySourceType(ctx, "note", 10_000, 0)
	if err != nil {
		c.health.Status = plugin.StatusDegraded
		c.health.Message = fmt.Sprintf("unable to count notes: %v", err)
		return nil // non-fatal: the connector is still usable
	}

	c.health.Status = plugin.StatusHealthy
	c.health.Message = ""
	c.health.ItemCount = int64(len(items))
	return nil
}

// Stop is a no-op for notes (no persistent connections to close).
func (c *NotesConnector) Stop(_ context.Context) error {
	return nil
}

// Health returns the current health status without performing any IO.
func (c *NotesConnector) Health() plugin.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// ---------------------------------------------------------------------------
// plugin.Lister
// ---------------------------------------------------------------------------

// List returns paginated notes from the knowledge store as plugin.Item values.
// Only items with source_type == "note" are returned. If opts.Limit is 0 the
// default of 100 is applied.
func (c *NotesConnector) List(ctx context.Context, opts plugin.ListOptions) ([]plugin.Item, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = defaultListLimit
	}

	rawItems, err := c.db.ListKnowledgeItemsBySourceType(ctx, "note", limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("notes connector List: %w", err)
	}

	items := make([]plugin.Item, 0, len(rawItems))
	for _, ki := range rawItems {
		items = append(items, knowledgeItemToPluginItem(ki))
	}
	return items, nil
}

// knowledgeItemToPluginItem converts a store.KnowledgeItem to a plugin.Item.
func knowledgeItemToPluginItem(ki *store.KnowledgeItem) plugin.Item {
	return plugin.Item{
		ID:          ki.ContentID,
		ConnectorID: "notes",
		SourceType:  "note",
		Title:       ki.Title,
		Content:     ki.NormalizedText,
		CreatedAt:   ki.CreatedAt,
		UpdatedAt:   ki.UpdatedAt,
	}
}

// ---------------------------------------------------------------------------
// plugin.Indexer
// ---------------------------------------------------------------------------

// Index ensures that the note identified by itemID has chunks and embeddings in
// the store. If embeddings are already present the call is a no-op.
// If the embedding service is unavailable, the note is still considered indexed
// (FTS remains functional) and no error is returned.
func (c *NotesConnector) Index(ctx context.Context, itemID string) error {
	if c.embedSvc == nil {
		// No embedding service configured — nothing to do.
		return nil
	}

	// Check whether embeddings already exist for this item.
	embCount, err := c.db.CountEmbeddingsForItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("notes connector Index: CountEmbeddingsForItem %q: %w", itemID, err)
	}
	if embCount > 0 {
		// Already indexed — skip.
		return nil
	}

	// Retrieve the chunks that need embedding.
	chunks, err := c.db.GetChunksByContentID(ctx, itemID)
	if err != nil {
		return fmt.Errorf("notes connector Index: GetChunksByContentID %q: %w", itemID, err)
	}
	if len(chunks) == 0 {
		// No chunks to embed (item may have empty content or not yet chunked).
		return nil
	}

	// Convert to []store.Chunk (GetChunksByContentID returns []*store.Chunk).
	storeChunks := make([]store.Chunk, 0, len(chunks))
	for _, ch := range chunks {
		storeChunks = append(storeChunks, *ch)
	}

	if err := c.embedSvc.BatchEmbedAndStore(ctx, storeChunks); err != nil {
		return fmt.Errorf("notes connector Index: BatchEmbedAndStore %q: %w", itemID, err)
	}

	return nil
}

// IndexBatch indexes multiple notes; it continues on partial errors and
// aggregates them in the returned IndexResult.
func (c *NotesConnector) IndexBatch(ctx context.Context, itemIDs []string) (*plugin.IndexResult, error) {
	result := &plugin.IndexResult{}

	for _, id := range itemIDs {
		if err := c.Index(ctx, id); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, plugin.IndexError{
				ItemID:  id,
				Message: err.Error(),
			})
			continue
		}
		result.Indexed++
	}

	return result, nil
}
