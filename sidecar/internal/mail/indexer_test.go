package mail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// testLogger discards output. zerolog.NewTestWriter(nil) panics the
// first time anything is logged because the underlying *testing.T pointer
// is nil — package-level test loggers can't tie themselves to a single
// t.Helper, so we just throw the records away.
var testLogger = zerolog.New(io.Discard).With().Timestamp().Logger()

func TestEmailIndexer_IndexThread(t *testing.T) {
	ctx := context.Background()

	// Create in-memory database
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	indexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	// Create test thread and messages
	now := time.Now()
	thread := &Thread{
		ID:           "thread-123",
		Subject:      "Project Update Meeting",
		Participants: []string{"alice@example.com", "bob@example.com"},
		DateRange:    [2]time.Time{now.Add(-time.Hour), now},
		MessageCount: 2,
	}

	messages := []Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-123",
			From:     "alice@example.com",
			To:       []string{"bob@example.com"},
			Date:     now.Add(-time.Hour),
			Subject:  "Project Update Meeting",
			Body:     "Hi Bob, let's discuss the project status tomorrow.",
		},
		{
			ID:       "msg-2",
			ThreadID: "thread-123",
			From:     "bob@example.com",
			To:       []string{"alice@example.com"},
			Date:     now,
			Subject:  "Re: Project Update Meeting",
			Body:     "Sure Alice, I'll prepare the slides.",
		},
	}

	// Test 1: Index a new thread
	result, err := indexer.IndexThread(ctx, thread, messages, "")
	if err != nil {
		t.Fatalf("IndexThread failed: %v", err)
	}

	if result.ContentID != "email:thread-123" {
		t.Errorf("expected ContentID 'email:thread-123', got '%s'", result.ContentID)
	}

	if result.Status != "indexed" {
		t.Errorf("expected Status 'indexed', got '%s'", result.Status)
	}

	if result.ChunkCount == 0 {
		t.Error("expected ChunkCount > 0")
	}

	// Verify KnowledgeItem was created
	item, err := db.GetKnowledgeItem(ctx, "email:thread-123")
	if err != nil {
		t.Fatalf("GetKnowledgeItem failed: %v", err)
	}
	if item == nil {
		t.Fatal("KnowledgeItem not found")
	}
	if item.Title != "Project Update Meeting" {
		t.Errorf("expected Title 'Project Update Meeting', got '%s'", item.Title)
	}
	if item.SourceType != "email" {
		t.Errorf("expected SourceType 'email', got '%s'", item.SourceType)
	}

	// Verify metadata
	if item.Metadata["thread_id"] != "thread-123" {
		t.Errorf("expected thread_id 'thread-123', got '%v'", item.Metadata["thread_id"])
	}

	// Test 2: Index same thread again - should be duplicate
	result2, err := indexer.IndexThread(ctx, thread, messages, "")
	if err != nil {
		t.Fatalf("IndexThread (duplicate) failed: %v", err)
	}

	if result2.Status != "duplicate" {
		t.Errorf("expected Status 'duplicate', got '%s'", result2.Status)
	}

	if result2.ChunkCount != 0 {
		t.Errorf("expected ChunkCount 0 for duplicate, got %d", result2.ChunkCount)
	}
}

