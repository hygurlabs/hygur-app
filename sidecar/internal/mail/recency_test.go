package mail

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/store"
)

// accountingThread builds an actionable accounting mail (keyword + amount +
// due date) dated `when` — the kind that would normally fire priority_mail.
func accountingThread(when time.Time) (*Thread, []Message) {
	thread := &Thread{
		ID:           "thread-tva-recency",
		Subject:      "Déclaration TVA - 1er trimestre",
		Participants: []string{"compta@example.test"},
		DateRange:    [2]time.Time{when, when},
		MessageCount: 1,
	}
	msg := Message{
		ID:       "msg-1",
		ThreadID: thread.ID,
		From:     "compta@example.test",
		To:       []string{"client@example.com"},
		Date:     when,
		Subject:  thread.Subject,
		Body:     "Déclaration TVA envoyée. Montant : 1 200,00 € à payer avant le 25 du mois.",
	}
	return thread, []Message{msg}
}

func TestRecencyGate_BlocksOldMailEmitsRecent(t *testing.T) {
	ctx := context.Background()

	run := func(when time.Time) int {
		db, err := store.NewDB(":memory:")
		if err != nil {
			t.Fatalf("NewDB: %v", err)
		}
		defer db.Close()
		broker := events.NewBroker()
		sub := broker.SubscribeFor(events.EventTypePriorityMail)
		idx := NewEmailIndexer(db, NewThreadNormalizer(), nil, testLogger)
		idx.SetBroker(broker)
		idx.SetNotifyRecencyDays(14)

		thread, msgs := accountingThread(when)
		if _, err := idx.IndexThread(ctx, thread, msgs, "acc-1"); err != nil {
			t.Fatalf("IndexThread: %v", err)
		}
		return len(drainBroker(t, sub, 100*time.Millisecond))
	}

	// A year-old accounting mail (the reported bug) must NOT notify...
	if n := run(time.Now().AddDate(-1, 0, 0)); n != 0 {
		t.Errorf("year-old mail should not emit, got %d events", n)
	}
	// ...while a fresh one still does.
	if n := run(time.Now().Add(-2 * 24 * time.Hour)); n != 1 {
		t.Errorf("recent mail should emit 1 event, got %d", n)
	}
}

func TestMailIsRecentEnough(t *testing.T) {
	idx := NewEmailIndexer(nil, nil, nil, testLogger)

	now := time.Now()
	cases := []struct {
		name string
		days int
		date time.Time
		want bool
	}{
		{"gate disabled passes old mail", 0, now.AddDate(-1, 0, 0), true},
		{"negative gate disabled", -1, now.AddDate(-1, 0, 0), true},
		{"recent mail within window", 14, now.Add(-3 * 24 * time.Hour), true},
		{"old mail outside window", 14, now.AddDate(-1, 0, 0), false},
		{"exactly at edge passes", 14, now.Add(-13 * 24 * time.Hour), true},
		{"just past edge fails", 14, now.Add(-15 * 24 * time.Hour), false},
		{"undated mail with gate on is dropped", 14, time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idx.SetNotifyRecencyDays(c.days)
			if got := idx.mailIsRecentEnough(c.date); got != c.want {
				t.Errorf("mailIsRecentEnough(days=%d, date=%v) = %v, want %v",
					c.days, c.date, got, c.want)
			}
		})
	}
}
