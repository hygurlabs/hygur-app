package mail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// TestIndexRetry_EnqueueThenDrain proves R1's acceptance: a thread that fails to
// embed is parked in the retry queue, then re-indexed on a later drain WITHOUT a
// full sync and WITHOUT producing a duplicate knowledge item.
func TestIndexRetry_EnqueueThenDrain(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	now := time.Now()
	thread := Thread{
		ID:           "t-1",
		Subject:      "Invoice",
		Participants: []string{"a@b.com"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	mock := newMockMailConnector(
		[]Thread{thread},
		map[string][]Message{
			"t-1": {{
				ID: "m1", ThreadID: "t-1", From: "a@b.com", Subject: "Invoice",
				Body: "This message body is long enough to pass normalization and be chunked into the knowledge base for indexing.",
				Date: now,
			}},
		},
	)
	normalizer := NewThreadNormalizer()

	// 1. Enqueue: index against an embedder that always 500s. Call the per-thread
	//    processor directly to bypass IndexMailbox's pre-flight ping — a global
	//    outage is handled separately; R1 targets per-thread failures.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "embedding down", http.StatusInternalServerError)
	}))
	defer failSrv.Close()
	failSvc := llm.NewEmbeddingService(llm.NewClientWithHTTP(failSrv.URL, 5*time.Second, 0, failSrv.Client()), db)
	failIdx := NewMailboxIndexer(NewEmailIndexer(db, normalizer, failSvc, testLogger), mock)

	stats := &IndexStats{StartedAt: now}
	cfg := BatchIndexConfig{AccountID: "acct", Provider: "gmail", MaxConcurrent: 1, Timeout: 10 * time.Second}
	failIdx.processThreadsConcurrently(ctx, []Thread{thread}, cfg, stats)

	if stats.EmbeddingErrors == 0 {
		t.Fatal("expected an embedding error")
	}
	if n, _ := db.CountIndexRetry(ctx, "gmail", "acct"); n != 1 {
		t.Fatalf("expected 1 parked retry, got %d", n)
	}
	if item, _ := db.GetKnowledgeItem(ctx, "email:t-1"); item != nil {
		t.Fatal("no knowledge item should exist yet (rolled back on failure)")
	}

	// 2. Drain with a working embedder (nil svc → FTS-only success). The parked
	//    row was scheduled ~1min out, so force it due, then drain.
	okIdx := NewMailboxIndexer(NewEmailIndexer(db, normalizer, nil, testLogger), mock)
	if err := db.BumpIndexRetry(ctx, "gmail", "acct", "t-1", now.Add(-time.Minute), "due now"); err != nil {
		t.Fatalf("bump: %v", err)
	}
	indexed, _, _ := okIdx.DrainRetryQueue(ctx, "gmail", "acct", 100)
	if indexed != 1 {
		t.Fatalf("expected 1 thread indexed by drain, got %d", indexed)
	}
	if item, _ := db.GetKnowledgeItem(ctx, "email:t-1"); item == nil {
		t.Fatal("thread should be indexed after a successful drain")
	}
	if n, _ := db.CountIndexRetry(ctx, "gmail", "acct"); n != 0 {
		t.Fatalf("retry row should be gone after success, got %d", n)
	}

	// 3. A second drain is a no-op; the dedup keeps exactly one item (no duplicate).
	if indexed2, _, _ := okIdx.DrainRetryQueue(ctx, "gmail", "acct", 100); indexed2 != 0 {
		t.Fatalf("second drain should index nothing, got %d", indexed2)
	}
}

// TestIndexRetry_DropsVanishedThread checks that a parked retry whose thread no
// longer exists on the server is dropped (the queue self-empties), not retried
// forever.
func TestIndexRetry_DropsVanishedThread(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	// Mock has no threads → GetThread returns ErrThreadNotFound.
	mock := newMockMailConnector(nil, nil)
	idx := NewMailboxIndexer(NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger), mock)

	if err := db.EnqueueIndexRetry(ctx, "gmail", "acct", "gone-1", "embedding_failed", "boom", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	_, _, dropped := idx.DrainRetryQueue(ctx, "gmail", "acct", 100)
	if dropped != 1 {
		t.Fatalf("expected 1 dropped, got %d", dropped)
	}
	if n, _ := db.CountIndexRetry(ctx, "gmail", "acct"); n != 0 {
		t.Fatalf("vanished thread should be removed from queue, got %d rows", n)
	}
}
