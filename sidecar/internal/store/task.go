package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Task is a note-like to-do. The body, tags and project live on the underlying
// knowledge_item (source_type="task"); the task state (status, due_date) lives in
// task_attrs. ID is the knowledge_item content_id ("task:<uuid>"). Tags/ProjectID
// are hydrated by the handler for responses, not stored here.
type Task struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"` // "open" | "done"
	DueDate   string `json:"due_date,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// UpsertTaskAttrs writes the task state (status, due_date) for a task's content
// id, stamping updated_at. status defaults to "open" when blank.
func (d *DB) UpsertTaskAttrs(ctx context.Context, contentID, status, dueDate string) error {
	if strings.TrimSpace(status) == "" {
		status = "open"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx, `
INSERT INTO task_attrs (content_id, status, due_date, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(content_id) DO UPDATE SET status = excluded.status, due_date = excluded.due_date, updated_at = excluded.updated_at`,
		contentID, status, dueDate, now, now)
	return err
}

// taskSelect is the join that composes a task from its knowledge_item + task_attrs.
const taskSelect = `
SELECT ki.content_id, ki.title, ki.normalized_text, ta.status, ta.due_date, ki.created_at, ki.updated_at
FROM knowledge_items ki
JOIN task_attrs ta ON ta.content_id = ki.content_id`

func scanTask(rows interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var created, updated time.Time
	if err := rows.Scan(&t.ID, &t.Title, &t.Body, &t.Status, &t.DueDate, &created, &updated); err != nil {
		return nil, err
	}
	t.CreatedAt = created.UTC().Format(time.RFC3339)
	t.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return &t, nil
}

// ListTasks returns tasks, optionally filtered by project (via project_links) and
// status. Open tasks first, then by due date (dated first, soonest first), then
// most-recent.
func (d *DB) ListTasks(ctx context.Context, projectID, status string) ([]*Task, error) {
	q := taskSelect
	var conds []string
	var args []any
	if projectID != "" {
		q += " JOIN project_links pl ON pl.content_id = ki.content_id"
		conds = append(conds, "pl.project_id = ?")
		args = append(args, projectID)
	}
	if status != "" {
		conds = append(conds, "ta.status = ?")
		args = append(args, status)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY (ta.status = 'done') ASC, (ta.due_date = '') ASC, ta.due_date ASC, ki.created_at DESC"

	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask loads one task by content id; returns (nil, nil) when not found.
func (d *DB) GetTask(ctx context.Context, id string) (*Task, error) {
	t, err := scanTask(d.db.QueryRowContext(ctx, taskSelect+" WHERE ki.content_id = ?", id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// TasksDueBefore returns open tasks with a due date on or before the cutoff
// (RFC3339/UTC), soonest first — the feed for proactive briefings/alerts.
func (d *DB) TasksDueBefore(ctx context.Context, cutoffRFC3339 string) ([]*Task, error) {
	rows, err := d.db.QueryContext(ctx,
		taskSelect+" WHERE ta.status = 'open' AND ta.due_date != '' AND ta.due_date <= ? ORDER BY ta.due_date ASC",
		cutoffRFC3339)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
