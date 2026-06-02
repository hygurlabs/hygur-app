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
