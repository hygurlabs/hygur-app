// Package files implements the plugin.Connector interface for local file system sources.
// It wraps the ingest.Ingestor to index local files and folders into the knowledge base.
package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
)

// Compile-time assertions: FilesConnector must satisfy all required plugin interfaces.
var (
	_ plugin.Connector = (*FilesConnector)(nil)
	_ plugin.Lister    = (*FilesConnector)(nil)
	_ plugin.Syncer    = (*FilesConnector)(nil)
	_ plugin.Indexer   = (*FilesConnector)(nil)
)

// defaultExtensions is the list of file extensions indexed when none is configured.
const defaultExtensions = ".md,.txt,.pdf,.docx,.png,.jpg,.jpeg,.heic,.webp,.mp3,.m4a,.wav,.ogg"

// fileSourceTypes lists the source_type values written by the ingestor for the
// extensions handled by this connector. Used to compute ItemCount and to
// enumerate items in List. The legacy "file" type is included for backward
// compatibility with older ingests.
var fileSourceTypes = []string{"markdown", "txt", "pdf", "docx", "image", "audio", "file"}

// ignorePatterns lists directory and file names that are always skipped during walk.
var defaultIgnorePatterns = map[string]bool{
	".git":         true,
	"node_modules": true,
	".ds_store":    true,
}

// FilesConnector adapts the ingest.Ingestor into the plugin.Connector family of interfaces
// to index local files and folders.
type FilesConnector struct {
	ingestor *ingest.Ingestor
	db       *store.DB
	config   plugin.ConnectorConfig
	health   plugin.HealthStatus
	lastSync time.Time
	mu       sync.RWMutex
}

// New creates a new FilesConnector.
func New(ingestor *ingest.Ingestor, db *store.DB) *FilesConnector {
	return &FilesConnector{
		ingestor: ingestor,
		db:       db,
		health: plugin.HealthStatus{
			Status: plugin.StatusUnconfigured,
		},
	}
}

// Info returns the static metadata for the files connector.
func (c *FilesConnector) Info() plugin.ConnectorInfo {
	return plugin.ConnectorInfo{
		ID:          "files",
		Name:        "Local files",
		Description: "Index local files and folders",
		Version:     "1.0.0",
		Icon:        "folder",
		Color:       "#10B981",
		Tags:        []string{"files", "local"},
	}
}

// Capabilities returns the operations supported by this connector.
func (c *FilesConnector) Capabilities() plugin.Capabilities {
	return plugin.Capabilities{
		CanList:      true,
		CanSearch:    false,
		CanSync:      true,
		CanIndex:     true,
		CanSummarize: false,
		CanAttach:    false,
		NeedsAuth:    false,
		AuthType:     plugin.AuthNone,
		Locality:     plugin.LocalityDevice, // reads the local filesystem → edge agent
	}
}

// ConfigSchema returns the configuration schema for UI generation.
func (c *FilesConnector) ConfigSchema() plugin.ConfigSchema {
	return plugin.ConfigSchema{
		Groups: []plugin.ConfigGroup{
			{
				Title: "Source",
				Fields: []plugin.ConfigField{
					{
						Key:         "path",
						Type:        plugin.FieldPath,
						Label:       "Folder to index",
						Description: "Absolute path to a folder. Multiple folders can be provided separated by a newline.",
						Required:    true,
					},
					{
						Key:         "extensions",
						Type:        plugin.FieldString,
						Label:       "Extensions",
						Default:     ".md,.txt,.pdf,.docx,.png,.jpg,.jpeg,.heic,.webp,.mp3,.m4a,.wav,.ogg",
						Description: "Comma-separated list",
					},
					{
						Key:         "recursive",
						Type:        plugin.FieldBool,
						Label:       "Include subfolders",
						Description: "Walk subdirectories recursively",
						Default:     "true",
					},
				},
			},
			{
				Title: "Synchronization",
				Fields: []plugin.ConfigField{
					{
						Key:     "schedule",
						Type:    plugin.FieldCron,
						Label:   "Sync frequency",
						Default: "0 */6 * * *",
					},
				},
			},
		},
	}
}

