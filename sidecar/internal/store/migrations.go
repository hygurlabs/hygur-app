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
	// Migration 7 adds Phase 3.3 columns to memories: source, accepted_at,
	// embedding, session_id. Existing rows are back-filled to source='manual'
	// and accepted_at=created_at so prior auto-extracted memories continue to
	// be injected (we can't ask the user to retroactively review them, and
	// silently dropping established memories would surprise long-term users).
	// Fresh extractions made after this migration apply will land with
	// source='extracted' and accepted_at=NULL — i.e. require user review.
	//
	// Note: schemaSQL (v1) already declares these columns on fresh installs.
	// Production DBs that pre-date v7 still need the ALTER TABLEs. We can't
	// use "ADD COLUMN IF NOT EXISTS" (SQLite < 3.35), so the migration is
	// applied through a per-statement runner (applyMemoriesV7Migration) that
	// inspects PRAGMA table_info and only adds missing columns.
	{
		Version: 7,
		Name:    "memories_long_term_columns",
		SQL:     "", // handled by applyMigrations special-case below
	},
	// Migration 8 introduces interaction_log, the append-only signal stream
	// that powers Phase 1 (learning progress bar) and unlocks phases 2-5
	// (recap slot detection, ranking signals, contradiction prioritisation).
	// Idempotent on fresh installs because schemaSQL v1 already declares
	// the table.
	{
		Version: 8,
		Name:    "interaction_log",
		SQL: `
CREATE TABLE IF NOT EXISTS interaction_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    ref_kind TEXT,
    ref_id TEXT,
    payload TEXT,
    occurred_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_interaction_log_kind_time ON interaction_log(kind, occurred_at);
CREATE INDEX IF NOT EXISTS idx_interaction_log_occurred_at ON interaction_log(occurred_at);
`,
	},
	// Migration 9 introduces the hybrid-retrieval storage layer: the `sections`
	// block table (small-to-big), chunks.section_id, and the chunks_fts FTS5
	// index + sync triggers (BM25 lexical search). Handled by
	// applySectionsAndFTSV9Migration (special-cased below) so it stays
	// idempotent across fresh/upgraded installs and so FTS5 trigger creation
	// runs as discrete statements. Requires the sqlite_fts5 build tag.
	{
		Version: 9,
		Name:    "sections_and_fts5",
		SQL:     "", // handled by applySectionsAndFTSV9Migration
	},
	// Migration 10 adds persistent chat transcripts: chat_sessions +
	// chat_messages. Idempotent on fresh installs (schemaSQL v1 now declares
	// both tables). Upgraded DBs get them created here.
	{
		Version: 10,
		Name:    "chat_sessions",
		SQL: `
CREATE TABLE IF NOT EXISTS chat_sessions (
    session_id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    project_id TEXT REFERENCES projects(project_id) ON DELETE SET NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_updated_at ON chat_sessions(updated_at);
CREATE INDEX IF NOT EXISTS idx_chat_sessions_project_id ON chat_sessions(project_id);
CREATE TABLE IF NOT EXISTS chat_messages (
    message_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES chat_sessions(session_id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    sources TEXT,
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, ordinal);
`,
	},
	// Migration 11 adds persistent LLM token accounting (daily per-category
	// counters, UPSERTed) and a generic key/value app_settings store used here
	// for the cost-estimate pricing.
	{
		Version: 11,
		Name:    "token_usage_and_settings",
		SQL: `
CREATE TABLE IF NOT EXISTS token_usage (
    day        TEXT NOT NULL,
    category   TEXT NOT NULL,
    tokens_in  INTEGER NOT NULL DEFAULT 0,
    tokens_out INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, category)
);
CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`,
	},
	// Migration 12 adds chat_message_attachments: the image/audio media of a
	// user turn, so a reopened conversation re-displays the image and replays
	// the audio. data is NULL once an audio recording is purged by the size cap
	// (the row stays so the UI shows a clean "no longer available" placeholder).
	// Idempotent on fresh installs (schemaSQL v1 declares the table too).
	{
		Version: 12,
		Name:    "chat_message_attachments",
		SQL: `
CREATE TABLE IF NOT EXISTS chat_message_attachments (
    message_id TEXT NOT NULL REFERENCES chat_messages(message_id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    format TEXT NOT NULL DEFAULT '',
    data BLOB,
    byte_size INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (message_id, ordinal)
);
CREATE INDEX IF NOT EXISTS idx_chat_attachments_type ON chat_message_attachments(type, created_at);
`,
	},
	{
		Version: 13,
		Name:    "tasks",
		SQL: `
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    due_date TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    source_content_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
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

		// Migration 7 needs to be idempotent on fresh installs (schemaSQL v1
		// already declares the new memories columns). Older DBs that only
		// hold the v6 schema still need the ALTER TABLEs. The custom runner
		// handles both paths cleanly.
		if m.Version == 7 {
			if err := applyMemoriesV7Migration(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		} else if m.Version == 9 {
			if err := applySectionsAndFTSV9Migration(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
		} else if m.SQL != "" {
			if _, err := tx.Exec(m.SQL); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.Name, err)
			}
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

// applyMemoriesV7Migration adds the Phase 3.3 columns to memories only when
// they are missing. Fresh installs already see them via schemaSQL, so the
// ALTERs would error with "duplicate column name". We inspect PRAGMA
// table_info(memories) and add only what's missing, then back-fill
// accepted_at = created_at for legacy rows.
func applyMemoriesV7Migration(tx *sql.Tx) error {
	existing, err := existingColumns(tx, "memories")
	if err != nil {
		return err
	}
	type colSpec struct {
		name string
		sql  string
	}
	wanted := []colSpec{
		{name: "source", sql: "ALTER TABLE memories ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'"},
		{name: "accepted_at", sql: "ALTER TABLE memories ADD COLUMN accepted_at DATETIME"},
		{name: "embedding", sql: "ALTER TABLE memories ADD COLUMN embedding BLOB"},
		{name: "session_id", sql: "ALTER TABLE memories ADD COLUMN session_id TEXT"},
	}
	for _, c := range wanted {
		if _, ok := existing[c.name]; ok {
			continue
		}
		if _, err := tx.Exec(c.sql); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	// Back-fill accepted_at for legacy rows that pre-date the column. New
	// rows inserted after this migration can have NULL (= pending).
	if _, err := tx.Exec(`UPDATE memories SET accepted_at = created_at WHERE accepted_at IS NULL`); err != nil {
		return fmt.Errorf("backfill accepted_at: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source)`); err != nil {
		return fmt.Errorf("index source: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_memories_accepted_at ON memories(accepted_at)`); err != nil {
		return fmt.Errorf("index accepted_at: %w", err)
	}
	return nil
}

