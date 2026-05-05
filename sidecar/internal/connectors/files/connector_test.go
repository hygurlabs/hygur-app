package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
)

// newTestIngestor creates a fully wired Ingestor backed by a SQLite in-memory
// database. It registers all parsers required by the default extension set so
// that integration tests exercise the real ingestion pipeline.
func newTestIngestor(t *testing.T) (*ingest.Ingestor, *store.DB) {
	t.Helper()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("store.NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ing := ingest.NewIngestorWithStore(db)
	ing.RegisterParser(parsers.NewMarkdownParser())
	ing.RegisterParser(parsers.NewTXTParser())

	return ing, db
}

// writeMD creates a minimal Markdown file in dir and returns its path.
func writeMD(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

// initConnector calls Init and Start on c using dir as the watched path.
func initConnector(t *testing.T, c *FilesConnector, dir string) {
	t.Helper()
	cfg := plugin.ConnectorConfig{
		Settings: map[string]string{"path": dir},
	}
	if err := c.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Info
// ---------------------------------------------------------------------------

func TestFilesConnector_Info(t *testing.T) {
	ing, db := newTestIngestor(t)
	c := New(ing, db)

	info := c.Info()

	if info.ID != "files" {
		t.Errorf("Info().ID = %q, want %q", info.ID, "files")
	}
	if info.Icon != "folder" {
		t.Errorf("Info().Icon = %q, want %q", info.Icon, "folder")
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Init_MissingPath
// ---------------------------------------------------------------------------

func TestFilesConnector_Init_MissingPath(t *testing.T) {
	ing, db := newTestIngestor(t)
	c := New(ing, db)

	cfg := plugin.ConnectorConfig{
		Settings: map[string]string{},
	}

	err := c.Init(context.Background(), cfg)
	if err == nil {
		t.Fatal("Init with missing path should return an error")
	}

	if c.health.Status != plugin.StatusUnconfigured {
		t.Errorf("health.Status = %q, want %q", c.health.Status, plugin.StatusUnconfigured)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Sync_IndexesNewFiles
//
// Integration wiring test: the Ingestor must really be called so items land
// in the store. This validates that Init → Start → Sync form a connected pipeline.
// ---------------------------------------------------------------------------

func TestFilesConnector_Sync_IndexesNewFiles(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "a.md", "# Doc A\nContent of document A.")
	writeMD(t, dir, "b.md", "# Doc B\nContent of document B.")
	writeMD(t, dir, "c.md", "# Doc C\nContent of document C.")

	ing, db := newTestIngestor(t)
	c := New(ing, db)
	initConnector(t, c, dir)

	result, err := c.Sync(context.Background(), plugin.SyncOptions{Full: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if result.Processed < 3 {
		t.Errorf("Sync().Processed = %d, want >= 3", result.Processed)
	}

	// Wiring validation: confirm items are persisted in the store.
	items, err := db.ListKnowledgeItems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("ListKnowledgeItems: %v", err)
	}
	if len(items) < 3 {
		t.Errorf("store contains %d items after Sync, want >= 3", len(items))
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_ItemCount_ReflectsIndexedFiles
//
// Regression: Start() and Sync() were querying source_type="file" but the
// ingestor stores .md as "markdown", .txt as "txt", etc., so the count was
// always 0 even after a successful sync — making the UI show 0 indexed
// items. The fix counts across the file-bucket source_types.
// ---------------------------------------------------------------------------

func TestFilesConnector_ItemCount_ReflectsIndexedFiles(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "a.md", "# Doc A\nContent A.")
	writeMD(t, dir, "b.md", "# Doc B\nContent B.")

	ing, db := newTestIngestor(t)
	c := New(ing, db)
	initConnector(t, c, dir)

	// Right after Init/Start with an empty DB, count is 0.
	if got := c.Health().ItemCount; got != 0 {
		t.Errorf("ItemCount before Sync = %d, want 0", got)
	}

	if _, err := c.Sync(context.Background(), plugin.SyncOptions{Full: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// After Sync, ItemCount must reflect the 2 indexed markdown files.
	if got := c.Health().ItemCount; got != 2 {
		t.Errorf("ItemCount after Sync = %d, want 2", got)
	}

	// A fresh Start on the same DB must also pick up the existing items.
	c2 := New(ing, db)
	initConnector(t, c2, dir)
	if got := c2.Health().ItemCount; got != 2 {
		t.Errorf("ItemCount after restart = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Sync_SkipsOldFiles
// ---------------------------------------------------------------------------

func TestFilesConnector_Sync_SkipsOldFiles(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "old.md", "# Old\nThis file will not be re-indexed.")

	ing, db := newTestIngestor(t)
	c := New(ing, db)
	initConnector(t, c, dir)

	// First sync to establish the watermark.
	if _, err := c.Sync(context.Background(), plugin.SyncOptions{Full: true}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Move the watermark to now so the existing file appears old.
	c.mu.Lock()
	c.lastSync = time.Now()
	c.mu.Unlock()

	// Second sync with the watermark in the future: no new files.
	result, err := c.Sync(context.Background(), plugin.SyncOptions{Full: false})
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if result.Processed != 0 {
		t.Errorf("second Sync().Processed = %d, want 0 (all files older than lastSync)", result.Processed)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Sync_RespectsExtensions
// ---------------------------------------------------------------------------

func TestFilesConnector_Sync_RespectsExtensions(t *testing.T) {
	dir := t.TempDir()
	writeMD(t, dir, "doc.md", "# Document\nIndexable markdown content.")
	// Write a PNG placeholder (not a real PNG, but the extension is what matters).
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte("fake png bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	ing, db := newTestIngestor(t)
	c := New(ing, db)

	cfg := plugin.ConnectorConfig{
		Settings: map[string]string{
			"path":       dir,
			"extensions": ".md",
		},
	}
	if err := c.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}

	result, err := c.Sync(context.Background(), plugin.SyncOptions{Full: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Exactly the .md file should be indexed; the .png should be skipped.
	if result.Processed != 1 {
		t.Errorf("Sync().Processed = %d, want 1 (only .md)", result.Processed)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Sync_RespectsLimit
// ---------------------------------------------------------------------------

func TestFilesConnector_Sync_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeMD(t, dir,
			filepath.Base(filepath.Join(dir, "doc"+string(rune('0'+i))+".md")),
			"# Doc\nContent.",
		)
	}

	ing, db := newTestIngestor(t)
	c := New(ing, db)
	initConnector(t, c, dir)

	result, err := c.Sync(context.Background(), plugin.SyncOptions{Full: true, Limit: 3})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if result.Processed > 3 {
		t.Errorf("Sync().Processed = %d, want <= 3 (limit respected)", result.Processed)
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Index_AbsolutePath
// ---------------------------------------------------------------------------

func TestFilesConnector_Index_AbsolutePath(t *testing.T) {
	ing, db := newTestIngestor(t)
	c := New(ing, db)

	err := c.Index(context.Background(), "relative/path/file.md")
	if err == nil {
		t.Fatal("Index with relative path should return an error")
	}
}

// ---------------------------------------------------------------------------
// TestFilesConnector_Index_ExistingFile
// ---------------------------------------------------------------------------

func TestFilesConnector_Index_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := writeMD(t, dir, "single.md", "# Title\nSome content here.")

	ing, db := newTestIngestor(t)
	c := New(ing, db)

	if err := c.Index(context.Background(), path); err != nil {
		t.Errorf("Index on valid file returned error: %v", err)
	}

	// Wiring: item must be in the store.
	items, err := db.ListKnowledgeItems(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("ListKnowledgeItems: %v", err)
	}
	if len(items) == 0 {
		t.Error("store is empty after Index — Ingestor was not called or not wired")
	}
}
