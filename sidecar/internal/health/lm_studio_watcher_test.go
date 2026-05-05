package health

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/rs/zerolog"
)

// fakePinger is a programmable Pinger that returns a sequence of (ok, err)
// pairs so tests can drive specific transitions deterministically.
type fakePinger struct {
	results []pingResult
	idx     atomic.Int32
}

type pingResult struct {
	ok  bool
	err error
}

func (f *fakePinger) Ping(_ context.Context) (bool, error) {
	i := int(f.idx.Add(1)) - 1
	if i >= len(f.results) {
		// Stay on the last value forever.
		i = len(f.results) - 1
	}
	r := f.results[i]
	return r.ok, r.err
}

func newTestLogger() zerolog.Logger {
	return zerolog.New(io.Discard).With().Timestamp().Logger()
}

// drain collects all events from a subscription within `settle`. Bounded so
// tests don't hang.
func drain(ch <-chan events.Event, settle time.Duration) []events.Event {
	var out []events.Event
	deadline := time.After(settle)
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-deadline:
			return out
		}
	}
}

func TestWatcher_FirstUpEmitsExactlyOneEvent(t *testing.T) {
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeLMStudio)
	pinger := &fakePinger{results: []pingResult{{ok: true}, {ok: true}, {ok: true}}}

	w := New(pinger, broker, Options{URL: "http://x:1234", Interval: 20 * time.Millisecond}, newTestLogger())
	if w == nil {
		t.Fatal("New returned nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	got := drain(sub, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if status, _ := got[0].Data["status"].(string); status != string(events.LMStudioStatusUp) {
		t.Errorf("status = %q, want %q", status, events.LMStudioStatusUp)
	}
	if w.LastStatus() != events.LMStudioStatusUp {
		t.Errorf("LastStatus = %q, want up", w.LastStatus())
	}
}

func TestWatcher_FirstDownEmitsExactlyOneEvent(t *testing.T) {
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeLMStudio)
	pinger := &fakePinger{results: []pingResult{{ok: false, err: errors.New("connect refused")}}}

	w := New(pinger, broker, Options{Interval: 20 * time.Millisecond}, newTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	got := drain(sub, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if status, _ := got[0].Data["status"].(string); status != string(events.LMStudioStatusDown) {
		t.Errorf("status = %q, want %q", status, events.LMStudioStatusDown)
	}
}

func TestWatcher_StableStatus_NoExtraEvents(t *testing.T) {
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeLMStudio)
	// 5 successful pings in a row → exactly 1 event total (the initial flip).
	pinger := &fakePinger{results: []pingResult{{ok: true}, {ok: true}, {ok: true}, {ok: true}, {ok: true}}}

	w := New(pinger, broker, Options{Interval: 10 * time.Millisecond}, newTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	got := drain(sub, 100*time.Millisecond)
	if len(got) != 1 {
		t.Errorf("expected 1 event for stable up, got %d", len(got))
	}
}

func TestWatcher_FlipUpDownUp_EmitsThreeEvents(t *testing.T) {
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeLMStudio)
	pinger := &fakePinger{results: []pingResult{
		{ok: true},  // flip 1: unknown → up
		{ok: false}, // flip 2: up → down
		{ok: false}, // stable
		{ok: true},  // flip 3: down → up
		{ok: true},  // stable
	}}

	w := New(pinger, broker, Options{Interval: 10 * time.Millisecond}, newTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	got := drain(sub, 200*time.Millisecond)
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	wantStatus := []string{
		string(events.LMStudioStatusUp),
		string(events.LMStudioStatusDown),
		string(events.LMStudioStatusUp),
	}
	for i, e := range got {
		if status, _ := e.Data["status"].(string); status != wantStatus[i] {
			t.Errorf("event %d status = %q, want %q", i, status, wantStatus[i])
		}
	}
}

func TestWatcher_NilArgs_ReturnsNil(t *testing.T) {
	if New(nil, events.NewBroker(), Options{}, newTestLogger()) != nil {
		t.Error("expected nil watcher when client is nil")
	}
	if New(&fakePinger{}, nil, Options{}, newTestLogger()) != nil {
		t.Error("expected nil watcher when broker is nil")
	}
}

func TestWatcher_RespectsContextCancellation(t *testing.T) {
	broker := events.NewBroker()
	pinger := &fakePinger{results: []pingResult{{ok: true}}}
	w := New(pinger, broker, Options{Interval: 10 * time.Millisecond}, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Wait long enough to let any leaked goroutine misbehave; we just
	// assert we don't panic and the logger doesn't blow up.
	time.Sleep(50 * time.Millisecond)
}
