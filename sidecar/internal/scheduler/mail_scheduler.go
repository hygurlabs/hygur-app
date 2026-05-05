// Package scheduler provides background job scheduling for periodic tasks.
package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/mail"
)

// MailProvider provides access to mail connectors and indexer.
type MailProvider interface {
	GetConnectedSources() []string
	GetConnector(name string) mail.MailConnector
	GetIndexer() *mail.EmailIndexer
	GetLabelLister(name string) (mail.LabelLister, bool)
}

// MailIndexScheduler handles periodic mail indexing.
type MailIndexScheduler struct {
	provider MailProvider
	interval time.Duration
	limit    int

	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc
	lastRun time.Time
	lastErr error
	stats   SchedulerStats
}

// SchedulerStats holds statistics about the scheduler's activity.
type SchedulerStats struct {
	TotalRuns       int       `json:"total_runs"`
	SuccessfulRuns  int       `json:"successful_runs"`
	FailedRuns      int       `json:"failed_runs"`
	TotalIndexed    int       `json:"total_indexed"`
	LastRunTime     time.Time `json:"last_run_time,omitempty"`
	LastRunDuration float64   `json:"last_run_duration_seconds,omitempty"`
	NextRunTime     time.Time `json:"next_run_time,omitempty"`
	IsRunning       bool      `json:"is_running"`
}

// NewMailIndexScheduler creates a new mail index scheduler.
// interval: how often to run (e.g., 15 * time.Minute)
// limit: max messages per mailbox per run (0 = unlimited)
func NewMailIndexScheduler(provider MailProvider, interval time.Duration, limit int) *MailIndexScheduler {
	if interval < time.Minute {
		interval = time.Minute // Minimum 1 minute
	}
	if limit <= 0 {
		limit = 100 // Default limit per mailbox
	}
	return &MailIndexScheduler{
		provider: provider,
		interval: interval,
		limit:    limit,
	}
}

// Start begins the periodic indexing.
func (s *MailIndexScheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	go s.run(ctx)
	log.Printf("[MailScheduler] Started with interval=%v, limit=%d", s.interval, s.limit)
}

// Stop halts the periodic indexing.
func (s *MailIndexScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	log.Printf("[MailScheduler] Stopped")
}

// IsRunning returns whether the scheduler is active.
func (s *MailIndexScheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Stats returns the current scheduler statistics.
func (s *MailIndexScheduler) Stats() SchedulerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := s.stats
	stats.IsRunning = s.running
	if s.running {
		stats.NextRunTime = s.lastRun.Add(s.interval)
	}
	return stats
}

// TriggerNow triggers an immediate indexing run (non-blocking).
func (s *MailIndexScheduler) TriggerNow() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.indexAllSources(ctx)
	}()
}

func (s *MailIndexScheduler) run(ctx context.Context) {
	// Run immediately on start
	s.indexAllSources(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.indexAllSources(ctx)
		}
	}
}

func (s *MailIndexScheduler) indexAllSources(ctx context.Context) {
	startTime := time.Now()

	s.mu.Lock()
	s.lastRun = startTime
	s.stats.TotalRuns++
	s.mu.Unlock()

	log.Printf("[MailScheduler] Starting indexing run...")

	indexer := s.provider.GetIndexer()
	if indexer == nil {
		log.Printf("[MailScheduler] No indexer available")
		return
	}

	// Get all connected mail sources
	sources := s.provider.GetConnectedSources()
	if len(sources) == 0 {
		log.Printf("[MailScheduler] No connected mail sources")
		s.mu.Lock()
		s.stats.SuccessfulRuns++
		s.stats.LastRunTime = startTime
		s.stats.LastRunDuration = time.Since(startTime).Seconds()
		s.mu.Unlock()
		return
	}

	totalIndexed := 0
	var runErr error

	for _, sourceName := range sources {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[MailScheduler] Indexing source: %s", sourceName)

		// Get the connector for this source
		connector := s.provider.GetConnector(sourceName)
		if connector == nil {
			log.Printf("[MailScheduler] No connector for source: %s", sourceName)
			continue
		}

		// Create a MailboxIndexer for this source
		mailboxIndexer := mail.NewMailboxIndexer(indexer, connector)

		// Index INBOX and common labels
		mailboxes := []string{"INBOX", "Sent", "Archive"}

		config := mail.DefaultBatchIndexConfig()
		config.Limit = s.limit

		for _, mailbox := range mailboxes {
			result, err := mailboxIndexer.IndexMailbox(ctx, sourceName, mailbox, config)
			if err != nil {
				log.Printf("[MailScheduler] ERROR indexing %s/%s: %v", sourceName, mailbox, err)
				runErr = err
				continue
			}

			if result.EmbeddingErrors > 0 {
				log.Printf("[MailScheduler] WARN %s/%s: %d embedding failures (items not indexed)",
					sourceName, mailbox, result.EmbeddingErrors)
			}
			log.Printf("[MailScheduler] Indexed %s/%s: %d messages, %d skipped",
				sourceName, mailbox, result.IndexedMessages, result.SkippedDuplicates)
			totalIndexed += result.IndexedMessages
		}

		// Also index Gmail labels if available
		if lister, ok := s.provider.GetLabelLister(sourceName); ok {
			labels, err := lister.ListLabels(ctx)
			if err == nil {
				labelConfig := mail.DefaultBatchIndexConfig()
				labelConfig.Limit = s.limit / 2 // Lower limit for labels

				for _, label := range labels {
					// Skip system labels we already indexed
					if label.Name == "INBOX" || label.Name == "Sent" || label.Name == "SENT" {
						continue
					}
					// Index user labels (type: user)
					if label.Type == "user" {
						result, err := mailboxIndexer.IndexMailbox(ctx, sourceName, label.Name, labelConfig)
						if err != nil {
							log.Printf("[MailScheduler] ERROR indexing label %s/%s: %v", sourceName, label.Name, err)
							continue
						}
						if result.EmbeddingErrors > 0 {
							log.Printf("[MailScheduler] WARN label %s/%s: %d embedding failures",
								sourceName, label.Name, result.EmbeddingErrors)
						}
						log.Printf("[MailScheduler] Indexed label %s/%s: %d messages", sourceName, label.Name, result.IndexedMessages)
						totalIndexed += result.IndexedMessages
					}
				}
			}
		}
	}

	duration := time.Since(startTime)

	s.mu.Lock()
	s.lastErr = runErr
	s.stats.LastRunTime = startTime
	s.stats.LastRunDuration = duration.Seconds()
	s.stats.TotalIndexed += totalIndexed
	if runErr == nil {
		s.stats.SuccessfulRuns++
	} else {
		s.stats.FailedRuns++
	}
	s.mu.Unlock()

	log.Printf("[MailScheduler] Completed indexing run: %d messages indexed in %.1fs", totalIndexed, duration.Seconds())
}
