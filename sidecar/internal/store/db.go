// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection and provides CRUD operations.
type DB struct {
	db *sql.DB
}

// SQLDB returns the underlying *sql.DB for callers (such as one-off
// migrations) that need raw SQL access. Use sparingly — prefer the typed
// CRUD methods on DB.
func (d *DB) SQLDB() *sql.DB {
	return d.db
}

// NewDB opens or creates a SQLite database at the given path and applies migrations.
// The database file is created with 0600 permissions for security.
// For in-memory databases, use ":memory:" as the path.
func NewDB(path string) (*DB, error) {
	// For non-memory databases, ensure the directory exists and set file permissions
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}

		// Create file with secure permissions if it doesn't exist
		if _, err := os.Stat(path); os.IsNotExist(err) {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				return nil, fmt.Errorf("failed to create database file: %w", err)
			}
			f.Close()
		}
	}

	// Open the database with foreign keys enabled
	dsn := path
	if path != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_foreign_keys=on", path)
	} else {
		// Use shared cache for in-memory databases so all connections (goroutines) see the same data
		// Use a unique name so each NewDB(":memory:") call creates a separate database
		uniqueID := uuid.New().String()[:8]
		dsn = fmt.Sprintf("file:memdb_%s?mode=memory&cache=shared&_foreign_keys=on", uniqueID)
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// In-memory SQLite databases deadlock with concurrent connections because
	// SQLite's shared-cache locking is not compatible with the Go connection
	// pool's concurrent-connection model. Limit to a single connection so all
	// accesses are serialised through one handle.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable foreign keys (belt and suspenders - also set in DSN)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Apply migrations
	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// InsertKnowledgeItem inserts a new knowledge item into the database.
func (d *DB) InsertKnowledgeItem(ctx context.Context, item *KnowledgeItem) error {
	metadata, err := json.Marshal(item.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO knowledge_items (content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, item.ContentID, item.SourceType, item.SourcePath, item.Title, item.NormalizedText, string(metadata), item.VersionID, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert knowledge item: %w", err)
	}

	return nil
}

// GetKnowledgeItem retrieves a knowledge item by its content ID.
func (d *DB) GetKnowledgeItem(ctx context.Context, contentID string) (*KnowledgeItem, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		WHERE content_id = ?
	`, contentID)

	item := &KnowledgeItem{}
	var metadataStr sql.NullString
	var sourcePath sql.NullString

	err := row.Scan(
		&item.ContentID,
		&item.SourceType,
		&sourcePath,
		&item.Title,
		&item.NormalizedText,
		&metadataStr,
		&item.VersionID,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get knowledge item: %w", err)
	}

	if sourcePath.Valid {
		item.SourcePath = &sourcePath.String
	}

	if metadataStr.Valid && metadataStr.String != "" {
		if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return item, nil
}

// GetKnowledgeItemByHash retrieves a knowledge item by its content hash (stored in metadata).
func (d *DB) GetKnowledgeItemByHash(ctx context.Context, contentHash string) (*KnowledgeItem, error) {
	// Query items where metadata contains the content_hash
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		WHERE json_extract(metadata, '$.content_hash') = ?
		LIMIT 1
	`, contentHash)
	if err != nil {
		return nil, fmt.Errorf("failed to query knowledge item by hash: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}

	item := &KnowledgeItem{}
	var metadataStr sql.NullString
	var sourcePath sql.NullString

	err = rows.Scan(
		&item.ContentID,
		&item.SourceType,
		&sourcePath,
		&item.Title,
		&item.NormalizedText,
		&metadataStr,
		&item.VersionID,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
	}

	if sourcePath.Valid {
		item.SourcePath = &sourcePath.String
	}

	if metadataStr.Valid && metadataStr.String != "" {
		if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return item, nil
}

// DeleteKnowledgeItem deletes a knowledge item and its associated chunks (via CASCADE).
func (d *DB) DeleteKnowledgeItem(ctx context.Context, contentID string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM knowledge_items WHERE content_id = ?", contentID)
	if err != nil {
		return fmt.Errorf("failed to delete knowledge item: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("knowledge item not found: %s", contentID)
	}

	return nil
}

// ListKnowledgeItems returns a paginated list of knowledge items.
func (d *DB) ListKnowledgeItems(ctx context.Context, limit, offset int) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge items: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr sql.NullString
		var sourcePath sql.NullString

		err := rows.Scan(
			&item.ContentID,
			&item.SourceType,
			&sourcePath,
			&item.Title,
			&item.NormalizedText,
			&metadataStr,
			&item.VersionID,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}

		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}

		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating knowledge items: %w", err)
	}

	return items, nil
}

// ListKnowledgeItemsSince returns items created or updated on/after `since`,
// optionally restricted to specific source_types. Ordered with the most
// recently created first. limit caps the result size; 0 means default 100.
//
// Used by the DailyBrief task to aggregate the last 24 h of activity.
func (d *DB) ListKnowledgeItemsSince(ctx context.Context, since time.Time, sourceTypes []string, limit int) ([]*KnowledgeItem, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		WHERE (created_at >= ? OR updated_at >= ?)
	`
	args := []any{since, since}
	if len(sourceTypes) > 0 {
		placeholders := make([]string, len(sourceTypes))
		for i, st := range sourceTypes {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += " AND source_type IN (" + strings.Join(placeholders, ", ") + ")"
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ListKnowledgeItemsSince: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr sql.NullString
		var sourcePath sql.NullString
		if err := rows.Scan(
			&item.ContentID, &item.SourceType, &sourcePath, &item.Title,
			&item.NormalizedText, &metadataStr, &item.VersionID,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}
		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ListRecentItems returns knowledge items created or updated within the last
// rangeHours hours, ordered newest first. It is a convenience wrapper around
// ListKnowledgeItemsSince used by the agenda pipeline.
func (d *DB) ListRecentItems(ctx context.Context, rangeHours int) ([]KnowledgeItem, error) {
	since := time.Now().Add(-time.Duration(rangeHours) * time.Hour)
	items, err := d.ListKnowledgeItemsSince(ctx, since, nil, 200)
	if err != nil {
		return nil, fmt.Errorf("ListRecentItems: %w", err)
	}
	out := make([]KnowledgeItem, 0, len(items))
	for _, item := range items {
		if item != nil {
			out = append(out, *item)
		}
	}
	return out, nil
}

// CountKnowledgeItems returns the total number of knowledge items.
func (d *DB) CountKnowledgeItems(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM knowledge_items`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count knowledge items: %w", err)
	}
	return count, nil
}

// CountKnowledgeItemsBySourceTypes returns the number of items whose source_type
// is in the supplied set. Empty slice → 0. Used by connectors that group several
// source_type values under a single logical bucket (e.g. files = markdown + txt
// + pdf + docx).
func (d *DB) CountKnowledgeItemsBySourceTypes(ctx context.Context, sourceTypes []string) (int, error) {
	if len(sourceTypes) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(sourceTypes))
	args := make([]any, len(sourceTypes))
	for i, st := range sourceTypes {
		placeholders[i] = "?"
		args[i] = st
	}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM knowledge_items WHERE source_type IN (%s)`,
		strings.Join(placeholders, ","))
	var count int
	if err := d.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count knowledge items by source types: %w", err)
	}
	return count, nil
}

// SearchKnowledgeItemsByTitle returns items whose title contains q (case-insensitive).
// Used for diagnostics (e.g., "is this email indexed?").
func (d *DB) SearchKnowledgeItemsByTitle(ctx context.Context, q string, limit int) ([]*KnowledgeItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		WHERE title LIKE ?
		ORDER BY created_at DESC
		LIMIT ?
	`, "%"+q+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search knowledge items by title: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr sql.NullString
		var sourcePath sql.NullString
		if err := rows.Scan(&item.ContentID, &item.SourceType, &sourcePath, &item.Title, &item.NormalizedText, &metadataStr, &item.VersionID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}
		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}
		if metadataStr.Valid && metadataStr.String != "" {
			_ = json.Unmarshal([]byte(metadataStr.String), &item.Metadata)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CountMailItemsByAccount returns the number of indexed email threads for the
// given mail account. The account is identified by its account_id (typically
// an email address). Counts items whose content_id starts with
// "mail:{account_id}:".
//
// LastIndexedAt is the most recent updated_at among matching items, or zero
// time if no items exist for the account.
func (d *DB) CountMailItemsByAccount(ctx context.Context, accountID, provider string) (count int64, lastIndexedAt time.Time, err error) {
	if accountID == "" {
		return 0, time.Time{}, fmt.Errorf("account_id is required")
	}
	// Match either the full email address (multi-account path) or the provider
	// name ("gmail", "proton") stored by the legacy single-account sync path.
	row := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(updated_at)
		FROM knowledge_items
		WHERE source_type = 'email'
		  AND (
		    json_extract(metadata, '$.account_id') = ?
		    OR (? != '' AND json_extract(metadata, '$.account_id') = ?)
		  )
	`, accountID, provider, provider)

	// MAX() over a DATETIME column comes back as TEXT through the sqlite3
	// driver because the aggregate's affinity is not preserved. Scan into a
	// string and parse explicitly.
	var maxUpdated sql.NullString
	if err := row.Scan(&count, &maxUpdated); err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to count mail items for %s: %w", accountID, err)
	}
	if maxUpdated.Valid && maxUpdated.String != "" {
		for _, layout := range []string{
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05",
			time.RFC3339Nano,
			time.RFC3339,
		} {
			if t, perr := time.Parse(layout, maxUpdated.String); perr == nil {
				lastIndexedAt = t
				break
			}
		}
	}
	return count, lastIndexedAt, nil
}

// ListKnowledgeItemsBySourceType returns items filtered by source_type.
func (d *DB) ListKnowledgeItemsBySourceType(ctx context.Context, sourceType string, limit, offset int) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items
		WHERE source_type = ?
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?
	`, sourceType, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list knowledge items: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr sql.NullString
		var sourcePath sql.NullString

		err := rows.Scan(
			&item.ContentID,
			&item.SourceType,
			&sourcePath,
			&item.Title,
			&item.NormalizedText,
			&metadataStr,
			&item.VersionID,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}

		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}

		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating knowledge items: %w", err)
	}

	return items, nil
}