func TestEmailIndexer_IndexThread_Update(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	indexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	now := time.Now()
	thread := &Thread{
		ID:           "thread-456",
		Subject:      "Budget Discussion",
		Participants: []string{"charlie@example.com"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}

	messages := []Message{
		{
			ID:       "msg-1",
			ThreadID: "thread-456",
			From:     "charlie@example.com",
			To:       []string{"team@example.com"},
			Date:     now,
			Subject:  "Budget Discussion",
			Body:     "Initial budget proposal.",
		},
	}

	// Index initial version
	result, err := indexer.IndexThread(ctx, thread, messages, "")
	if err != nil {
		t.Fatalf("IndexThread failed: %v", err)
	}
	if result.Status != "indexed" {
		t.Errorf("expected Status 'indexed', got '%s'", result.Status)
	}

	// Update messages (add a reply)
	messages = append(messages, Message{
		ID:       "msg-2",
		ThreadID: "thread-456",
		From:     "finance@example.com",
		To:       []string{"charlie@example.com"},
		Date:     now.Add(time.Hour),
		Subject:  "Re: Budget Discussion",
		Body:     "Budget approved!",
	})
	thread.MessageCount = 2
	thread.DateRange[1] = now.Add(time.Hour)
	thread.Participants = append(thread.Participants, "finance@example.com")

	// Index updated version
	result2, err := indexer.IndexThread(ctx, thread, messages, "")
	if err != nil {
		t.Fatalf("IndexThread (update) failed: %v", err)
	}

	if result2.Status != "updated" {
		t.Errorf("expected Status 'updated', got '%s'", result2.Status)
	}

	if result2.ChunkCount == 0 {
		t.Error("expected ChunkCount > 0 for updated thread")
	}

	// Verify the item was updated
	item, err := db.GetKnowledgeItem(ctx, "email:thread-456")
	if err != nil {
		t.Fatalf("GetKnowledgeItem failed: %v", err)
	}
	if item == nil {
		t.Fatal("KnowledgeItem not found after update")
	}

	// Content should contain the new message
	// Note: JSON unmarshals numbers as float64, so we compare as float64
	msgCount, ok := item.Metadata["message_count"].(float64)
	if !ok {
		// If not float64, try int (for when data is not round-tripped through JSON)
		if intCount, ok := item.Metadata["message_count"].(int); ok {
			msgCount = float64(intCount)
		} else {
			t.Fatalf("message_count is not a number: %T", item.Metadata["message_count"])
		}
	}
	if msgCount != 2.0 {
		t.Errorf("expected message_count 2, got %v", msgCount)
	}
}

func TestEmailIndexer_NilInputs(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()

	// Test nil store
	indexerNoStore := NewEmailIndexer(nil, normalizer, nil, testLogger)
	_, err = indexerNoStore.IndexThread(ctx, &Thread{ID: "test"}, []Message{}, "")
	if err == nil {
		t.Error("expected error with nil store")
	}

	// Test nil normalizer
	indexerNoNorm := NewEmailIndexer(db, nil, nil, testLogger)
	_, err = indexerNoNorm.IndexThread(ctx, &Thread{ID: "test"}, []Message{}, "")
	if err == nil {
		t.Error("expected error with nil normalizer")
	}

	// Test nil thread
	indexer := NewEmailIndexer(db, normalizer, nil, testLogger)
	_, err = indexer.IndexThread(ctx, nil, []Message{}, "")
	if err == nil {
		t.Error("expected error with nil thread")
	}
}

func TestEmailIndexer_EmptyMessages(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	indexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	thread := &Thread{
		ID:      "thread-empty",
		Subject: "Empty Thread",
	}

	// Empty messages should fail (normalizer returns ErrEmptyThread)
	_, err = indexer.IndexThread(ctx, thread, []Message{}, "")
	if err == nil {
		t.Error("expected error with empty messages")
	}
}

// mockMailConnector is a mock implementation of MailConnector for testing.
type mockMailConnector struct {
	threads   []Thread
	messages  map[string][]Message
	connected bool
}

func newMockMailConnector(threads []Thread, messages map[string][]Message) *mockMailConnector {
	return &mockMailConnector{
		threads:   threads,
		messages:  messages,
		connected: true,
	}
}

func (m *mockMailConnector) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockMailConnector) Disconnect() error {
	m.connected = false
	return nil
}

func (m *mockMailConnector) IsConnected() bool {
	return m.connected
}

func (m *mockMailConnector) ListThreads(ctx context.Context, opts ListOptions) ([]Thread, error) {
	start := opts.Offset
	if start >= len(m.threads) {
		return nil, nil
	}
	// Limit == 0 means "no limit" in the real connectors — return everything
	// from `start` onwards.
	end := len(m.threads)
	if opts.Limit > 0 {
		end = start + opts.Limit
		if end > len(m.threads) {
			end = len(m.threads)
		}
	}
	return m.threads[start:end], nil
}

func (m *mockMailConnector) GetThread(ctx context.Context, threadID string) (*Thread, error) {
	for _, t := range m.threads {
		if t.ID == threadID {
			return &t, nil
		}
	}
	return nil, ErrThreadNotFound
}

func (m *mockMailConnector) GetMessages(ctx context.Context, threadID string) ([]Message, error) {
	if msgs, ok := m.messages[threadID]; ok {
		return msgs, nil
	}
	return nil, ErrThreadNotFound
}

func (m *mockMailConnector) GetMessagesByThread(ctx context.Context, thread *Thread) ([]Message, error) {
	if thread == nil {
		return nil, ErrThreadNotFound
	}
	return m.GetMessages(ctx, thread.ID)
}

