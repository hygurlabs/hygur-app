package store

import (
	"context"
	"testing"
	"time"
)

func TestIndexRetry_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	now := time.Now()

	// Two parked retries for the same (connector, account): one due, one future.
	if err := db.EnqueueIndexRetry(ctx, "gmail", "acct", "thread-1", "embedding_failed", "boom", now.Add(-time.Minute)); err != nil {
		t.Fatalf("enqueue thread-1: %v", err)
	}
	if err := db.EnqueueIndexRetry(ctx, "gmail", "acct", "thread-2", "embedding_failed", "boom", now.Add(time.Hour)); err != nil {
		t.Fatalf("enqueue thread-2: %v", err)
	}

	// Only thread-1 is due.
	due, err := db.DueIndexRetries(ctx, "gmail", "acct", now, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 || due[0].SourceRef != "thread-1" {
		t.Fatalf("expected only thread-1 due, got %+v", due)
	}

	// Re-enqueuing the same key must UPDATE, not duplicate (PK upsert).
	if err := db.EnqueueIndexRetry(ctx, "gmail", "acct", "thread-1", "embedding_failed", "boom2", now.Add(-time.Second)); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if n, _ := db.CountIndexRetry(ctx, "gmail", "acct"); n != 2 {
		t.Fatalf("expected 2 rows after re-enqueue, got %d", n)
	}

	// Bump thread-1 into the future → no longer due, attempts advanced.
	if err := db.BumpIndexRetry(ctx, "gmail", "acct", "thread-1", now.Add(time.Hour), "again"); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if due, _ := db.DueIndexRetries(ctx, "gmail", "acct", now, 10); len(due) != 0 {
		t.Fatalf("expected nothing due after bump, got %d", len(due))
	}
	all, _ := db.DueIndexRetries(ctx, "gmail", "acct", now.Add(2*time.Hour), 10)
	var found bool
	for _, r := range all {
		if r.SourceRef == "thread-1" {
			found = true
			if r.Attempts != 1 {
				t.Fatalf("expected thread-1 attempts=1, got %d", r.Attempts)
			}
		}
	}
	if !found {
		t.Fatal("thread-1 missing from due set at now+2h")
	}

	// Delete.
	if err := db.DeleteIndexRetry(ctx, "gmail", "acct", "thread-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, _ := db.CountIndexRetry(ctx, "gmail", "acct"); n != 1 {
		t.Fatalf("expected 1 row left, got %d", n)
	}
}