// GetProjectIDForItem returns the project ID linked to a knowledge item, if any.
func (d *DB) GetProjectIDForItem(ctx context.Context, contentID string) (*string, error) {
	var projectID string
	err := d.db.QueryRowContext(ctx, `
		SELECT project_id FROM project_links WHERE content_id = ? LIMIT 1
	`, contentID).Scan(&projectID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project for item: %w", err)
	}
	return &projectID, nil
}

// UpdateKnowledgeItem updates an existing knowledge item.
func (d *DB) UpdateKnowledgeItem(ctx context.Context, item *KnowledgeItem) error {
	metadata, err := json.Marshal(item.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	item.UpdatedAt = time.Now()

	result, err := d.db.ExecContext(ctx, `
		UPDATE knowledge_items
		SET source_type = ?, source_path = ?, title = ?, normalized_text = ?, metadata = ?, version_id = ?, updated_at = ?
		WHERE content_id = ?
	`, item.SourceType, item.SourcePath, item.Title, item.NormalizedText, string(metadata), item.VersionID, item.UpdatedAt, item.ContentID)
	if err != nil {
		return fmt.Errorf("failed to update knowledge item: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("knowledge item not found: %s", item.ContentID)
	}

	return nil
}

// InsertChunk inserts a new chunk into the database.
func (d *DB) InsertChunk(ctx context.Context, chunk *Chunk) error {
	metadata, err := json.Marshal(chunk.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO chunks (chunk_id, content_id, chunk_hash, embedding_model, text, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, chunk.ChunkID, chunk.ContentID, chunk.ChunkHash, chunk.EmbeddingModel, chunk.Text, string(metadata), chunk.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert chunk: %w", err)
	}

	return nil
}

// GetChunksByContentID retrieves all chunks for a given content ID.
func (d *DB) GetChunksByContentID(ctx context.Context, contentID string) ([]*Chunk, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT chunk_id, content_id, chunk_hash, embedding_model, text, metadata, created_at
		FROM chunks
		WHERE content_id = ?
		ORDER BY created_at ASC
	`, contentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		chunk := &Chunk{}
		var metadataStr sql.NullString
		var embeddingModel sql.NullString

		err := rows.Scan(
			&chunk.ChunkID,
			&chunk.ContentID,
			&chunk.ChunkHash,
			&embeddingModel,
			&chunk.Text,
			&metadataStr,
			&chunk.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if embeddingModel.Valid {
			chunk.EmbeddingModel = &embeddingModel.String
		}

		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating chunks: %w", err)
	}

	return chunks, nil
}

// GetChunk retrieves a single chunk by its chunk_id. Returns nil, nil if not found.
func (d *DB) GetChunk(ctx context.Context, chunkID string) (*Chunk, error) {
	chunk := &Chunk{}
	var metadataStr sql.NullString
	var embeddingModel sql.NullString
	err := d.db.QueryRowContext(ctx, `
		SELECT chunk_id, content_id, chunk_hash, embedding_model, text, metadata, created_at
		FROM chunks WHERE chunk_id = ?
	`, chunkID).Scan(
		&chunk.ChunkID, &chunk.ContentID, &chunk.ChunkHash,
		&embeddingModel, &chunk.Text, &metadataStr, &chunk.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetChunk: %w", err)
	}
	if embeddingModel.Valid {
		chunk.EmbeddingModel = &embeddingModel.String
	}
	if metadataStr.Valid && metadataStr.String != "" {
		if err := json.Unmarshal([]byte(metadataStr.String), &chunk.Metadata); err != nil {
			return nil, fmt.Errorf("GetChunk metadata: %w", err)
		}
	}
	return chunk, nil
}

// DeleteChunksByContentID deletes all chunks for a given content ID.
func (d *DB) DeleteChunksByContentID(ctx context.Context, contentID string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM chunks WHERE content_id = ?", contentID)
	if err != nil {
		return fmt.Errorf("failed to delete chunks: %w", err)
	}
	return nil
}

// InsertProject inserts a new project into the database.
func (d *DB) InsertProject(ctx context.Context, project *Project) error {
	tags, err := json.Marshal(project.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO projects (project_id, name, description, tags, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, project.ProjectID, project.Name, project.Description, string(tags), project.Archived, project.CreatedAt, project.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert project: %w", err)
	}

	return nil
}

// GetProject retrieves a project by its ID.
func (d *DB) GetProject(ctx context.Context, projectID string) (*Project, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT project_id, name, description, tags, archived, created_at, updated_at
		FROM projects
		WHERE project_id = ?
	`, projectID)

	project := &Project{}
	var description sql.NullString
	var tagsStr sql.NullString

	err := row.Scan(
		&project.ProjectID,
		&project.Name,
		&description,
		&tagsStr,
		&project.Archived,
		&project.CreatedAt,
		&project.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	if description.Valid {
		project.Description = &description.String
	}

	if tagsStr.Valid && tagsStr.String != "" {
		if err := json.Unmarshal([]byte(tagsStr.String), &project.Tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
		}
	}

	return project, nil
}

// ListProjects returns all projects.
func (d *DB) ListProjects(ctx context.Context) ([]*Project, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT project_id, name, description, tags, archived, created_at, updated_at
		FROM projects
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		project := &Project{}
		var description sql.NullString
		var tagsStr sql.NullString

		err := rows.Scan(
			&project.ProjectID,
			&project.Name,
			&description,
			&tagsStr,
			&project.Archived,
			&project.CreatedAt,
			&project.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}

		if description.Valid {
			project.Description = &description.String
		}

		if tagsStr.Valid && tagsStr.String != "" {
			if err := json.Unmarshal([]byte(tagsStr.String), &project.Tags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tags: %w", err)
			}
		}

		projects = append(projects, project)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating projects: %w", err)
	}

	return projects, nil
}

// UpdateProject updates an existing project.
func (d *DB) UpdateProject(ctx context.Context, project *Project) error {
	project.UpdatedAt = time.Now()

	tags, err := json.Marshal(project.Tags)
	if err != nil {
		return fmt.Errorf("failed to marshal tags: %w", err)
	}

	result, err := d.db.ExecContext(ctx, `
		UPDATE projects
		SET name = ?, description = ?, tags = ?, archived = ?, updated_at = ?
		WHERE project_id = ?
	`, project.Name, project.Description, string(tags), project.Archived, project.UpdatedAt, project.ProjectID)
	if err != nil {
		return fmt.Errorf("failed to update project: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("project not found: %s", project.ProjectID)
	}

	return nil
}

// DeleteProject deletes a project and its associated links (via CASCADE).
func (d *DB) DeleteProject(ctx context.Context, projectID string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM projects WHERE project_id = ?", projectID)
	if err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("project not found: %s", projectID)
	}

	return nil
}

// InsertProjectLink creates a link between a project and a knowledge item.
func (d *DB) InsertProjectLink(ctx context.Context, link *ProjectLink) error {
	localTags, err := json.Marshal(link.LocalTags)
	if err != nil {
		return fmt.Errorf("failed to marshal local tags: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO project_links (link_id, project_id, content_id, local_title, local_notes, pin_state, local_tags, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, link.LinkID, link.ProjectID, link.ContentID, link.LocalTitle, link.LocalNotes, link.PinState, string(localTags), link.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert project link: %w", err)
	}

	return nil
}

// GetProjectLinks retrieves all links for a given project.
func (d *DB) GetProjectLinks(ctx context.Context, projectID string) ([]*ProjectLink, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT link_id, project_id, content_id, local_title, local_notes, pin_state, local_tags, created_at
		FROM project_links
		WHERE project_id = ?
		ORDER BY created_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get project links: %w", err)
	}
	defer rows.Close()

	var links []*ProjectLink
	for rows.Next() {
		link := &ProjectLink{}
		var localTitle sql.NullString
		var localNotes sql.NullString
		var localTagsStr sql.NullString

		err := rows.Scan(
			&link.LinkID,
			&link.ProjectID,
			&link.ContentID,
			&localTitle,
			&localNotes,
			&link.PinState,
			&localTagsStr,
			&link.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan project link: %w", err)
		}

		if localTitle.Valid {
			link.LocalTitle = &localTitle.String
		}
		if localNotes.Valid {
			link.LocalNotes = &localNotes.String
		}
		if localTagsStr.Valid && localTagsStr.String != "" {
			if err := json.Unmarshal([]byte(localTagsStr.String), &link.LocalTags); err != nil {
				return nil, fmt.Errorf("failed to unmarshal local tags: %w", err)
			}
		}

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating project links: %w", err)
	}

	return links, nil
}

// DeleteProjectLink deletes a project link by its ID.
func (d *DB) DeleteProjectLink(ctx context.Context, linkID string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM project_links WHERE link_id = ?", linkID)
	if err != nil {
		return fmt.Errorf("failed to delete project link: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("project link not found: %s", linkID)
	}

	return nil
}

// LinkToProject creates or updates a link between a knowledge item and a project.
// If a link already exists for this content_id, it updates the project_id.
func (d *DB) LinkToProject(ctx context.Context, contentID, projectID string) error {
	// First, remove any existing link for this content_id
	_, _ = d.db.ExecContext(ctx, "DELETE FROM project_links WHERE content_id = ?", contentID)

	// Create new link
	link := &ProjectLink{
		LinkID:    uuid.New().String(),
		ProjectID: projectID,
		ContentID: contentID,
		PinState:  false,
		LocalTags: []string{},
		CreatedAt: time.Now(),
	}

	return d.InsertProjectLink(ctx, link)
}

// UnlinkFromProject removes the link between a knowledge item and its project.
func (d *DB) UnlinkFromProject(ctx context.Context, contentID string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM project_links WHERE content_id = ?", contentID)
	if err != nil {
		return fmt.Errorf("failed to unlink from project: %w", err)
	}
	return nil
}

// ResolveFocusContentIDs returns the union of content_ids linked to any of
// the given project_ids OR carrying any of the given tag_ids. Used by the
// retrieval layer's focus_scope filter. Returns an empty (non-nil) slice
// when both inputs are empty so callers can distinguish "no filter requested"
// (caller passes nil/empty) from "filter requested but matched nothing"
// (this returns an empty slice).
func (d *DB) ResolveFocusContentIDs(ctx context.Context, projectIDs, tagIDs []string) ([]string, error) {
	if len(projectIDs) == 0 && len(tagIDs) == 0 {
		return []string{}, nil
	}

	seen := make(map[string]struct{})

	if len(projectIDs) > 0 {
		placeholders := strings.Repeat("?,", len(projectIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, 0, len(projectIDs))
		for _, id := range projectIDs {
			args = append(args, id)
		}
		query := fmt.Sprintf(
			"SELECT DISTINCT content_id FROM project_links WHERE project_id IN (%s)",
			placeholders)
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("resolve focus (projects): %w", err)
		}
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return nil, fmt.Errorf("resolve focus scan: %w", err)
			}
			seen[cid] = struct{}{}
		}
		rows.Close()
	}

	if len(tagIDs) > 0 {
		placeholders := strings.Repeat("?,", len(tagIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, 0, len(tagIDs))
		for _, id := range tagIDs {
			args = append(args, id)
		}
		query := fmt.Sprintf(
			"SELECT DISTINCT content_id FROM item_tags WHERE tag_id IN (%s)",
			placeholders)
		rows, err := d.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("resolve focus (tags): %w", err)
		}
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return nil, fmt.Errorf("resolve focus scan: %w", err)
			}
			seen[cid] = struct{}{}
		}
		rows.Close()
	}

	out := make([]string, 0, len(seen))
	for cid := range seen {
		out = append(out, cid)
	}
	return out, nil
}