func TestMailboxIndexer_IndexMailbox(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	emailIndexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	now := time.Now()

	// Create test threads and messages
	threads := []Thread{
		{
			ID:           "thread-1",
			Subject:      "Meeting Tomorrow",
			Participants: []string{"alice@example.com", "bob@example.com"},
			DateRange:    [2]time.Time{now, now},
			MessageCount: 1,
		},
		{
			ID:           "thread-2",
			Subject:      "Project Update",
			Participants: []string{"charlie@example.com"},
			DateRange:    [2]time.Time{now, now},
			MessageCount: 1,
		},
	}

	messages := map[string][]Message{
		"thread-1": {
			{
				ID:       "msg-1",
				ThreadID: "thread-1",
				From:     "alice@example.com",
				To:       []string{"bob@example.com"},
				Date:     now,
				Subject:  "Meeting Tomorrow",
				Body:     "Let's meet tomorrow at 10am to discuss the project.",
			},
		},
		"thread-2": {
			{
				ID:       "msg-2",
				ThreadID: "thread-2",
				From:     "charlie@example.com",
				To:       []string{"team@example.com"},
				Date:     now,
				Subject:  "Project Update",
				Body:     "The project is on track. We'll finish by Friday.",
			},
		},
	}

	connector := newMockMailConnector(threads, messages)
	mailboxIndexer := NewMailboxIndexer(emailIndexer, connector)

	// Test batch indexing
	config := BatchIndexConfig{
		BatchSize:     10,
		MaxConcurrent: 2,
		Timeout:       5 * time.Second,
	}

	stats, err := mailboxIndexer.IndexMailbox(ctx, "mock", "INBOX", config)
	if err != nil {
		t.Fatalf("IndexMailbox failed: %v", err)
	}

	if stats.TotalThreads != 2 {
		t.Errorf("expected TotalThreads 2, got %d", stats.TotalThreads)
	}

	if stats.ProcessedThreads != 2 {
		t.Errorf("expected ProcessedThreads 2, got %d", stats.ProcessedThreads)
	}

	if stats.Errors != 0 {
		t.Errorf("expected 0 errors, got %d: %v", stats.Errors, stats.ErrorMessages)
	}

	if stats.IndexedMessages != 2 {
		t.Errorf("expected IndexedMessages 2, got %d", stats.IndexedMessages)
	}

	// Verify items were indexed
	item1, err := db.GetKnowledgeItem(ctx, "email:thread-1")
	if err != nil {
		t.Fatalf("failed to get item 1: %v", err)
	}
	if item1 == nil {
		t.Error("item 1 not found")
	}

	item2, err := db.GetKnowledgeItem(ctx, "email:thread-2")
	if err != nil {
		t.Fatalf("failed to get item 2: %v", err)
	}
	if item2 == nil {
		t.Error("item 2 not found")
	}
}

func TestMailboxIndexer_IndexMailbox_Duplicates(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	emailIndexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	now := time.Now()
	threads := []Thread{
		{
			ID:           "thread-dup",
			Subject:      "Duplicate Test",
			Participants: []string{"test@example.com"},
			DateRange:    [2]time.Time{now, now},
			MessageCount: 1,
		},
	}

	messages := map[string][]Message{
		"thread-dup": {
			{
				ID:       "msg-dup",
				ThreadID: "thread-dup",
				From:     "test@example.com",
				To:       []string{"team@example.com"},
				Date:     now,
				Subject:  "Duplicate Test",
				Body:     "This is a test message for duplicate detection.",
			},
		},
	}

	connector := newMockMailConnector(threads, messages)
	mailboxIndexer := NewMailboxIndexer(emailIndexer, connector)

	config := DefaultBatchIndexConfig()

	// First indexing
	stats1, err := mailboxIndexer.IndexMailbox(ctx, "mock", "INBOX", config)
	if err != nil {
		t.Fatalf("first IndexMailbox failed: %v", err)
	}

	if stats1.SkippedDuplicates != 0 {
		t.Errorf("first run should have 0 duplicates, got %d", stats1.SkippedDuplicates)
	}

	// Second indexing - should detect duplicate
	stats2, err := mailboxIndexer.IndexMailbox(ctx, "mock", "INBOX", config)
	if err != nil {
		t.Fatalf("second IndexMailbox failed: %v", err)
	}

	if stats2.SkippedDuplicates != 1 {
		t.Errorf("second run should have 1 duplicate, got %d", stats2.SkippedDuplicates)
	}

	if stats2.IndexedMessages != 0 {
		t.Errorf("second run should have 0 indexed messages, got %d", stats2.IndexedMessages)
	}
}

