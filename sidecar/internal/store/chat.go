package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ChatSession is a persisted conversation. It is the durable counterpart to the
// in-memory session.Store (which only caches entities/topic for follow-ups).
type ChatSession struct {
	SessionID string
	Title     string
	ProjectID *string
	CreatedAt time.Time
	UpdatedAt time.Time

	// MessageCount and LastMessage are computed on listing — not stored columns.
	MessageCount int
	LastMessage  string
}

// ChatMessage is one persisted turn within a session.
type ChatMessage struct {
	MessageID string
	SessionID string
	Role      string // "user" | "assistant"
	Content   string
	Sources   string // JSON array of RAGSource; empty for user turns
	Ordinal   int
	CreatedAt time.Time
}

// ChatAttachment is one media item (image / audio) carried by a user turn,
// persisted so a reopened conversation can re-display / replay it. Data holds
// the raw bytes (decoded from the wire base64); it is nil when the audio has
// been purged by the retention cap, in which case the row is still returned so
// the UI can show a "recording no longer available" placeholder.
type ChatAttachment struct {
	Type     string // "image" | "audio"
	Title    string
	MimeType string // image MIME
	Format   string // audio format
	Data     []byte // nil when purged
	ByteSize int
}

// maxAudioAttachmentBytes caps the total retained audio across all
// conversations. Once exceeded, the oldest audio recordings have their bytes
// purged (data set to NULL) until back under the cap — the metadata row stays
// so the history shows a clean placeholder. Images are never purged (small).
// A var (not const) so tests can lower it without allocating the real cap.
var maxAudioAttachmentBytes int64 = 200 << 20 // 200 MiB

// CreateChatSession inserts a new conversation row. project_id may be nil.
func (d *DB) CreateChatSession(ctx context.Context, s *ChatSession) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO chat_sessions (session_id, title, project_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, s.SessionID, s.Title, s.ProjectID, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert chat session: %w", err)
	}
	return nil
}

// ChatSessionExists reports whether a session row is present.
func (d *DB) ChatSessionExists(ctx context.Context, sessionID string) (bool, error) {
	var one int
	err := d.db.QueryRowContext(ctx, `SELECT 1 FROM chat_sessions WHERE session_id = ?`, sessionID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check chat session: %w", err)
	}
	return true, nil
}

