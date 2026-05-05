// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"database/sql"
	"fmt"
)

// Migration represents a database migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// migrations contains all available migrations in order.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQL:     schemaSQL,
	},
	// Migration 2 originally created FTS5 tables; dropped by migration 5. No-op on fresh DBs.
	{
		Version: 2,
		Name:    "fts5_and_vectors",
		SQL:     `SELECT 1;`,
	},
	// Migration 3 is now a no-op because the 'tags' column was added to the initial schema.
	// We keep the migration entry for version tracking but with an empty SQL statement.
	{
		Version: 3,
		Name:    "add_project_tags",
		SQL:     `SELECT 1;`, // No-op: tags column now exists in initial schema
	},
	// Migration 4 adds the tags and item_tags tables for tag management.
	{
		Version: 4,
		Name:    "add_tags_tables",
		SQL: `
-- tags stores user-defined and auto-generated tags
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color TEXT NOT NULL DEFAULT '#3B82F6',
    auto_rule TEXT,
    is_auto BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
CREATE INDEX IF NOT EXISTS idx_tags_is_auto ON tags(is_auto);

-- item_tags links knowledge_items to tags (many-to-many)
CREATE TABLE IF NOT EXISTS item_tags (
    content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_item_tags_content_id ON item_tags(content_id);
CREATE INDEX IF NOT EXISTS idx_item_tags_tag_id ON item_tags(tag_id);
`,
	},
	// Migration 5 removes FTS5 virtual tables and their sync triggers.
	// All search is now vector/semantic only.
	{
		Version: 5,
		Name:    "drop_fts_and_unify",
		SQL: `
DROP TRIGGER IF EXISTS chunks_ai;
DROP TRIGGER IF EXISTS chunks_ad;
DROP TRIGGER IF EXISTS chunks_au;
DROP TRIGGER IF EXISTS knowledge_ai;
DROP TRIGGER IF EXISTS knowledge_ad;
DROP TRIGGER IF EXISTS knowledge_au;
DROP TABLE IF EXISTS chunks_fts;
DROP TABLE IF EXISTS knowledge_fts;
`,
	},
	// Migration 6 ensures the memories table exists on installs that pre-date
	// its addition to schemaSQL. CREATE TABLE IF NOT EXISTS is a no-op on
	// fresh DBs (where v1 already created it).
	{
		Version: 6,
		Name:    "ensure_memories_table",
		SQL: `
CREATE TABLE IF NOT EXISTS memories (
    memory_id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    context_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    score FLOAT
);
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type);
CREATE INDEX IF NOT EXISTS idx_memories_context_id ON memories(context_id);
`,
	},
}

// applyMigrations applies all pending migrations to the database.
func applyMigrations(db *sql.DB) error {
	// Ensure schema_version table exists
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_version table: %w", err)
	}

	// Get current version
	var currentVersion int
	err = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	// Apply pending migrations
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %d: %w", m.Version, err)
		}

		_, err = tx.Exec(m.SQL)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
		}

		_, err = tx.Exec("INSERT INTO schema_version (version) VALUES (?)", m.Version)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", m.Version, err)
		}
	}

	return nil
}

// GetSchemaVersion returns the current schema version from the database.
func (d *DB) GetSchemaVersion() (int, error) {
	var version int
	err := d.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	return version, nil
}