// InsertSummary inserts a new summary into the database.
func (d *DB) InsertSummary(ctx context.Context, summary *Summary) error {
	decisions, err := json.Marshal(summary.Decisions)
	if err != nil {
		return fmt.Errorf("failed to marshal decisions: %w", err)
	}

	actions, err := json.Marshal(summary.Actions)
	if err != nil {
		return fmt.Errorf("failed to marshal actions: %w", err)
	}

	openQuestions, err := json.Marshal(summary.OpenQuestions)
	if err != nil {
		return fmt.Errorf("failed to marshal open questions: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO summaries (summary_id, source_ref, model_used, decisions, actions, open_questions, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, summary.SummaryID, summary.SourceRef, summary.ModelUsed, string(decisions), string(actions), string(openQuestions), summary.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert summary: %w", err)
	}

	return nil
}

// GetSummary retrieves a summary by its ID.
func (d *DB) GetSummary(ctx context.Context, summaryID string) (*Summary, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT summary_id, source_ref, model_used, decisions, actions, open_questions, created_at
		FROM summaries
		WHERE summary_id = ?
	`, summaryID)

	summary := &Summary{}
	var decisionsStr sql.NullString
	var actionsStr sql.NullString
	var openQuestionsStr sql.NullString

	err := row.Scan(
		&summary.SummaryID,
		&summary.SourceRef,
		&summary.ModelUsed,
		&decisionsStr,
		&actionsStr,
		&openQuestionsStr,
		&summary.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get summary: %w", err)
	}

	if decisionsStr.Valid && decisionsStr.String != "" {
		if err := json.Unmarshal([]byte(decisionsStr.String), &summary.Decisions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal decisions: %w", err)
		}
	}
	if actionsStr.Valid && actionsStr.String != "" {
		if err := json.Unmarshal([]byte(actionsStr.String), &summary.Actions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal actions: %w", err)
		}
	}
	if openQuestionsStr.Valid && openQuestionsStr.String != "" {
		if err := json.Unmarshal([]byte(openQuestionsStr.String), &summary.OpenQuestions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal open questions: %w", err)
		}
	}

	return summary, nil
}

// CountProjectItems returns the number of knowledge items linked to a project.
func (d *DB) CountProjectItems(ctx context.Context, projectID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM project_links WHERE project_id = ?",
		projectID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count project items: %w", err)
	}
	return count, nil
}

// GetItemsForProject returns all knowledge items linked to a project.
func (d *DB) GetItemsForProject(ctx context.Context, projectID string) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT ki.content_id, ki.source_type, ki.source_path, ki.title, ki.normalized_text,
		       ki.metadata, ki.version_id, ki.created_at, ki.updated_at
		FROM knowledge_items ki
		INNER JOIN project_links pl ON ki.content_id = pl.content_id
		WHERE pl.project_id = ?
		ORDER BY ki.updated_at DESC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to get items for project: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		var item KnowledgeItem
		var metadata string
		var sourcePath sql.NullString
		err := rows.Scan(
			&item.ContentID, &item.SourceType, &sourcePath, &item.Title,
			&item.NormalizedText, &metadata, &item.VersionID, &item.CreatedAt, &item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}
		if sourcePath.Valid {
			sp := sourcePath.String
			item.SourcePath = &sp
		}
		if err := json.Unmarshal([]byte(metadata), &item.Metadata); err != nil {
			item.Metadata = make(map[string]any)
		}
		items = append(items, &item)
	}

	return items, nil
}

// GetSummariesBySourceRef retrieves all summaries for a given source reference.
func (d *DB) GetSummariesBySourceRef(ctx context.Context, sourceRef string) ([]*Summary, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT summary_id, source_ref, model_used, decisions, actions, open_questions, created_at
		FROM summaries
		WHERE source_ref = ?
		ORDER BY created_at DESC
	`, sourceRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get summaries: %w", err)
	}
	defer rows.Close()

	var summaries []*Summary
	for rows.Next() {
		summary := &Summary{}
		var decisionsStr sql.NullString
		var actionsStr sql.NullString
		var openQuestionsStr sql.NullString

		err := rows.Scan(
			&summary.SummaryID,
			&summary.SourceRef,
			&summary.ModelUsed,
			&decisionsStr,
			&actionsStr,
			&openQuestionsStr,
			&summary.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan summary: %w", err)
		}

		if decisionsStr.Valid && decisionsStr.String != "" {
			if err := json.Unmarshal([]byte(decisionsStr.String), &summary.Decisions); err != nil {
				return nil, fmt.Errorf("failed to unmarshal decisions: %w", err)
			}
		}
		if actionsStr.Valid && actionsStr.String != "" {
			if err := json.Unmarshal([]byte(actionsStr.String), &summary.Actions); err != nil {
				return nil, fmt.Errorf("failed to unmarshal actions: %w", err)
			}
		}
		if openQuestionsStr.Valid && openQuestionsStr.String != "" {
			if err := json.Unmarshal([]byte(openQuestionsStr.String), &summary.OpenQuestions); err != nil {
				return nil, fmt.Errorf("failed to unmarshal open questions: %w", err)
			}
		}

		summaries = append(summaries, summary)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating summaries: %w", err)
	}

	return summaries, nil
}