// GetChatSession returns one session (without messages) or nil when absent.
func (d *DB) GetChatSession(ctx context.Context, sessionID string) (*ChatSession, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT session_id, title, project_id, created_at, updated_at
		FROM chat_sessions WHERE session_id = ?
	`, sessionID)
	s := &ChatSession{}
	var projectID sql.NullString
	err := row.Scan(&s.SessionID, &s.Title, &projectID, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chat session: %w", err)
	}
	if projectID.Valid {
		s.ProjectID = &projectID.String
	}
	return s, nil
}

// ListChatSessions returns sessions ordered by most-recently-updated, each
// decorated with its message count and a preview of the latest message.
func (d *DB) ListChatSessions(ctx context.Context, limit int) ([]*ChatSession, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT s.session_id, s.title, s.project_id, s.created_at, s.updated_at,
		       COUNT(m.message_id) AS msg_count,
		       COALESCE((
		           SELECT content FROM chat_messages
		           WHERE session_id = s.session_id
		           ORDER BY ordinal DESC LIMIT 1
		       ), '') AS last_message
		FROM chat_sessions s
		LEFT JOIN chat_messages m ON m.session_id = s.session_id
		GROUP BY s.session_id
		ORDER BY s.updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list chat sessions: %w", err)
	}
	defer rows.Close()

	var out []*ChatSession
	for rows.Next() {
		s := &ChatSession{}
		var projectID sql.NullString
		if err := rows.Scan(&s.SessionID, &s.Title, &projectID, &s.CreatedAt, &s.UpdatedAt, &s.MessageCount, &s.LastMessage); err != nil {
			return nil, fmt.Errorf("failed to scan chat session: %w", err)
		}
		if projectID.Valid {
			s.ProjectID = &projectID.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateChatSession patches title and/or project_id. Pass non-nil pointers for
// the fields to change. A non-nil projectID pointing at "" clears the link.
func (d *DB) UpdateChatSession(ctx context.Context, sessionID string, title *string, projectID *string) error {
	if title != nil {
		if _, err := d.db.ExecContext(ctx, `UPDATE chat_sessions SET title = ?, updated_at = ? WHERE session_id = ?`, *title, time.Now(), sessionID); err != nil {
			return fmt.Errorf("failed to update chat session title: %w", err)
		}
	}
	if projectID != nil {
		if *projectID == "" {
			if _, err := d.db.ExecContext(ctx, `UPDATE chat_sessions SET project_id = NULL, updated_at = ? WHERE session_id = ?`, time.Now(), sessionID); err != nil {
				return fmt.Errorf("failed to clear chat session project: %w", err)
			}
		} else if _, err := d.db.ExecContext(ctx, `UPDATE chat_sessions SET project_id = ?, updated_at = ? WHERE session_id = ?`, *projectID, time.Now(), sessionID); err != nil {
			return fmt.Errorf("failed to set chat session project: %w", err)
		}
	}
	return nil
}

// DeleteChatSession removes a session and (via CASCADE) all its messages.
func (d *DB) DeleteChatSession(ctx context.Context, sessionID string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete chat session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("chat session not found: %s", sessionID)
	}
	return nil
}

// AppendChatMessage inserts a turn, auto-assigning the next ordinal, and bumps
// the session's updated_at so listings sort by recency.
func (d *DB) AppendChatMessage(ctx context.Context, m *ChatMessage) error {
	var nextOrdinal int
	if err := d.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal), -1) + 1 FROM chat_messages WHERE session_id = ?`,
		m.SessionID,
	).Scan(&nextOrdinal); err != nil {
		return fmt.Errorf("failed to compute message ordinal: %w", err)
	}
	m.Ordinal = nextOrdinal
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	var sources any
	if m.Sources != "" {
		sources = m.Sources
	}
	if _, err := d.db.ExecContext(ctx, `
		INSERT INTO chat_messages (message_id, session_id, role, content, sources, ordinal, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, m.MessageID, m.SessionID, m.Role, m.Content, sources, m.Ordinal, m.CreatedAt); err != nil {
		return fmt.Errorf("failed to insert chat message: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, `UPDATE chat_sessions SET updated_at = ? WHERE session_id = ?`, m.CreatedAt, m.SessionID); err != nil {
		return fmt.Errorf("failed to touch chat session: %w", err)
	}
	return nil
}

// ListMailContentIDsByAccount returns the content_ids of every indexed mail
// item belonging to the given account. Used by mail-deletion reconciliation to
// find items no longer present on the server.
func (d *DB) ListMailContentIDsByAccount(ctx context.Context, accountID string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT content_id FROM knowledge_items
		WHERE source_type IN ('email','thread','mail')
		  AND json_extract(metadata, '$.account_id') = ?
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to list mail items for account: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan mail content_id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// (ListChatMessages follows.)

// ListChatMessages returns every turn of a session in order.
func (d *DB) ListChatMessages(ctx context.Context, sessionID string) ([]*ChatMessage, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT message_id, session_id, role, content, sources, ordinal, created_at
		FROM chat_messages WHERE session_id = ?
		ORDER BY ordinal ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list chat messages: %w", err)
	}
	defer rows.Close()

	var out []*ChatMessage
	for rows.Next() {
		m := &ChatMessage{}
		var sources sql.NullString
		if err := rows.Scan(&m.MessageID, &m.SessionID, &m.Role, &m.Content, &sources, &m.Ordinal, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan chat message: %w", err)
		}
		if sources.Valid {
			m.Sources = sources.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppendChatMessageAttachments persists the media attached to a message (in
// order) and then enforces the audio retention cap. Best-effort cap: a failure
// to purge is non-fatal (the attachments are already stored).
func (d *DB) AppendChatMessageAttachments(ctx context.Context, messageID string, atts []ChatAttachment) error {
	if len(atts) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin attachments tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	for i, a := range atts {
		var data any // keep SQL NULL for empty data rather than a 0-byte blob
		if len(a.Data) > 0 {
			data = a.Data
		}
		byteSize := a.ByteSize
		if byteSize == 0 {
			byteSize = len(a.Data)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO chat_message_attachments
				(message_id, ordinal, type, title, mime_type, format, data, byte_size, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, messageID, i, a.Type, a.Title, a.MimeType, a.Format, data, byteSize, now); err != nil {
			return fmt.Errorf("failed to insert chat attachment: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit attachments: %w", err)
	}
	if err := d.enforceAudioAttachmentCap(ctx); err != nil {
		return fmt.Errorf("failed to enforce audio cap: %w", err)
	}
	return nil
}

// enforceAudioAttachmentCap purges (NULLs) the bytes of the oldest retained
// audio attachments until total retained audio is back under the cap. Rows are
// kept (only data is cleared) so the UI shows a placeholder.
func (d *DB) enforceAudioAttachmentCap(ctx context.Context) error {
	var total int64
	if err := d.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(byte_size), 0) FROM chat_message_attachments WHERE type = 'audio' AND data IS NOT NULL`,
	).Scan(&total); err != nil {
		return fmt.Errorf("failed to sum audio bytes: %w", err)
	}
	if total <= maxAudioAttachmentBytes {
		return nil
	}
	// Collect oldest-first candidates, then purge (can't UPDATE while iterating
	// the same SQLite connection's open rows).
	type ref struct {
		messageID string
		ordinal   int
		size      int64
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT message_id, ordinal, byte_size
		FROM chat_message_attachments
		WHERE type = 'audio' AND data IS NOT NULL
		ORDER BY created_at ASC, message_id ASC, ordinal ASC
	`)
	if err != nil {
		return fmt.Errorf("failed to list audio attachments: %w", err)
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.messageID, &r.ordinal, &r.size); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan audio attachment: %w", err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range refs {
		if total <= maxAudioAttachmentBytes {
			break
		}
		if _, err := d.db.ExecContext(ctx,
			`UPDATE chat_message_attachments SET data = NULL WHERE message_id = ? AND ordinal = ?`,
			r.messageID, r.ordinal,
		); err != nil {
			return fmt.Errorf("failed to purge audio attachment: %w", err)
		}
		total -= r.size
	}
	return nil
}

// ListChatMessageAttachments returns the attachments of every message in a
// session, keyed by message_id and ordered within each message. A nil Data
// means the bytes were purged (audio cap) — the caller renders a placeholder.
func (d *DB) ListChatMessageAttachments(ctx context.Context, sessionID string) (map[string][]ChatAttachment, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT a.message_id, a.type, a.title, a.mime_type, a.format, a.data, a.byte_size
		FROM chat_message_attachments a
		JOIN chat_messages m ON m.message_id = a.message_id
		WHERE m.session_id = ?
		ORDER BY a.message_id ASC, a.ordinal ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to list chat attachments: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]ChatAttachment)
	for rows.Next() {
		var mid string
		var a ChatAttachment
		var data []byte // scans NULL as nil
		if err := rows.Scan(&mid, &a.Type, &a.Title, &a.MimeType, &a.Format, &data, &a.ByteSize); err != nil {
			return nil, fmt.Errorf("failed to scan chat attachment: %w", err)
		}
		a.Data = data
		out[mid] = append(out[mid], a)
	}
	return out, rows.Err()
}