func TestMailboxIndexer_IndexMailbox_EmptyMailbox(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	emailIndexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	connector := newMockMailConnector(nil, nil)
	mailboxIndexer := NewMailboxIndexer(emailIndexer, connector)

	config := DefaultBatchIndexConfig()

	stats, err := mailboxIndexer.IndexMailbox(ctx, "mock", "INBOX", config)
	if err != nil {
		t.Fatalf("IndexMailbox failed: %v", err)
	}

	if stats.TotalThreads != 0 {
		t.Errorf("expected TotalThreads 0, got %d", stats.TotalThreads)
	}

	if stats.ProcessedThreads != 0 {
		t.Errorf("expected ProcessedThreads 0, got %d", stats.ProcessedThreads)
	}
}

func TestMailboxIndexer_NilConnector(t *testing.T) {
	ctx := context.Background()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	normalizer := NewThreadNormalizer()
	emailIndexer := NewEmailIndexer(db, normalizer, nil, testLogger)

	mailboxIndexer := NewMailboxIndexer(emailIndexer, nil)

	config := DefaultBatchIndexConfig()

	_, err = mailboxIndexer.IndexMailbox(ctx, "mock", "INBOX", config)
	if err == nil {
		t.Error("expected error with nil connector")
	}
}

func TestBatchIndexConfig_Defaults(t *testing.T) {
	config := DefaultBatchIndexConfig()

	if config.BatchSize != 10 {
		t.Errorf("expected BatchSize 10, got %d", config.BatchSize)
	}

	if config.MaxConcurrent != 3 {
		t.Errorf("expected MaxConcurrent 3, got %d", config.MaxConcurrent)
	}

	if config.Timeout != 30*time.Second {
		t.Errorf("expected Timeout 30s, got %v", config.Timeout)
	}
}

