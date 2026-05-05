package scheduler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/agenda"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// AgendaScheduler checks upcoming deadlines once a day at 08:00 local time
// and emits an agenda_alert event for each high-priority action due within
// the next 48 hours.
type AgendaScheduler struct {
	store     *store.DB
	extractor *agenda.Extractor
	broker    *events.Broker
	hourLocal string // e.g. "08:00"
	logger    zerolog.Logger
}

// NewAgendaScheduler creates an AgendaScheduler. Returns nil when any required
// dependency is missing — callers should treat that as "feature disabled".
func NewAgendaScheduler(
	db *store.DB,
	ext *agenda.Extractor,
	broker *events.Broker,
	hourLocal string,
	logger zerolog.Logger,
) *AgendaScheduler {
	if db == nil || ext == nil || broker == nil {
		return nil
	}
	if hourLocal == "" {
		hourLocal = "08:00"
	}
	return &AgendaScheduler{
		store:     db,
		extractor: ext,
		broker:    broker,
		hourLocal: hourLocal,
		logger:    logger.With().Str("component", "agenda_scheduler").Logger(),
	}
}

// Start launches the background scheduling loop. It exits when ctx is done.
// No-op when the scheduler is nil.
func (s *AgendaScheduler) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go s.scheduleLoop(ctx)
}

func (s *AgendaScheduler) scheduleLoop(ctx context.Context) {
	for {
		next := agendaNextOccurrence(time.Now(), s.hourLocal)
		s.logger.Info().Time("next_run", next).Msg("agenda scheduler scheduled")

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}

		if err := s.Run(ctx); err != nil {
			s.logger.Warn().Err(err).Msg("agenda scheduler run failed")
		}
	}
}

// Run is the main work unit: list recent items, extract high-priority actions
// due within 48 h, and publish an agenda_alert event for each.
func (s *AgendaScheduler) Run(ctx context.Context) error {
	items, err := s.store.ListRecentItems(ctx, 48)
	if err != nil {
		return err
	}

	actions, err := s.extractor.ExtractActions(ctx, items)
	if err != nil {
		s.logger.Warn().Err(err).Msg("agenda extraction failed in scheduler")
		return nil // fail-soft
	}

	now := time.Now()
	cutoff := now.Add(48 * time.Hour)
	cutoffStr := cutoff.Format("2006-01-02")

	for _, a := range actions {
		if a.Priority != "high" {
			continue
		}
		if a.DeadlineISO > cutoffStr {
			continue
		}

		payload := events.AgendaAlertPayload{
			What:     a.What,
			Deadline: a.DeadlineISO,
			SourceID: a.SourceID,
			Priority: a.Priority,
		}
		data, _ := marshalAgendaPayload(payload)

		s.broker.Publish(events.Event{
			Type:      events.EventTypeAgendaAlert,
			Source:    "agenda_scheduler",
			Status:    events.StatusCompleted,
			Message:   "Deadline imminente : " + a.What,
			Data:      data,
			CreatedAt: now,
		})

		s.logger.Info().
			Str("what", a.What).
			Str("deadline", a.DeadlineISO).
			Msg("agenda alert published")
	}
	return nil
}

// marshalAgendaPayload converts an AgendaAlertPayload to map[string]any for
// the broker Event.Data field.
func marshalAgendaPayload(p events.AgendaAlertPayload) (map[string]any, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// agendaNextOccurrence returns the next wall-clock time at hh:mm strictly
// after now. Falls back to 08:00 on parse error.
func agendaNextOccurrence(now time.Time, hourLocal string) time.Time {
	parts := strings.SplitN(hourLocal, ":", 2)
	if len(parts) != 2 {
		parts = []string{"08", "00"}
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		h, m = 8, 0
	}

	candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}
