// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// sectionColumns is the canonical SELECT list for the sections table, kept in
// one place so every scan path stays in sync with scanSectionRow.
const sectionColumns = `section_id, content_id, parent_section_id, heading, heading_path, level, ordinal, full_text, token_count, metadata, created_at`

// InsertSection inserts or replaces a section (upsert on section_id). Sections
// are rewritten wholesale when a document is re-ingested, so an upsert keeps
// re-indexing idempotent without a separate delete step.
func (d *DB) InsertSection(ctx context.Context, s *Section) error {
	headingPath, err := json.Marshal(s.HeadingPath)
	if err != nil {
		return fmt.Errorf("marshal heading_path: %w", err)
	}
	metadata, err := json.Marshal(s.Metadata)
	if err != nil {
		return fmt.Errorf("marshal section metadata: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO sections
			(section_id, content_id, parent_section_id, heading, heading_path, level, ordinal, full_text, token_count, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(section_id) DO UPDATE SET
			content_id        = excluded.content_id,
			parent_section_id = excluded.parent_section_id,
			heading           = excluded.heading,
			heading_path      = excluded.heading_path,
			level             = excluded.level,
			ordinal           = excluded.ordinal,
			full_text         = excluded.full_text,
			token_count       = excluded.token_count,
			metadata          = excluded.metadata
	`, s.SectionID, s.ContentID, s.ParentSectionID, s.Heading, string(headingPath),
		s.Level, s.Ordinal, s.FullText, s.TokenCount, string(metadata))
	if err != nil {
		return fmt.Errorf("insert section: %w", err)
	}
	return nil
}

// GetSection retrieves a single section by ID. Returns nil, nil if not found.
func (d *DB) GetSection(ctx context.Context, sectionID string) (*Section, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+sectionColumns+` FROM sections WHERE section_id = ?`, sectionID)
	s, err := scanSectionRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetSection: %w", err)
	}
	return s, nil
}

// GetSectionByChunkID returns the section a chunk belongs to (its full logical
// block), used for small-to-big expansion at retrieval time. Returns nil, nil
// when the chunk has no section (e.g. legacy rows) or doesn't exist.
func (d *DB) GetSectionByChunkID(ctx context.Context, chunkID string) (*Section, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT s.section_id, s.content_id, s.parent_section_id, s.heading, s.heading_path,
		       s.level, s.ordinal, s.full_text, s.token_count, s.metadata, s.created_at
		FROM sections s
		JOIN chunks c ON c.section_id = s.section_id
		WHERE c.chunk_id = ?`, chunkID)
	s, err := scanSectionRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetSectionByChunkID: %w", err)
	}
	return s, nil
}

// GetSectionsByContentID returns all sections of a document in document order.
func (d *DB) GetSectionsByContentID(ctx context.Context, contentID string) ([]*Section, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+sectionColumns+` FROM sections WHERE content_id = ? ORDER BY ordinal ASC`, contentID)
	if err != nil {
		return nil, fmt.Errorf("GetSectionsByContentID: %w", err)
	}
	defer rows.Close()

	var out []*Section
	for rows.Next() {
		s, err := scanSectionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sections: %w", err)
	}
	return out, nil
}

// DeleteSectionsByContentID removes every section of a document. Re-ingestion
// uses InsertSection's upsert, so this is mainly for full deletes; the
// knowledge_items ON DELETE CASCADE also covers item removal.
func (d *DB) DeleteSectionsByContentID(ctx context.Context, contentID string) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM sections WHERE content_id = ?`, contentID); err != nil {
		return fmt.Errorf("delete sections: %w", err)
	}
	return nil
}

// scanSectionRow scans one sections row (from *sql.Row or *sql.Rows) in the
// column order of sectionColumns. Follows the scanMemoryRow pattern.
func scanSectionRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*Section, error) {
	s := &Section{}
	var parent, headingPathStr, metadataStr sql.NullString
	if err := scanner.Scan(
		&s.SectionID,
		&s.ContentID,
		&parent,
		&s.Heading,
		&headingPathStr,
		&s.Level,
		&s.Ordinal,
		&s.FullText,
		&s.TokenCount,
		&metadataStr,
		&s.CreatedAt,
	); err != nil {
		return nil, err
	}
	if parent.Valid {
		s.ParentSectionID = &parent.String
	}
	if headingPathStr.Valid && headingPathStr.String != "" {
		if err := json.Unmarshal([]byte(headingPathStr.String), &s.HeadingPath); err != nil {
			return nil, fmt.Errorf("unmarshal heading_path: %w", err)
		}
	}
	if metadataStr.Valid && metadataStr.String != "" {
		if err := json.Unmarshal([]byte(metadataStr.String), &s.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal section metadata: %w", err)
		}
	}
	return s, nil
}
