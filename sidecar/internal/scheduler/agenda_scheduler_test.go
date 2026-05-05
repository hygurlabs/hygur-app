package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func TestAgendaScheduler_PublishesAlerts(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Insert a high-priority item with a due date in the next 48 h.
	tomorrow := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	now := time.Now()
	item := &store.KnowledgeItem{
		ContentID:      "alert-item-1",
		SourceType:     "note",
		Title:          "Payer la facture",
		NormalizedText: "Payer avant deadline",
		Metadata: map[string]any{
			"extracted_due_dates": []interface{}{tomorrow},
			"priority":            "high",
		},
		VersionID: "v1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	ctx := context.Background()
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	broker := events.NewBroker()
	sub := broker.Subscribe()

	ext := agenda.NewExtractor(nil) // no LLM — templated path
	sched := NewAgendaScheduler(db, ext, broker, "08:00", zerolog.Nop())
	if sched == nil {
		t.Fatal("expected non-nil scheduler")
	}

	if err := sched.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Drain the subscriber channel with a short timeout.
	var received []events.Event
	deadline := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case evt := <-sub:
			received = append(received, evt)
		case <-deadline:
			break drain
		}
	}

	if len(received) == 0 {
		t.Fatal("expected at least one agenda_alert event")
	}
	for _, e := range received {
		if e.Type != events.EventTypeAgendaAlert {
			t.Errorf("unexpected event type %s", e.Type)
		}
	}
}

func TestAgendaScheduler_NilDepsReturnNil(t *testing.T) {
	sched := NewAgendaScheduler(nil, nil, nil, "", zerolog.Nop())
	if sched != nil {
		t.Error("expected nil scheduler when deps are missing")
	}
}
