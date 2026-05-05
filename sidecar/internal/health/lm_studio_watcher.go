// Package health provides background watchers that emit events when the
// reachability of an external dependency changes.
package health

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/rs/zerolog"
)

// Pinger is the slim interface a watcher needs from a real LLM client.
// Defining it here (not just *llm.Client) makes the watcher trivially
// testable with a fake.
type Pinger interface {
	Ping(ctx context.Context) (bool, error)
}

// LMStudioWatcher polls a Pinger on a fixed interval and emits an event on
// the broker each time the up/down status flips. The first observation
// always produces an event (transition from "unknown").
type LMStudioWatcher struct {
	client   Pinger
	broker   *events.Broker
	url      string
	interval time.Duration
	timeout  time.Duration
	logger   zerolog.Logger

	// last is read/written atomically so callers can observe the current
	// status without taking a lock.
	last atomic.Value // events.LMStudioStatus
}

// Options configures the watcher. URL is informational only — surfaced in the
// event payload so the macOS app can display "LM Studio at http://… is down".
type Options struct {
	URL      string
	Interval time.Duration // default 10s
	Timeout  time.Duration // default 3s; should be < Interval
}

// New constructs a watcher. Returns nil when client or broker is nil.
func New(client Pinger, broker *events.Broker, opts Options, logger zerolog.Logger) *LMStudioWatcher {
	if client == nil || broker == nil {
		return nil
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Second
	}
	w := &LMStudioWatcher{
		client:   client,
		broker:   broker,
		url:      opts.URL,
		interval: opts.Interval,
		timeout:  opts.Timeout,
		logger:   logger.With().Str("component", "lm_studio_watcher").Logger(),
	}
	w.last.Store(events.LMStudioStatusUnknown)
	return w
}

// Start launches the background loop. Returns immediately. The loop exits
// when ctx is cancelled.
func (w *LMStudioWatcher) Start(ctx context.Context) {
	if w == nil {
		return
	}
	go w.run(ctx)
}

// LastStatus returns the most recently observed status. Useful for surfacing
// the current state in HTTP /health output without waiting for the next flip.
func (w *LMStudioWatcher) LastStatus() events.LMStudioStatus {
	if w == nil {
		return events.LMStudioStatusUnknown
	}
	v, _ := w.last.Load().(events.LMStudioStatus)
	if v == "" {
		return events.LMStudioStatusUnknown
	}
	return v
}

func (w *LMStudioWatcher) run(ctx context.Context) {
	// Initial probe — fires immediately so the first event isn't delayed
	// by a full interval at startup.
	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick performs a single ping with timeout and emits a flip event if the
// observed status differs from the cached one.
func (w *LMStudioWatcher) tick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()

	start := time.Now()
	ok, err := w.client.Ping(ctx)
	latency := time.Since(start)

	current := events.LMStudioStatusUp
	if !ok || err != nil {
		current = events.LMStudioStatusDown
	}

	previous, _ := w.last.Load().(events.LMStudioStatus)
	if previous == current {
		return // no change, no event
	}
	w.last.Store(current)

	w.logger.Info().
		Str("status", string(current)).
		Str("previous", string(previous)).
		Dur("latency", latency).
		Msg("LM Studio status changed")

	w.broker.Publish(events.NewLMStudioEvent(events.LMStudioStatusPayload{
		Status:    current,
		URL:       w.url,
		LatencyMs: latency.Milliseconds(),
	}))
}
