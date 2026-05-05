package events

import (
	"testing"
	"time"
)

// TestBroker_DeliversIngestCompleteToSubscribers locks the contract Sprint 1
// relies on: an `ingest_complete` event published to the broker reaches both
// type-filtered subscribers and the catch-all subscribers, with its content_id
// preserved through the broker.
func TestBroker_DeliversIngestCompleteToSubscribers(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	typed := b.SubscribeFor(EventTypeIngestComplete)
	all := b.Subscribe()

	go func() {
		b.Publish(NewIngestEvent(EventTypeIngestComplete, IngestPayload{
			ContentID:  "doc:42",
			Path:       "/tmp/x.md",
			SourceType: "markdown",
			DurationMs: 123,
		}))
	}()

	select {
	case evt := <-typed:
		if evt.Type != EventTypeIngestComplete {
			t.Fatalf("typed type = %q", evt.Type)
		}
		if got, _ := evt.Data["content_id"].(string); got != "doc:42" {
			t.Fatalf("typed content_id = %v", evt.Data["content_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("typed subscriber did not receive event")
	}

	select {
	case evt := <-all:
		if evt.Type != EventTypeIngestComplete {
			t.Fatalf("catch-all type = %q", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("catch-all subscriber did not receive event")
	}
}