// DiagnosticStats contains statistics about the knowledge base health.
type DiagnosticStats struct {
	TotalItems          int            `json:"total_items"`
	ItemsWithChunks     int            `json:"items_with_chunks"`
	ItemsWithEmbeddings int            `json:"items_with_embeddings"`
	TotalChunks         int            `json:"total_chunks"`
	TotalChunkVectors   int            `json:"total_chunk_vectors"`
	OrphanChunks        int            `json:"orphan_chunks"` // chunks without embeddings
	SourceTypeCounts    map[string]int `json:"source_type_counts"`
	MissingChunks       []string       `json:"missing_chunks"`     // content_ids without chunks
	MissingEmbeddings   []string       `json:"missing_embeddings"` // content_ids with chunks but no embeddings
}

// GetDiagnosticStats returns statistics about the knowledge base health.
func (d *DB) GetDiagnosticStats(ctx context.Context) (*DiagnosticStats, error) {
	stats := &DiagnosticStats{
		MissingChunks:     make([]string, 0),
		MissingEmbeddings: make([]string, 0),
		SourceTypeCounts:  make(map[string]int),
	}

	// Count total knowledge items
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_items").Scan(&stats.TotalItems)
	if err != nil {
		return nil, fmt.Errorf("failed to count knowledge items: %w", err)
	}

	// Count total chunks
	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&stats.TotalChunks)
	if err != nil {
		return nil, fmt.Errorf("failed to count chunks: %w", err)
	}

	// Count total chunk vectors
	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunk_vectors").Scan(&stats.TotalChunkVectors)
	if err != nil {
		return nil, fmt.Errorf("failed to count chunk vectors: %w", err)
	}

	// Count orphan chunks (chunks without embeddings)
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM chunks c
		LEFT JOIN chunk_vectors cv ON c.chunk_id = cv.chunk_id
		WHERE cv.chunk_id IS NULL
	`).Scan(&stats.OrphanChunks)
	if err != nil {
		return nil, fmt.Errorf("failed to count orphan chunks: %w", err)
	}

	// Count items by source type
	rows1, err := d.db.QueryContext(ctx, "SELECT source_type, COUNT(*) FROM knowledge_items GROUP BY source_type")
	if err != nil {
		return nil, fmt.Errorf("failed to count items by source type: %w", err)
	}
	defer rows1.Close()
	for rows1.Next() {
		var sourceType string
		var count int
		if err := rows1.Scan(&sourceType, &count); err != nil {
			return nil, fmt.Errorf("failed to scan source type count: %w", err)
		}
		stats.SourceTypeCounts[sourceType] = count
	}
	if err := rows1.Err(); err != nil {
		return nil, fmt.Errorf("error iterating source type counts: %w", err)
	}

	// Count items with chunks
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ki.content_id)
		FROM knowledge_items ki
		WHERE EXISTS (SELECT 1 FROM chunks c WHERE c.content_id = ki.content_id)
	`).Scan(&stats.ItemsWithChunks)
	if err != nil {
		return nil, fmt.Errorf("failed to count items with chunks: %w", err)
	}

	// Count items with embeddings (items that have at least one chunk with a vector)
	err = d.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ki.content_id)
		FROM knowledge_items ki
		WHERE EXISTS (
			SELECT 1 FROM chunks c
			JOIN chunk_vectors cv ON c.chunk_id = cv.chunk_id
			WHERE c.content_id = ki.content_id
		)
	`).Scan(&stats.ItemsWithEmbeddings)
	if err != nil {
		return nil, fmt.Errorf("failed to count items with embeddings: %w", err)
	}

	// Get items missing chunks
	rows2, err := d.db.QueryContext(ctx, `
		SELECT ki.content_id
		FROM knowledge_items ki
		WHERE NOT EXISTS (SELECT 1 FROM chunks c WHERE c.content_id = ki.content_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query items missing chunks: %w", err)
	}
	defer rows2.Close()

	for rows2.Next() {
		var contentID string
		if err := rows2.Scan(&contentID); err != nil {
			return nil, fmt.Errorf("failed to scan content_id: %w", err)
		}
		stats.MissingChunks = append(stats.MissingChunks, contentID)
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("error iterating missing chunks: %w", err)
	}

	// Get items with chunks but missing embeddings
	rows3, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT c.content_id
		FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM chunk_vectors cv WHERE cv.chunk_id = c.chunk_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query items missing embeddings: %w", err)
	}
	defer rows3.Close()

	for rows3.Next() {
		var contentID string
		if err := rows3.Scan(&contentID); err != nil {
			return nil, fmt.Errorf("failed to scan content_id: %w", err)
		}
		stats.MissingEmbeddings = append(stats.MissingEmbeddings, contentID)
	}
	if err := rows3.Err(); err != nil {
		return nil, fmt.Errorf("error iterating missing embeddings: %w", err)
	}

	return stats, nil
}

