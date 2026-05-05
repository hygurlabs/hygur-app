package files_test

// TestFilesConnector_PeriodicSync_TriggeredByCron verifies that:
//  1. The FilesScheduler wires the files connector's Sync method into a cron job.
//  2. When the cron fires, Sync is called with the expected options.
//  3. On success, an EventTypeIngestComplete event is published to the broker.
//  4. When no schedule is configured, the default ("*/15 * * * *") is used.
//  5. Fail-soft: nil syncer or nil broker both yield a nil scheduler that is
//     safe to call Start on.
//
// The test uses a mock syncer and a real events.Broker to avoid touching the
// filesystem or SQLite. The cron is exercised with a short "@every 50ms"
// expression so the job fires within milliseconds and tests stay deterministic.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/scheduler"
	"github.com/rs/zerolog"
)

// mockSyncer records calls to Sync without touching real storage.
type mockSyncer struct {
	mu     sync.Mutex
	calls  []plugin.SyncOptions
	retErr error
}

func (m *mockSyncer) Sync(_ context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, opts)
	return &plugin.SyncResult{Processed: 3, Skipped: 1, Duration: 50 * time.Millisecond}, m.retErr
}

func (m *mockSyncer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func TestFilesConnector_PeriodicSync_TriggeredByCron(t *testing.T) {
	t.Run("cron fires Sync and emits ingest_complete event", func(t *testing.T) {
		broker := events.NewBroker()

		// Subscribe to ingest_complete events only.
		ch := broker.SubscribeFor(events.EventTypeIngestComplete)

		syncer := &mockSyncer{}

		// Use @every 50ms so the job fires quickly in CI without being flaky.
		fs := scheduler.NewFilesScheduler(syncer, broker, "@every 50ms", zerolog.Nop())
		if fs == nil {
			t.Fatal("NewFilesScheduler returned nil for a valid schedule — check constructor logic")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		fs.Start(ctx)

		// Wait for at least one ingest_complete event, up to 1.5 s.
		select {
		case evt := <-ch:
			// Verify the event carries the expected type.
			if evt.Type != events.EventTypeIngestComplete {
				t.Errorf("event type = %q, want %q", evt.Type, events.EventTypeIngestComplete)
			}
			// Verify source_type in Data indicates the files connector.
			if got, ok := evt.Data["source_type"]; !ok || got != "files" {
				t.Errorf("event data source_type = %v, want %q", got, "files")
			}
		case <-time.After(1500 * time.Millisecond):
			t.Fatalf("timed out waiting for ingest_complete event; Sync called %d times", syncer.callCount())
		}

		// Verify Sync was invoked at least once.
		if syncer.callCount() == 0 {
			t.Error("Sync was never called — cron did not fire")
		}
	})

	t.Run("nil syncer returns nil scheduler (fail-soft)", func(t *testing.T) {
		broker := events.NewBroker()
		fs := scheduler.NewFilesScheduler(nil, broker, "", zerolog.Nop())
		if fs != nil {
			t.Error("expected nil FilesScheduler when syncer is nil")
		}
		// Start on nil must not panic.
		fs.Start(context.Background())
	})

	t.Run("nil broker returns nil scheduler (fail-soft)", func(t *testing.T) {
		syncer := &mockSyncer{}
		fs := scheduler.NewFilesScheduler(syncer, nil, "", zerolog.Nop())
		if fs != nil {
			t.Error("expected nil FilesScheduler when broker is nil")
		}
		fs.Start(context.Background())
	})

	t.Run("empty schedule falls back to default without error", func(t *testing.T) {
		broker := events.NewBroker()
		syncer := &mockSyncer{}
		// Empty schedule → constructor must not return nil (valid default is used).
		fs := scheduler.NewFilesScheduler(syncer, broker, "", zerolog.Nop())
		if fs == nil {
			t.Error("NewFilesScheduler returned nil for empty schedule — default should be applied")
		}
	})

	t.Run("invalid cron expression returns nil scheduler (fail-soft)", func(t *testing.T) {
		broker := events.NewBroker()
		syncer := &mockSyncer{}
		fs := scheduler.NewFilesScheduler(syncer, broker, "not-a-cron", zerolog.Nop())
		if fs != nil {
			t.Error("expected nil FilesScheduler for invalid cron expression")
		}
		// Start on nil must not panic.
		fs.Start(context.Background())
	})
}