// Init validates the configuration and sets the connector health.
// It returns an error if no valid path is configured. Multiple folders may be
// configured, separated by newlines; each one is validated independently.
func (c *FilesConnector) Init(_ context.Context, cfg plugin.ConnectorConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.config = cfg

	paths := parsePaths(cfg.Settings["path"])
	if len(paths) == 0 {
		c.health = plugin.HealthStatus{
			Status:  plugin.StatusUnconfigured,
			Message: "'path' setting is required",
		}
		return errors.New("invalid configuration: 'path' setting is required")
	}

	for _, path := range paths {
		if !filepath.IsAbs(path) {
			c.health = plugin.HealthStatus{
				Status:  plugin.StatusUnconfigured,
				Message: fmt.Sprintf("path must be absolute: %q", path),
			}
			return fmt.Errorf("invalid configuration: path must be absolute: %q", path)
		}

		info, err := os.Stat(path)
		if err != nil {
			c.health = plugin.HealthStatus{
				Status:  plugin.StatusUnconfigured,
				Message: fmt.Sprintf("configured folder is not accessible: %v", err),
			}
			return fmt.Errorf("invalid configuration: configured folder is not accessible: %w", err)
		}

		if !info.IsDir() {
			c.health = plugin.HealthStatus{
				Status:  plugin.StatusUnconfigured,
				Message: fmt.Sprintf("path %q is not a directory", path),
			}
			return fmt.Errorf("invalid configuration: path %q is not a directory", path)
		}
	}

	c.health = plugin.HealthStatus{
		Status: plugin.StatusHealthy,
	}
	return nil
}

// parsePaths splits a multi-line path setting into a clean list of absolute
// paths. Empty lines and whitespace-only entries are dropped.
func parsePaths(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Start counts already-indexed files and updates the item count in health.
// Returns an error if the database is not available.
func (c *FilesConnector) Start(ctx context.Context) error {
	if c.db == nil {
		c.mu.Lock()
		c.health.Status = plugin.StatusDegraded
		c.health.Message = "database not available"
		c.mu.Unlock()
		return fmt.Errorf("database not available")
	}

	// Count how many file-family items are already indexed across the source
	// types written by the ingestor (markdown, txt, pdf, docx) plus the legacy
	// "file" type. A failure here is non-fatal — the count just stays 0 until
	// the next Sync refreshes it.
	if count, err := c.db.CountKnowledgeItemsBySourceTypes(ctx, fileSourceTypes); err == nil {
		c.mu.Lock()
		c.health.ItemCount = int64(count)
		c.mu.Unlock()
	}

	return nil
}

// Stop is a no-op; no persistent connections are held by this connector.
func (c *FilesConnector) Stop(_ context.Context) error {
	return nil
}

// Health returns the current health status without performing IO.
func (c *FilesConnector) Health() plugin.HealthStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// Sync walks each configured folder, ingests files matching the configured
// extensions, and returns a combined SyncResult.
//
// When opts.Full is false only files modified after the last successful sync
// are processed. When opts.Limit > 0 the sync stops after that many files
// have been processed (indexed + skipped but not errored).
//
// The lastSync watermark is only advanced when the walk completes without a
// fatal error, so a transient permissions issue will not silently cause later
// syncs to skip freshly-modified files.
func (c *FilesConnector) Sync(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	c.mu.RLock()
	rawPaths := c.config.Settings["path"]
	extRaw := c.config.Settings["extensions"]
	lastSync := c.lastSync
	c.mu.RUnlock()

	paths := parsePaths(rawPaths)
	if len(paths) == 0 {
		return nil, errors.New("sync: connector is not configured (missing path)")
	}

	allowedExts := parseExtensions(extRaw)
	start := time.Now()

	var indexed, skipped, errs int
	fatal := false

	for _, rootPath := range paths {
		if ctx.Err() != nil {
			break
		}

		walkErr := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}

			name := strings.ToLower(info.Name())
			if defaultIgnorePatterns[name] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if info.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if !allowedExts[ext] {
				skipped++
				return nil
			}

			if !opts.Full && !lastSync.IsZero() && !info.ModTime().After(lastSync) {
				skipped++
				return nil
			}

			if opts.Limit > 0 && indexed >= opts.Limit {
				return filepath.SkipAll
			}

			_, ingestErr := c.ingestor.Ingest(ctx, path, ingest.IngestOptions{})
			if ingestErr != nil {
				errs++
				return nil
			}

			indexed++
			return nil
		})

		if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) && !errors.Is(walkErr, filepath.SkipAll) {
			errs++
			fatal = true
		}
	}

	c.mu.Lock()
	if !fatal && ctx.Err() == nil {
		now := time.Now()
		c.lastSync = now
		c.health.LastSync = now
	}
	if c.db != nil {
		// The Sync ctx may already be cancelled (e.g. user-initiated abort);
		// fall back to a fresh background ctx so the post-sync count refresh
		// still runs. A short timeout keeps Sync responsive.
		countCtx := ctx
		if countCtx.Err() != nil {
			var cancel context.CancelFunc
			countCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
		}
		if count, err := c.db.CountKnowledgeItemsBySourceTypes(countCtx, fileSourceTypes); err == nil {
			c.health.ItemCount = int64(count)
		}
	}
	c.mu.Unlock()

	return &plugin.SyncResult{
		Processed: indexed,
		Skipped:   skipped,
		Failed:    errs,
		Duration:  time.Since(start),
	}, nil
}