func TestEmailIndexer_IndexThread_EmbeddingFailRollback(t *testing.T) {
	ctx := context.Background()

	// Spin up a test HTTP server that always returns 500 for embedding requests.
	// This causes BatchEmbedAndStore to fail, triggering the rollback path.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "embedding service unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Build a real EmbeddingService whose client points to the failing server.
	failClient := llm.NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	failSvc := llm.NewEmbeddingService(failClient, db)

	normalizer := NewThreadNormalizer()
	indexer := NewEmailIndexer(db, normalizer, failSvc, zerolog.New(io.Discard))

	now := time.Now()
	thread := &Thread{
		ID:           "rollback-test",
		Subject:      "Test rollback",
		Participants: []string{"a@b.com"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	messages := []Message{{
		ID:      "msg1",
		From:    "a@b.com",
		Subject: "Test rollback",
		Body:    "This is a test message body that is long enough to pass normalization. It contains enough content to be chunked.",
		Date:    now,
	}}

	_, err = indexer.IndexThread(ctx, thread, messages, "")
	if err == nil {
		t.Fatal("expected error from embedding failure, got nil")
	}
	if !errors.Is(err, ErrEmbeddingFailed) {
		t.Errorf("expected ErrEmbeddingFailed, got: %v", err)
	}

	// Verify the knowledge item was rolled back (not left in DB without vectors).
	item, getErr := db.GetKnowledgeItem(ctx, "email:rollback-test")
	if getErr != nil {
		t.Fatalf("GetKnowledgeItem returned unexpected error: %v", getErr)
	}
	if item != nil {
		t.Error("knowledge item was not rolled back after embedding failure")
	}
}

// drainBroker drains an events broker subscription with a short timeout,
// returning all observed events. Used by priority-mail emission tests.
func drainBroker(t *testing.T, ch <-chan events.Event, settle time.Duration) []events.Event {
	t.Helper()
	deadline := time.After(settle)
	var out []events.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

func TestEmailIndexer_EmitsPriorityMail_WhenAccountingAndAmount(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypePriorityMail)

	indexer := NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger)
	indexer.SetBroker(broker)

	now := time.Now()
	thread := &Thread{
		ID:           "thread-tva-1",
		Subject:      "Déclaration TVA - 0x0800 - 1er trimestre 2026",
		Participants: []string{"compta@example.test"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	msg := Message{
		ID:       "msg-1",
		ThreadID: thread.ID,
		From:     "compta@example.test",
		To:       []string{"client@example.com"},
		Date:     now,
		Subject:  thread.Subject,
		Body: `Cher Client,
Sur base des documents reçus, nous avons envoyé la déclaration TVA.
Montant : 7 421,85 €
A payer avant le 25 avril 2026
IBAN : BE68 5390 0754 7034
Communication : +++090/9337/55493+++`,
	}

	if _, err := indexer.IndexThread(ctx, thread, []Message{msg}, "acc-1"); err != nil {
		t.Fatalf("IndexThread: %v", err)
	}

	got := drainBroker(t, sub, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected 1 priority_mail event, got %d", len(got))
	}
	evt := got[0]
	if evt.Type != events.EventTypePriorityMail {
		t.Errorf("Type = %q, want %q", evt.Type, events.EventTypePriorityMail)
	}
	if evt.Source != "email:thread-tva-1" {
		t.Errorf("Source = %q, want email:thread-tva-1", evt.Source)
	}
	if amount, _ := evt.Data["amount"].(string); amount != "7421.85 EUR" {
		t.Errorf("Data[amount] = %q, want 7421.85 EUR", amount)
	}
	if iban, _ := evt.Data["iban"].(string); iban != "BE68539007547034" {
		t.Errorf("Data[iban] = %q, want BE68539007547034", iban)
	}
	if due, _ := evt.Data["due_date"].(string); due != "25 avril 2026" {
		t.Errorf("Data[due_date] = %q, want 25 avril 2026", due)
	}
}

func TestEmailIndexer_DoesNotEmit_WhenNoAmountOrDueDate(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypePriorityMail)

	indexer := NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger)
	indexer.SetBroker(broker)

	now := time.Now()
	thread := &Thread{
		ID:           "thread-noise",
		Subject:      "Newsletter TVA — réforme à venir",
		Participants: []string{"news@example.test"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	// Has TVA keyword (high_priority=true) and accounting domain, but
	// neither an amount nor a due date — should NOT emit.
	msg := Message{
		ID:       "msg-1",
		ThreadID: thread.ID,
		From:     "news@example.test",
		Date:     now,
		Subject:  thread.Subject,
		Body:     "Bonjour, voici notre newsletter mensuelle sur la TVA. Pas d'action requise.",
	}

	if _, err := indexer.IndexThread(ctx, thread, []Message{msg}, "acc-1"); err != nil {
		t.Fatalf("IndexThread: %v", err)
	}

	got := drainBroker(t, sub, 100*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("expected no events, got %d: %+v", len(got), got)
	}
}

func TestEmailIndexer_DoesNotEmit_WhenNotHighPriority(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypePriorityMail)

	indexer := NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger)
	indexer.SetBroker(broker)

	now := time.Now()
	thread := &Thread{
		ID:           "thread-personal",
		Subject:      "Restaurant ce soir ?",
		Participants: []string{"friend@example.com"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	// Mentions an amount but isn't accounting — should NOT emit.
	msg := Message{
		ID:       "msg-1",
		ThreadID: thread.ID,
		From:     "friend@example.com",
		Date:     now,
		Subject:  thread.Subject,
		Body:     "Le menu est à 35 EUR par personne, ça te va ?",
	}

	if _, err := indexer.IndexThread(ctx, thread, []Message{msg}, "acc-1"); err != nil {
		t.Fatalf("IndexThread: %v", err)
	}

	got := drainBroker(t, sub, 100*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("expected no events for non-priority mail, got %d: %+v", len(got), got)
	}
}

func TestEmailIndexer_DoesNotEmit_OnDuplicate(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypePriorityMail)

	indexer := NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger)
	indexer.SetBroker(broker)

	now := time.Now()
	thread := &Thread{
		ID:           "thread-tva-dup",
		Subject:      "Déclaration TVA - 1er trimestre 2026",
		Participants: []string{"compta@example.test"},
		DateRange:    [2]time.Time{now, now},
		MessageCount: 1,
	}
	msg := Message{
		ID:       "msg-1",
		ThreadID: thread.ID,
		From:     "compta@example.test",
		Date:     now,
		Subject:  thread.Subject,
		Body:     "Montant : 5 000,00 € à payer avant le 25 avril 2026 IBAN : BE68 5390 0754 7034",
	}

	// First insert: emits.
	if _, err := indexer.IndexThread(ctx, thread, []Message{msg}, "acc-1"); err != nil {
		t.Fatalf("first IndexThread: %v", err)
	}
	first := drainBroker(t, sub, 100*time.Millisecond)
	if len(first) != 1 {
		t.Fatalf("first index expected 1 event, got %d", len(first))
	}

	// Second insert with identical content: status="duplicate", must NOT emit.
	if _, err := indexer.IndexThread(ctx, thread, []Message{msg}, "acc-1"); err != nil {
		t.Fatalf("second IndexThread: %v", err)
	}
	second := drainBroker(t, sub, 100*time.Millisecond)
	if len(second) != 0 {
		t.Errorf("second index (duplicate) expected 0 events, got %d", len(second))
	}
}
