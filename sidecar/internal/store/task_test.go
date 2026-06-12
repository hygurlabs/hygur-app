package store

import (
	"context"
	"testing"
	"time"
)

// makeTaskItem inserts a task knowledge_item (the note-like backing row) so the
// task-attr/list helpers have something to join against.
func makeTaskItem(t *testing.T, db *DB, id, title string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &KnowledgeItem{
		ContentID: id, SourceType: SourceTypeTask, Title: title, NormalizedText: "",
		Metadata: map[string]any{}, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert task item %s: %v", id, err)
	}
}

func TestTaskAttrsAndList(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	makeTaskItem(t, db, "task:1", "Open with due date")
	makeTaskItem(t, db, "task:2", "Done one")
	if err := db.UpsertTaskAttrs(ctx, "task:1", "open", "2026-06-15T00:00:00Z"); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := db.UpsertTaskAttrs(ctx, "task:2", "done", ""); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	all, err := db.ListTasks(ctx, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListTasks = %d (err %v)", len(all), err)
	}
	// Open tasks sort before done ones.
	if all[0].ID != "task:1" || all[0].Status != "open" || all[0].DueDate != "2026-06-15T00:00:00Z" {
		t.Errorf("ordering/fields off: %+v", all[0])
	}

	// Status filter.
	open, _ := db.ListTasks(ctx, "", "open")
	if len(open) != 1 || open[0].ID != "task:1" {
		t.Errorf("status filter = %+v", open)
	}

	// Get one + missing.
	got, _ := db.GetTask(ctx, "task:2")
	if got == nil || got.Title != "Done one" || got.Status != "done" {
		t.Errorf("get = %+v", got)
	}
	if missing, _ := db.GetTask(ctx, "task:nope"); missing != nil {
		t.Errorf("want nil for missing task")
	}

	// Upsert updates in place (status flips to done).
	if err := db.UpsertTaskAttrs(ctx, "task:1", "done", "2026-06-15T00:00:00Z"); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got1, _ := db.GetTask(ctx, "task:1")
	if got1 == nil || got1.Status != "done" {
		t.Errorf("upsert did not update: %+v", got1)
	}
}

// Guards that migration 15 ran ALL its statements (multi-statement Exec): the
// task_attrs table must exist AND the legacy tasks table must be gone. If the
// driver only ran the first statement, tasks would survive — exactly the failure
// that would silently strand the live migration.
func TestMigration15DropsLegacyTasksTable(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	exists := func(name string) bool {
		var n int
		if err := db.db.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
			t.Fatalf("sqlite_master query: %v", err)
		}
		return n > 0
	}
	if !exists("task_attrs") {
		t.Error("task_attrs table should exist after migrations")
	}
	if exists("tasks") {
		t.Error("legacy tasks table should be dropped by migration 15 (multi-statement Exec ran)")
	}
}

func TestTasksDueBefore(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	makeTaskItem(t, db, "task:soon", "Soon")
	makeTaskItem(t, db, "task:late", "Late")
	makeTaskItem(t, db, "task:none", "No date")
	makeTaskItem(t, db, "task:done", "Done soon")
	_ = db.UpsertTaskAttrs(ctx, "task:soon", "open", "2026-06-12T00:00:00Z")
	_ = db.UpsertTaskAttrs(ctx, "task:late", "open", "2026-07-01T00:00:00Z")
	_ = db.UpsertTaskAttrs(ctx, "task:none", "open", "")
	_ = db.UpsertTaskAttrs(ctx, "task:done", "done", "2026-06-12T00:00:00Z")

	due, err := db.TasksDueBefore(ctx, "2026-06-20T00:00:00Z")
	if err != nil {
		t.Fatalf("TasksDueBefore: %v", err)
	}
	// Only the open, dated, on-or-before-cutoff task qualifies.
	if len(due) != 1 || due[0].ID != "task:soon" {
		t.Fatalf("want [task:soon], got %+v", due)
	}
}
