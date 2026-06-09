package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Task is a lightweight local to-do, optionally linked to a project and to the
// knowledge item it was created from. Timestamps are RFC3339 strings.
type Task struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Status          string `json:"status"` // "open" | "done"
	DueDate         string `json:"due_date,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	SourceContentID string `json:"source_content_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// CreateTask inserts a task, filling id/status/timestamps when absent.
func (d *DB) CreateTask(ctx context.Context, t *Task) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if strings.TrimSpace(t.Status) == "" {
		t.Status = "open"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	t.CreatedAt, t.UpdatedAt = now, now
	_, err := d.db.ExecContext(ctx, `
INSERT INTO tasks (id, title, status, due_date, project_id, source_content_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Title, t.Status, t.DueDate, t.ProjectID, t.SourceContentID, t.CreatedAt, t.UpdatedAt)
	return err
}

// ListTasks returns tasks, optionally filtered by project and/or status. Open
// tasks first, then by due date / recency.
func (d *DB) ListTasks(ctx context.Context, projectID, status string) ([]*Task, error) {
	q := `SELECT id, title, status, due_date, project_id, source_content_id, created_at, updated_at FROM tasks`
	var conds []string
	var args []any
	if projectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, projectID)
	}
	if status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY (status = 'done') ASC, created_at DESC"

	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.DueDate, &t.ProjectID, &t.SourceContentID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// GetTask loads one task by id; returns (nil, nil) when not found.
func (d *DB) GetTask(ctx context.Context, id string) (*Task, error) {
	var t Task
	err := d.db.QueryRowContext(ctx, `
SELECT id, title, status, due_date, project_id, source_content_id, created_at, updated_at
FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.Title, &t.Status, &t.DueDate, &t.ProjectID, &t.SourceContentID, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTask persists title/status/due_date for an existing task and stamps
// updated_at.
func (d *DB) UpdateTask(ctx context.Context, t *Task) error {
	t.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx, `
UPDATE tasks SET title = ?, status = ?, due_date = ?, updated_at = ? WHERE id = ?`,
		t.Title, t.Status, t.DueDate, t.UpdatedAt, t.ID)
	return err
}

// DeleteTask removes a task by id.
func (d *DB) DeleteTask(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	return err
}