// ListOrphanChunks returns chunks that have no entry in chunk_vectors (i.e., were never embedded).
// Use limit <= 0 to fetch all orphans (capped internally at 10000).
func (d *DB) ListOrphanChunks(ctx context.Context, limit int) ([]Chunk, error) {
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT c.chunk_id, c.content_id, c.chunk_hash, c.embedding_model, c.text, c.metadata, c.created_at
		FROM chunks c
		LEFT JOIN chunk_vectors cv ON c.chunk_id = cv.chunk_id
		WHERE cv.chunk_id IS NULL
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("ListOrphanChunks: %w", err)
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		var embeddingModel sql.NullString
		var metadataStr sql.NullString
		if err := rows.Scan(&c.ChunkID, &c.ContentID, &c.ChunkHash, &embeddingModel, &c.Text, &metadataStr, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("ListOrphanChunks scan: %w", err)
		}
		if embeddingModel.Valid {
			c.EmbeddingModel = &embeddingModel.String
		}
		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &c.Metadata); err != nil {
				c.Metadata = nil
			}
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// GetItemsWithoutChunks returns all knowledge items that have no chunks.
func (d *DB) GetItemsWithoutChunks(ctx context.Context) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
		FROM knowledge_items ki
		WHERE NOT EXISTS (SELECT 1 FROM chunks c WHERE c.content_id = ki.content_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query items without chunks: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr sql.NullString
		var sourcePath sql.NullString

		err := rows.Scan(
			&item.ContentID,
			&item.SourceType,
			&sourcePath,
			&item.Title,
			&item.NormalizedText,
			&metadataStr,
			&item.VersionID,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan knowledge item: %w", err)
		}

		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}

		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &item.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating items without chunks: %w", err)
	}

	return items, nil
}

