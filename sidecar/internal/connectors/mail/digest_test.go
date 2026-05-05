package mail

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestCaptureAndPublishDigest_EmitsDigestForCapturedPriorityMail verifies
// that priority_mail events emitted *during* a sync cycle are aggregated
// into a single mail_digest event afterwards. The summarizer runs on
// real KnowledgeItem rows pulled from an in-memory DB, with no LLM (the
// templated path covers the test fixture).
func TestCaptureAndPublishDigest_EmitsDigestForCapturedPriorityMail(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mailItem := &store.KnowledgeItem{
		ContentID:      "email:digest-1",
		SourceType:     "email",
		Title:          "Facture Chargemap",
		NormalizedText: "Bonjour, votre facture s'élève à 23,50 EUR.",
		Metadata: map[string]any{
			"extracted_amounts":   []string{"23.50 EUR"},
			"extracted_due_dates": []string{"2026-07-15"},
			"extracted_orgs":      []string{"Chargemap"},
		},
		VersionID: "test",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.InsertKnowledgeItem(context.Background(), mailItem); err != nil {
		t.Fatalf("seed: %v", err)
	}

	broker := events.NewBroker()
	defer broker.Close()
	summarizer := retrieval.NewMailSummarizer(nil) // templated path only
	logger := zerolog.Nop()

	conn := &MailConnector{
		broker:     broker,
		store:      db,
		summarizer: summarizer,
		logger:     logger,
	}

	digestCh := broker.SubscribeFor(events.EventTypeMailDigest)

	cycleErr := conn.captureAndPublishDigest(context.Background(), func() error {
		// Simulate the indexer publishing a priority_mail event mid-cycle.
		broker.Publish(events.NewPriorityMailEvent(events.PriorityMailPayload{
			ContentID: "email:digest-1",
			Title:     "Facture Chargemap",
			Amount:    "23.50 EUR",
			DueDate:   "2026-07-15",
		}))
		// Give the capture goroutine a moment to drain the channel.
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if cycleErr != nil {
		t.Fatalf("cycle err: %v", cycleErr)
	}

	select {
	case evt := <-digestCh:
		count, _ := evt.Data["count"].(int)
		if count != 1 {
			t.Fatalf("digest count = %v, want 1", evt.Data["count"])
		}
		items, _ := evt.Data["items"].([]map[string]any)
		if len(items) != 1 {
			t.Fatalf("items len = %d", len(items))
		}
		oneLiner, _ := items[0]["one_liner"].(string)
		if oneLiner == "" {
			t.Fatal("one_liner is empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no mail_digest event received")
	}
}

// TestCaptureAndPublishDigest_NoDigestWhenNoPriority confirms the helper is
// silent when the cycle produced zero priority_mail events.
func TestCaptureAndPublishDigest_NoDigestWhenNoPriority(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()

	broker := events.NewBroker()
	defer broker.Close()
	conn := &MailConnector{
		broker:     broker,
		store:      db,
		summarizer: retrieval.NewMailSummarizer(nil),
		logger:     zerolog.Nop(),
	}
	digestCh := broker.SubscribeFor(events.EventTypeMailDigest)

	_ = conn.captureAndPublishDigest(context.Background(), func() error {
		return nil
	})

	select {
	case evt := <-digestCh:
		t.Fatalf("unexpected digest event: %+v", evt)
	case <-time.After(150 * time.Millisecond):
		// expected: no event
	}
}

// TestCaptureAndPublishDigest_NoOpWhenPipelineMissing exercises the early
// return when SetDigestPipeline was never called: the inner closure runs
// untouched and no goroutine subscribes.
func TestCaptureAndPublishDigest_NoOpWhenPipelineMissing(t *testing.T) {
	conn := &MailConnector{logger: zerolog.Nop()}
	called := false
	err := conn.captureAndPublishDigest(context.Background(), func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !called {
		t.Fatal("inner closure not invoked")
	}
}
