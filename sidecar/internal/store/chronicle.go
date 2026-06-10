package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SourceTypeChronicleAct marks a knowledge_item that is one nightly Chronicle act
// (a dated narrative block). The chapter it belongs to is encoded in its content_id
// (chronicle:<chapter_id>:<YYYY-MM-DD>) and in metadata.chapter_id.
const SourceTypeChronicleAct = "chronicle_act"

// ChronicleChapter is the narrative state of one chapter (a project's life, or the
// always-open "life" chapter). The prose itself is the chapter's acts; this row
// carries the rolling synopsis + watermark used to write the next act in
// continuity without re-narrating.
type ChronicleChapter struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id,omitempty"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Synopsis    string `json:"synopsis,omitempty"`
	Watermark   string `json:"watermark,omitempty"`    // RFC3339, last chronicled ingestion time
	PendingNote string `json:"pending_note,omitempty"` // set on reopen; the next write folds it in, then clears it
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func scanChronicleChapter(row interface{ Scan(...any) error }) (*ChronicleChapter, error) {
	var c ChronicleChapter
	var created, updated time.Time
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Title, &c.Status, &c.Synopsis, &c.Watermark, &c.PendingNote, &created, &updated); err != nil {
		return nil, err
	}
	c.CreatedAt = created.UTC().Format(time.RFC3339)
	c.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return &c, nil
}

const chronicleChapterCols = `id, project_id, title, status, synopsis, watermark, pending_note, created_at, updated_at`

// GetChronicleChapter loads one chapter by id; returns (nil, nil) when not found.
func (d *DB) GetChronicleChapter(ctx context.Context, id string) (*ChronicleChapter, error) {
	c, err := scanChronicleChapter(d.db.QueryRowContext(ctx,
		`SELECT `+chronicleChapterCols+` FROM chronicle_chapters WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// UpsertChronicleChapter creates or updates a chapter's state, stamping updated_at.
func (d *DB) UpsertChronicleChapter(ctx context.Context, c *ChronicleChapter) error {
	if c.Status == "" {
		c.Status = "open"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx, `
INSERT INTO chronicle_chapters (id, project_id, title, status, synopsis, watermark, pending_note, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  project_id = excluded.project_id, title = excluded.title, status = excluded.status,
  synopsis = excluded.synopsis, watermark = excluded.watermark, pending_note = excluded.pending_note,
  updated_at = excluded.updated_at`,
		c.ID, c.ProjectID, c.Title, c.Status, c.Synopsis, c.Watermark, c.PendingNote, now, now)
	return err
}

// ListChronicleChapters returns all chapters, the always-open "life" chapter first,
// then most-recently-updated.
func (d *DB) ListChronicleChapters(ctx context.Context) ([]*ChronicleChapter, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+chronicleChapterCols+` FROM chronicle_chapters ORDER BY (project_id = '') DESC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChronicleChapter
	for rows.Next() {
		c, err := scanChronicleChapter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChronicleActs returns a chapter's acts (chronicle_act items) in chronological
// order. Acts are keyed content_id = "chronicle:<chapterID>:<date>", so ordering by
// content_id orders by date.
func (d *DB) ListChronicleActs(ctx context.Context, chapterID string) ([]*KnowledgeItem, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT content_id, source_type, source_path, title, normalized_text, metadata, version_id, created_at, updated_at
FROM knowledge_items
WHERE source_type = ? AND content_id LIKE ? ESCAPE '\'
ORDER BY content_id ASC`,
		SourceTypeChronicleAct, `chronicle:`+escapeLike(chapterID)+`:%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var metadataStr, sourcePath sql.NullString
		if err := rows.Scan(
			&item.ContentID, &item.SourceType, &sourcePath, &item.Title,
			&item.NormalizedText, &metadataStr, &item.VersionID, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
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

// escapeLike escapes LIKE wildcards in a literal segment (we build the pattern with
// ESCAPE '\'). Chapter ids are tame ("life", "proj:<uuid>") but be safe.
func escapeLike(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '%', '_':
			r = append(r, '\\')
		}
		r = append(r, s[i])
	}
	return string(r)
}