// GetChunksWithoutEmbeddings returns all chunks that have no embeddings.
func (d *DB) GetChunksWithoutEmbeddings(ctx context.Context) ([]*Chunk, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT chunk_id, content_id, chunk_hash, embedding_model, text, metadata, created_at
		FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM chunk_vectors cv WHERE cv.chunk_id = c.chunk_id)
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query chunks without embeddings: %w", err)
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		chunk := &Chunk{}
		var metadataStr sql.NullString
		var embeddingModel sql.NullString

		err := rows.Scan(
			&chunk.ChunkID,
			&chunk.ContentID,
			&chunk.ChunkHash,
			&embeddingModel,
			&chunk.Text,
			&metadataStr,
			&chunk.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if embeddingModel.Valid {
			chunk.EmbeddingModel = &embeddingModel.String
		}

		if metadataStr.Valid && metadataStr.String != "" {
			if err := json.Unmarshal([]byte(metadataStr.String), &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating chunks without embeddings: %w", err)
	}

	return chunks, nil
}

// CountChunksForItem returns the number of chunks for a given content ID.
func (d *DB) CountChunksForItem(ctx context.Context, contentID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM chunks WHERE content_id = ?",
		contentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count chunks: %w", err)
	}
	return count, nil
}

// CountEmbeddingsForItem returns the number of embeddings for chunks belonging to a given content ID.
func (d *DB) CountEmbeddingsForItem(ctx context.Context, contentID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM chunk_vectors cv
		JOIN chunks c ON cv.chunk_id = c.chunk_id
		WHERE c.content_id = ?
	`, contentID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count embeddings: %w", err)
	}
	return count, nil
}

// TagItemLink represents a link between a tag and a knowledge item.
type TagItemLink struct {
	TagID     string
	ContentID string
}

// GetAllTagItemLinks returns all tag-item relationships for graph visualization.
func (d *DB) GetAllTagItemLinks(ctx context.Context) ([]TagItemLink, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT tag_id, content_id FROM item_tags")
	if err != nil {
		return nil, fmt.Errorf("failed to query tag-item links: %w", err)
	}
	defer rows.Close()

	var links []TagItemLink
	for rows.Next() {
		var link TagItemLink
		if err := rows.Scan(&link.TagID, &link.ContentID); err != nil {
			return nil, fmt.Errorf("failed to scan tag-item link: %w", err)
		}
		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tag-item links: %w", err)
	}

	return links, nil
}

// GetAllProjectLinks returns all project-item relationships for graph visualization.
func (d *DB) GetAllProjectLinks(ctx context.Context) ([]ProjectLink, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT link_id, project_id, content_id, local_title, local_notes, pin_state, local_tags, created_at
		FROM project_links
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query project links: %w", err)
	}
	defer rows.Close()

	var links []ProjectLink
	for rows.Next() {
		var link ProjectLink
		var localTitle, localNotes sql.NullString
		var localTagsStr sql.NullString

		if err := rows.Scan(
			&link.LinkID,
			&link.ProjectID,
			&link.ContentID,
			&localTitle,
			&localNotes,
			&link.PinState,
			&localTagsStr,
			&link.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan project link: %w", err)
		}

		if localTitle.Valid {
			link.LocalTitle = &localTitle.String
		}
		if localNotes.Valid {
			link.LocalNotes = &localNotes.String
		}
		if localTagsStr.Valid && localTagsStr.String != "" {
			_ = json.Unmarshal([]byte(localTagsStr.String), &link.LocalTags)
		}

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating project links: %w", err)
	}

	return links, nil
}

// ResetKnowledge clears all knowledge-related data from the database.
// This includes knowledge_items, chunks, chunk_vectors, and project_links.
// Projects themselves are preserved.
func (d *DB) ResetKnowledge(ctx context.Context) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete in order respecting foreign keys
	// chunk_vectors -> chunks -> project_links -> knowledge_items
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunk_vectors"); err != nil {
		return fmt.Errorf("failed to delete chunk_vectors: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks"); err != nil {
		return fmt.Errorf("failed to delete chunks: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM project_links"); err != nil {
		return fmt.Errorf("failed to delete project_links: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_items"); err != nil {
		return fmt.Errorf("failed to delete knowledge_items: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// InsertMemory inserts a new memory into the database.
func (d *DB) InsertMemory(m *Memory) error {
	_, err := d.db.ExecContext(
		context.Background(),
		`INSERT INTO memories (memory_id, type, content, context_id, created_at, expires_at, score)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.MemoryID, m.Type, m.Content, m.ContextID, m.CreatedAt, m.ExpiresAt, m.Score,
	)
	return err
}

