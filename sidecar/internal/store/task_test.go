package store

import (
	"context"
	"testing"
)

func TestTaskCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	a := &Task{Title: "Reply to client", ProjectID: "p1", SourceContentID: "imap:1@x"}
	if err := db.CreateTask(ctx, a); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if a.ID == "" || a.Status != "open" || a.CreatedAt == "" {
		t.Fatalf("create did not fill defaults: %+v", a)
	}
	b := &Task{Title: "Other project task", ProjectID: "p2"}
	if err := db.CreateTask(ctx, b); err != nil {
		t.Fatalf("CreateTask b: %v", err)
	}

	// Filter by project.
	p1, err := db.ListTasks(ctx, "p1", "")
	if err != nil || len(p1) != 1 || p1[0].ID != a.ID {
		t.Fatalf("ListTasks(p1) = %v (err %v)", p1, err)
	}

	// Complete a → open filter excludes it.
	a.Status = "done"
	if err := db.UpdateTask(ctx, a); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	open, err := db.ListTasks(ctx, "", "open")
	if err != nil {
		t.Fatalf("ListTasks(open): %v", err)
	}
	for _, tk := range open {
		if tk.ID == a.ID {
			t.Error("done task still in open filter")
		}
	}

	// Delete b.
	if err := db.DeleteTask(ctx, b.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if got, _ := db.GetTask(ctx, b.ID); got != nil {
		t.Error("deleted task still present")
	}
	all, _ := db.ListTasks(ctx, "", "")
	if len(all) != 1 {
		t.Fatalf("want 1 task left, got %d", len(all))
	}
}