// applySectionsAndFTSV9Migration introduces the hierarchical + hybrid-search
// storage layer:
//   - the `sections` block table and chunks.section_id (small-to-big),
//   - the `chunks_fts` FTS5 virtual table + sync triggers (BM25 lexical search).
//
// Idempotent across fresh and upgraded DBs: schemaSQL (v1) already declares
// `sections` and chunks.section_id on fresh installs, so those are guarded with
// IF NOT EXISTS / PRAGMA inspection. The FTS5 objects live only here (not in
// schemaSQL) so each CREATE runs as a discrete statement.
//
// NOTE: requires the sqlite_fts5 build tag (see the Makefile). Without it,
// "CREATE VIRTUAL TABLE ... USING fts5" fails with "no such module: fts5".
func applySectionsAndFTSV9Migration(tx *sql.Tx) error {
	// 1. sections table + indexes (no-op when schemaSQL already created them).
	for _, s := range []string{
		`CREATE TABLE IF NOT EXISTS sections (
			section_id TEXT PRIMARY KEY,
			content_id TEXT NOT NULL REFERENCES knowledge_items(content_id) ON DELETE CASCADE,
			parent_section_id TEXT,
			heading TEXT,
			heading_path TEXT,
			level INTEGER NOT NULL DEFAULT 0,
			ordinal INTEGER NOT NULL DEFAULT 0,
			full_text TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sections_content_id ON sections(content_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sections_parent ON sections(parent_section_id)`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("sections ddl: %w", err)
		}
	}

	// 2. chunks.section_id — added via PRAGMA inspection (SQLite < 3.35 lacks
	//    ADD COLUMN IF NOT EXISTS; fresh installs already have it via schemaSQL).
	cols, err := existingColumns(tx, "chunks")
	if err != nil {
		return err
	}
	if _, ok := cols["section_id"]; !ok {
		if _, err := tx.Exec(`ALTER TABLE chunks ADD COLUMN section_id TEXT`); err != nil {
			return fmt.Errorf("add chunks.section_id: %w", err)
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chunks_section_id ON chunks(section_id)`); err != nil {
		return fmt.Errorf("index chunks.section_id: %w", err)
	}

	// 3. FTS5 index over chunk text + sync triggers. Standalone (not external
	//    content) so chunk_id/content_id are returned directly from a MATCH.
	//    French tokenizer: unicode61 with diacritics folded.
	for _, s := range []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			chunk_id UNINDEXED,
			content_id UNINDEXED,
			text,
			tokenize = 'unicode61 remove_diacritics 2'
		)`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			VALUES (new.chunk_id, new.content_id, new.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_ad AFTER DELETE ON chunks BEGIN
			DELETE FROM chunks_fts WHERE chunk_id = old.chunk_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS chunks_fts_au AFTER UPDATE ON chunks BEGIN
			DELETE FROM chunks_fts WHERE chunk_id = old.chunk_id;
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			VALUES (new.chunk_id, new.content_id, new.text);
		END`,
	} {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("fts5 ddl: %w", err)
		}
	}

	// 4. Backfill the FTS index from any chunks predating the triggers. The
	//    anti-join probes chunks_fts by chunk_id, which is UNINDEXED in FTS5, so
	//    it falls back to a full FTS scan PER chunk → O(n²). ensureRAGSchema re-runs
	//    this on EVERY boot, so guard it with a cheap count: skip when the index is
	//    already in sync (the steady state). Only an out-of-sync DB — the table was
	//    just created, or a version-mismatch skipped v9 — pays the one-time backfill.
	//    (Without the guard, boot time grew quadratically with the chunk count:
	//    ~10s at 2.7k chunks, ~106s at 5.2k.)
	var nChunks, nFTS int
	if err := tx.QueryRow(`SELECT count(*) FROM chunks`).Scan(&nChunks); err != nil {
		return fmt.Errorf("count chunks: %w", err)
	}
	if err := tx.QueryRow(`SELECT count(*) FROM chunks_fts`).Scan(&nFTS); err != nil {
		return fmt.Errorf("count chunks_fts: %w", err)
	}
	if nFTS < nChunks {
		if _, err := tx.Exec(`
			INSERT INTO chunks_fts(chunk_id, content_id, text)
			SELECT c.chunk_id, c.content_id, c.text
			FROM chunks c
			WHERE NOT EXISTS (SELECT 1 FROM chunks_fts f WHERE f.chunk_id = c.chunk_id)
		`); err != nil {
			return fmt.Errorf("backfill fts5: %w", err)
		}
	}
	return nil
}

// existingColumns returns the set of column names on `table`, derived from
// PRAGMA table_info. Used by applyMemoriesV7Migration to make the migration
// idempotent across fresh and upgraded installs.
func existingColumns(tx *sql.Tx, table string) (map[string]struct{}, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan column info: %w", err)
		}
		out[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