// GetMemory retrieves a memory by its ID.
func (d *DB) GetMemory(ctx context.Context, memoryID string) (*Memory, error) {
	var m Memory
	err := d.db.QueryRowContext(ctx,
		`SELECT memory_id, type, content, context_id, created_at, expires_at, score
		 FROM memories WHERE memory_id = ?`, memoryID).Scan(
		&m.MemoryID, &m.Type, &m.Content, &m.ContextID, &m.CreatedAt, &m.ExpiresAt, &m.Score,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SearchMemories searches memories by querying content.
func (d *DB) SearchMemories(ctx context.Context, query string, limit int) ([]*Memory, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT memory_id, type, content, context_id, created_at, expires_at, score
		 FROM memories
		 WHERE content LIKE ?
		 LIMIT ?`,
		"%"+query+"%", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.MemoryID, &m.Type, &m.Content, &m.ContextID, &m.CreatedAt, &m.ExpiresAt, &m.Score); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		results = append(results, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ListMemoriesAfter returns all memories created after a given time.
func (d *DB) ListMemoriesAfter(ctx context.Context, since time.Time) ([]*Memory, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT memory_id, type, content, context_id, created_at, expires_at, score
		 FROM memories
		 WHERE created_at > ?`,
		since.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.MemoryID, &m.Type, &m.Content, &m.ContextID, &m.CreatedAt, &m.ExpiresAt, &m.Score); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		results = append(results, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// DeleteMemory removes a memory by its ID.
func (d *DB) DeleteMemory(ctx context.Context, memoryID string) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM memories WHERE memory_id = ?`, memoryID)
	return err
}

// GetMaxEmbeddingDimension returns the maximum vector dimension currently stored
// in chunk_vectors, measured as number of float32 values (BLOB length / 4).
// Returns 0 if no vectors are stored yet.
func (d *DB) GetMaxEmbeddingDimension(ctx context.Context) (int, error) {
	var dim sql.NullInt64
	err := d.db.QueryRowContext(ctx,
		`SELECT MAX(LENGTH(embedding)/4) FROM chunk_vectors WHERE embedding IS NOT NULL`,
	).Scan(&dim)
	if err != nil {
		return 0, fmt.Errorf("get max embedding dimension: %w", err)
	}
	if !dim.Valid {
		return 0, nil
	}
	return int(dim.Int64), nil
}

// CleanExpiredMemories removes all expired memories.
func (d *DB) CleanExpiredMemories(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?`,
		time.Now().Format(time.RFC3339),
	)
	return err
}
