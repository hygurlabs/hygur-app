package scheduler

import (
	"context"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/plugin"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

// defaultFilesSchedule is the cron expression used when the files connector
// has no schedule configured. Every 15 minutes is a sensible balance between
// freshness and CPU/disk pressure for a local file watcher.
const defaultFilesSchedule = "*/15 * * * *"

// FilesSchedulerSyncer is the subset of plugin.Syncer consumed by FilesScheduler.
// Declared as an interface so tests can inject a mock without a real Ingestor.
type FilesSchedulerSyncer interface {
	Sync(ctx context.Context, opts plugin.SyncOptions) (*plugin.SyncResult, error)
}

// FilesScheduler runs the files connector on a cron schedule and emits an
// EventTypeIngestComplete event after each successful sync cycle.
//
// It is separate from plugin.Scheduler so that:
//   - A sensible default schedule can be applied when none is configured.
//   - A post-sync event carrying the processed count can be emitted.
//   - The concern stays contained and independently testable.
type FilesScheduler struct {
	syncer   FilesSchedulerSyncer
	broker   *events.Broker
	schedule string // validated cron expression
	cron     *cron.Cron
	logger   zerolog.Logger
}

// NewFilesScheduler constructs a FilesScheduler.
//
// configSchedule is the value from the connector's config (may be empty).
// When it is empty, defaultFilesSchedule ("*/15 * * * *") is used as fallback.
//
// Returns nil and logs a warning when:
//   - syncer or broker is nil (Fail-soft: caller continues without periodic sync).
//   - The resolved cron expression is syntactically invalid.
func NewFilesScheduler(
	syncer FilesSchedulerSyncer,
	broker *events.Broker,
	configSchedule string,
	logger zerolog.Logger,
) *FilesScheduler {
	log := logger.With().Str("component", "files_scheduler").Logger()

	if syncer == nil {
		log.Warn().Msg("files connector not available — periodic sync disabled")
		return nil
	}
	if broker == nil {
		log.Warn().Msg("event broker not available — periodic sync disabled")
		return nil
	}

	schedule := strings.TrimSpace(configSchedule)
	if schedule == "" {
		schedule = defaultFilesSchedule
		log.Info().Str("schedule", schedule).Msg("no schedule configured for files connector — using default")
	}

	// Validate expression before storing it. Use the same parser flags as
	// cron.New() so that @every, @daily, @hourly descriptors are accepted.
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := parser.Parse(schedule); err != nil {
		log.Warn().Err(err).Str("schedule", schedule).Msg("invalid cron expression for files connector — periodic sync disabled")
		return nil
	}

	return &FilesScheduler{
		syncer:   syncer,
		broker:   broker,
		schedule: schedule,
		cron:     cron.New(),
		logger:   log,
	}
}

// Start registers the cron job and launches the cron daemon in the background.
// It exits gracefully when ctx is cancelled. No-op when the scheduler is nil.
func (fs *FilesScheduler) Start(ctx context.Context) {
	if fs == nil {
		return
	}

	_, err := fs.cron.AddFunc(fs.schedule, func() {
		fs.run(ctx)
	})
	if err != nil {
		// This should not happen because we validated the expression in
		// NewFilesScheduler, but guard defensively.
		fs.logger.Error().Err(err).Str("schedule", fs.schedule).Msg("failed to register cron job — periodic sync disabled")
		return
	}

	fs.cron.Start()
	fs.logger.Info().Str("schedule", fs.schedule).Msg("files periodic sync started")

	go func() {
		<-ctx.Done()
		fs.cron.Stop()
		fs.logger.Info().Msg("files periodic sync stopped")
	}()
}

// run is the body of each cron tick. It calls Sync on the connector and, on
// success, publishes an EventTypeIngestComplete event with the processed count.
func (fs *FilesScheduler) run(ctx context.Context) {
	syncCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	fs.logger.Info().Msg("files periodic sync triggered")

	result, err := fs.syncer.Sync(syncCtx, plugin.SyncOptions{Full: false})
	if err != nil {
		fs.logger.Error().Err(err).Msg("files periodic sync failed")
		return
	}

	fs.logger.Info().
		Int("processed", result.Processed).
		Int("skipped", result.Skipped).
		Int("failed", result.Failed).
		Dur("duration", result.Duration).
		Msg("files periodic sync completed")

	// Emit a connector-level ingest_complete event so the macOS Activity view
	// can display the sync cycle without relying solely on per-document events.
	fs.broker.Publish(events.NewIngestEvent(events.EventTypeIngestComplete, events.IngestPayload{
		SourceType: "files",
		DurationMs: result.Duration.Milliseconds(),
	}))
}