// Index ingests a single file identified by its absolute path.
// Returns nil when the file is already indexed (status == "duplicate").
func (c *FilesConnector) Index(ctx context.Context, itemID string) error {
	if !filepath.IsAbs(itemID) {
		return fmt.Errorf("index: path must be absolute: %q", itemID)
	}

	if _, err := os.Stat(itemID); err != nil {
		return fmt.Errorf("index: inaccessible file %q: %w", itemID, err)
	}

	result, err := c.ingestor.Ingest(ctx, itemID, ingest.IngestOptions{})
	if err != nil {
		return fmt.Errorf("index: ingestion failed for %q: %w", itemID, err)
	}

	// "duplicate" is not an error condition.
	_ = result
	return nil
}

// IndexBatch calls Index for each path in itemIDs and accumulates results.
// It continues on partial errors and reports them in the returned IndexResult.
func (c *FilesConnector) IndexBatch(ctx context.Context, itemIDs []string) (*plugin.IndexResult, error) {
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

// List returns knowledge items stored with source types that originate from local files.
// It queries the store using the provided ListOptions for pagination.
func (c *FilesConnector) List(ctx context.Context, opts plugin.ListOptions) ([]plugin.Item, error) {
	if c.db == nil {
		return nil, errors.New("list: store unavailable")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}

	// The ingestor stores files with source types: markdown, txt, pdf, docx.
	// We iterate over each known type. A future improvement could add a
	// connector_id column to the knowledge_items table.
	seen := make(map[string]bool)
	var items []plugin.Item

	for _, st := range fileSourceTypes {
		rows, err := c.db.ListKnowledgeItemsBySourceType(ctx, st, limit, opts.Offset)
		if err != nil {
			continue
		}
		for _, row := range rows {
			if seen[row.ContentID] {
				continue
			}
			seen[row.ContentID] = true

			item := plugin.Item{
				ID:          row.ContentID,
				ConnectorID: "files",
				SourceType:  row.SourceType,
				Title:       row.Title,
				Content:     row.NormalizedText,
				Metadata:    row.Metadata,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
			}
			if row.SourcePath != nil {
				item.URL = *row.SourcePath
			}
			items = append(items, item)
		}
	}

	return items, nil
}

// parseExtensions converts a comma-separated extension string into a lookup map.
// If the input is empty the default set is used.
func parseExtensions(raw string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		raw = defaultExtensions
	}

	result := make(map[string]bool)
	for _, ext := range strings.Split(raw, ",") {
		ext = strings.TrimSpace(strings.ToLower(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		result[ext] = true
	}
	return result
}
