package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// IndexResult contains the result of indexing an email thread.
type IndexResult struct {
	// ContentID is the unique identifier for the indexed content.
	ContentID string

	// ChunkCount is the number of chunks created from the thread.
	ChunkCount int

	// Status indicates the result: "indexed", "duplicate", or "updated".
	Status string
}

// EmailIndexer indexes email threads into the knowledge store.
type EmailIndexer struct {
	store            *store.DB
	normalizer       *ThreadNormalizer
	embeddingService *llm.EmbeddingService
	autoTagger       *ingest.AutoTagger
	broker           *events.Broker
	logger           zerolog.Logger
}

// NewEmailIndexer creates a new EmailIndexer.
// The embeddingService parameter can be nil if embeddings are not needed.
// The broker parameter can be nil to disable downstream event emission
// (priority_mail events) — convenient for tests and reindex tooling.
func NewEmailIndexer(store *store.DB, normalizer *ThreadNormalizer, embSvc *llm.EmbeddingService, logger zerolog.Logger) *EmailIndexer {
	var autoTagger *ingest.AutoTagger
	if store != nil {
		autoTagger = ingest.NewAutoTagger(store)
	}
	return &EmailIndexer{
		store:            store,
		normalizer:       normalizer,
		embeddingService: embSvc,
		autoTagger:       autoTagger,
		logger:           logger,
	}
}

// SetBroker attaches an events broker so the indexer can emit priority_mail
// events for freshly-indexed actionable emails. Pass nil to detach.
func (idx *EmailIndexer) SetBroker(b *events.Broker) {
	idx.broker = b
}

// CountItems returns the total number of email knowledge_items in the DB.
// Returns 0 if the store is nil or if the query fails.
func (idx *EmailIndexer) CountItems(ctx context.Context, accountID, provider string) int64 {
	if idx.store == nil {
		return 0
	}
	count, _, err := idx.store.CountMailItemsByAccount(ctx, accountID, provider)
	if err != nil {
		return 0
	}
	return count
}

// IndexThread indexes a complete email thread into the knowledge store.
// It normalizes the thread content, checks for duplicates, creates chunks,
// and optionally generates embeddings.
// accountID tags the resulting KnowledgeItem with the owner account; pass ""
// when the account is unknown or not relevant.
func (idx *EmailIndexer) IndexThread(ctx context.Context, thread *Thread, messages []Message, accountID string) (*IndexResult, error) {
	if idx.store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if idx.normalizer == nil {
		return nil, fmt.Errorf("normalizer is required")
	}
	if thread == nil {
		return nil, fmt.Errorf("thread is required")
	}

	// Step 1: Normalize the thread
	normalizedText, err := idx.normalizer.Normalize(thread, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize thread: %w", err)
	}

	if len(normalizedText) < 100 {
		idx.logger.Warn().
			Str("thread_id", thread.ID).
			Int("text_len", len(normalizedText)).
			Msg("normalized mail text suspiciously short")
	}

	// Step 2: Create ContentID
	contentID := "email:" + thread.ID

	// Step 3: Compute content hash
	contentHash := hashContent(normalizedText)
	versionID := contentHash[:16]

	// Step 4: Check for existing item
	existing, err := idx.store.GetKnowledgeItem(ctx, contentID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing item: %w", err)
	}

	if existing != nil {
		// Check if content hash matches (duplicate)
		if existing.VersionID == versionID {
			idx.logger.Debug().Str("content_id", contentID).Msg("duplicate: content unchanged, skipping")
			return &IndexResult{
				ContentID:  contentID,
				ChunkCount: 0,
				Status:     "duplicate",
			}, nil
		}

		// Content changed, delete old item and re-index
		idx.logger.Info().Str("content_id", contentID).Msg("content changed - re-indexing")
		if err := idx.store.DeleteKnowledgeItem(ctx, contentID); err != nil {
			return nil, fmt.Errorf("failed to delete existing item: %w", err)
		}
	}

	// Determine status
	status := "indexed"
	if existing != nil {
		status = "updated"
	}

	// Step 5: Create KnowledgeItem
	now := time.Now()

	// Extract mail-specific fields for search/sort
	mailFrom := ""
	if len(thread.Participants) > 0 {
		mailFrom = thread.Participants[0]
	}
	if len(messages) > 0 && messages[0].From != "" {
		mailFrom = messages[0].From
	}

	// Use the newest date in the thread as the mail_date for sorting
	mailDate := thread.DateRange[1] // DateRange[1] is newest
	if mailDate.IsZero() && len(messages) > 0 {
		mailDate = messages[0].Date
	}

	metadata := map[string]any{
		"thread_id":       thread.ID,
		"participants":    thread.Participants,
		"message_count":   thread.MessageCount,
		"date_range":      formatDateRange(thread.DateRange),
		"has_attachments": thread.HasAttachments,
		"content_hash":    contentHash,
		// Mail-specific fields for search and temporal sorting
		"mail_from":    mailFrom,
		"mail_date":    mailDate.Format(time.RFC3339),
		"mail_subject": thread.Subject,
		// canonical_date is the primary date field used by retrieval for freshness ranking
		"canonical_date": mailDate.UTC().Format(time.RFC3339),
		// Label/account provenance — enables per-label and per-account retrieval
		"gmail_labels": thread.Labels,
	}
	if accountID != "" {
		metadata["account_id"] = accountID
	}

	// Tier 1 entity extraction (regex, ~0ms). Writes IBAN, amounts, structured
	// communications, VAT numbers, phones, URLs into metadata for retrieval-time
	// filtering and high_priority detection.
	tier1, highPriority := enrichMetadataWithTier1(metadata, thread.Subject, normalizedText, mailFrom)

	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "email",
		Title:          thread.Subject,
		NormalizedText: normalizedText,
		Metadata:       metadata,
		VersionID:      versionID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := idx.store.InsertKnowledgeItem(ctx, item); err != nil {
		return nil, fmt.Errorf("failed to insert knowledge item: %w", err)
	}

	// Step 6: Chunk the text
	legacyChunks := ingest.ChunkText(normalizedText, ingest.DefaultChunkOptions())

	// Step 7: Insert chunks
	var storeChunks []store.Chunk
	for _, legacyChunk := range legacyChunks {
		chunkID := uuid.New().String()
		chunkHash := hashContent(legacyChunk.Content)

		chunk := &store.Chunk{
			ChunkID:   chunkID,
			ContentID: contentID,
			ChunkHash: chunkHash,
			Text:      legacyChunk.Content,
			Metadata: map[string]any{
				"index":        legacyChunk.Index,
				"start_offset": legacyChunk.StartOffset,
				"end_offset":   legacyChunk.EndOffset,
			},
			CreatedAt: now,
		}

		if err := idx.store.InsertChunk(ctx, chunk); err != nil {
			idx.logger.Error().Err(err).Int("index", legacyChunk.Index).Msg("failed to insert chunk")
			return nil, fmt.Errorf("failed to insert chunk %d: %w", legacyChunk.Index, err)
		}

		storeChunks = append(storeChunks, *chunk)
	}

	// Log successful indexing
	idx.logger.Info().
		Str("content_id", contentID).
		Str("title", item.Title).
		Int("chunks", len(storeChunks)).
		Str("version_id", versionID).
		Msg("successfully indexed email thread")

	// Step 8: Generate embeddings. On failure, roll back to keep DB clean —
	// a KnowledgeItem without vectors is worse than no item at all.
	if idx.embeddingService != nil && len(storeChunks) > 0 {
		if embErr := idx.embeddingService.BatchEmbedAndStore(ctx, storeChunks); embErr != nil {
			idx.logger.Error().
				Err(embErr).
				Str("content_id", contentID).
				Int("chunks", len(storeChunks)).
				Msg("embedding failed; rolling back knowledge item")
			if delErr := idx.store.DeleteKnowledgeItem(ctx, contentID); delErr != nil {
				idx.logger.Error().
					Err(delErr).
					Str("content_id", contentID).
					Msg("rollback of failed knowledge item failed; DB may be inconsistent")
			}
			return nil, fmt.Errorf("%w: indexing %s: %v", ErrEmbeddingFailed, contentID, embErr)
		}
	}

	// Step 9: Apply auto-tags for mail
	if idx.autoTagger != nil {
		// Extract sender email from first participant or first message
		senderEmail := ""
		if len(thread.Participants) > 0 {
			senderEmail = thread.Participants[0]
		}
		if len(messages) > 0 && messages[0].From != "" {
			senderEmail = messages[0].From
		}

		// Extract mailbox path from labels
		mailboxPath := ""
		if len(thread.Labels) > 0 {
			mailboxPath = strings.Join(thread.Labels, "/")
		}

		// Apply mail auto-tags
		_, _ = idx.autoTagger.TagMail(ctx, contentID, senderEmail, mailboxPath)
	}

	// Step 10: Emit priority_mail event if this is a fresh insert AND the
	// email is actionable (high_priority + amount or due_date present).
	// Only "indexed" status fires — re-indexes ("updated") are noisy and
	// duplicates already returned earlier in the function.
	if idx.broker != nil && status == "indexed" && highPriority {
		hasAmount := len(tier1.Amounts) > 0
		hasDueDate := len(tier1.DueDates) > 0
		if hasAmount || hasDueDate {
			payload := events.PriorityMailPayload{
				ContentID: contentID,
				Title:     thread.Subject,
				From:      mailFrom,
			}
			if hasAmount {
				a := tier1.Amounts[0]
				payload.Amount = a.Value + " " + a.Currency
			}
			if hasDueDate {
				payload.DueDate = tier1.DueDates[0]
			}
			if len(tier1.IBANs) > 0 {
				payload.IBAN = tier1.IBANs[0]
			}
			idx.broker.Publish(events.NewPriorityMailEvent(payload))
		}
	}

	return &IndexResult{
		ContentID:  contentID,
		ChunkCount: len(legacyChunks),
		Status:     status,
	}, nil
}

// hashContent returns the SHA-256 hash of the text as a hex string.
func hashContent(text string) string {
	h := sha256.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// formatDateRange converts a DateRange to a serializable format.
func formatDateRange(dateRange [2]time.Time) map[string]string {
	return map[string]string{
		"oldest": dateRange[0].Format(time.RFC3339),
		"newest": dateRange[1].Format(time.RFC3339),
	}
}

// BatchIndexConfig configures batch indexing behavior.
type BatchIndexConfig struct {
	// BatchSize is the number of threads to process in each batch.
	// Default: 10
	BatchSize int

	// MaxConcurrent is the maximum number of concurrent indexing goroutines.
	// Default: 3
	MaxConcurrent int

	// Timeout is the timeout for indexing each thread.
	// Default: 30 seconds
	Timeout time.Duration

	// Limit is the maximum number of threads to index in total.
	// Default: 100 (0 = unlimited)
	Limit int

	// AccountID tags every indexed KnowledgeItem with the owner account.
	AccountID string

	// LabelIDs is forwarded to ListOptions.LabelIDs when fetching threads.
	LabelIDs []string
}

// DefaultBatchIndexConfig returns the default batch indexing configuration.
func DefaultBatchIndexConfig() BatchIndexConfig {
	return BatchIndexConfig{
		BatchSize:     10,
		MaxConcurrent: 3,
		Timeout:       30 * time.Second,
		Limit:         100,
	}
}

// IndexStats contains statistics from a batch indexing operation.
type IndexStats struct {
	// TotalThreads is the total number of threads to process.
	TotalThreads int `json:"total_threads"`

	// ProcessedThreads is the number of threads processed (success or error).
	ProcessedThreads int `json:"processed_threads"`

	// IndexedMessages is the total number of messages successfully indexed.
	IndexedMessages int `json:"indexed_messages"`

	// SkippedDuplicates is the number of threads that were already indexed.
	SkippedDuplicates int `json:"skipped_duplicates"`

	// UpdatedThreads is the number of threads that were re-indexed due to changes.
	UpdatedThreads int `json:"updated_threads"`

	// Errors is the number of threads that failed to index.
	Errors int `json:"errors"`

	// EmbeddingErrors is the number of threads that failed specifically due to
	// embedding generation errors (subset of Errors).
	EmbeddingErrors int `json:"embedding_errors"`

	// ErrorMessages contains error details for failed threads.
	ErrorMessages []string `json:"error_messages,omitempty"`

	// StartedAt is the timestamp when indexing started.
	StartedAt time.Time `json:"started_at"`

	// FinishedAt is the timestamp when indexing completed.
	FinishedAt time.Time `json:"finished_at"`

	// Duration is the total time taken in seconds.
	Duration float64 `json:"duration_seconds"`
}

// MailboxIndexer extends EmailIndexer with batch indexing capabilities.
// It requires a MailConnector to fetch threads from a mailbox.
type MailboxIndexer struct {
	*EmailIndexer
	connector MailConnector
}

// NewMailboxIndexer creates a new MailboxIndexer for batch operations.
func NewMailboxIndexer(indexer *EmailIndexer, connector MailConnector) *MailboxIndexer {
	return &MailboxIndexer{
		EmailIndexer: indexer,
		connector:    connector,
	}
}

// IndexMailbox indexes all threads from a mailbox using concurrent processing.
// It fetches threads in batches and processes them with limited concurrency.
func (mi *MailboxIndexer) IndexMailbox(ctx context.Context, source string, mailbox string, config BatchIndexConfig) (*IndexStats, error) {
	// Apply defaults
	if config.BatchSize <= 0 {
		config.BatchSize = 10
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 3
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}

	// If no explicit AccountID was supplied, derive it from the source parameter.
	if config.AccountID == "" {
		config.AccountID = source
	}

	stats := &IndexStats{
		StartedAt: time.Now(),
	}

	if mi.connector == nil {
		return stats, fmt.Errorf("mail connector is required")
	}

	// Pre-flight: verify the embedding endpoint is reachable before fetching any
	// emails. Failing fast here avoids fetching hundreds of threads only to roll
	// them all back when the embedding call fails.
	if mi.embeddingService != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ok, pingErr := mi.embeddingService.GetClient().PingEmbedding(pingCtx)
		cancel()
		if !ok || pingErr != nil {
			stats.FinishedAt = time.Now()
			stats.Duration = stats.FinishedAt.Sub(stats.StartedAt).Seconds()
			if pingErr != nil {
				return stats, fmt.Errorf("embedding service unreachable (LM Studio may be offline): %w", pingErr)
			}
			return stats, fmt.Errorf("embedding service unreachable — LM Studio must be running before syncing mail")
		}
	}

	// Validate that stored embedding dimensions are consistent with the current model.
	// If the model changed, abort early to avoid mixing incompatible vectors.
	if mi.embeddingService != nil {
		if dimErr := mi.embeddingService.ValidateDimensionConsistency(ctx); dimErr != nil {
			stats.FinishedAt = time.Now()
			stats.Duration = stats.FinishedAt.Sub(stats.StartedAt).Seconds()
			return stats, fmt.Errorf("embedding model mismatch; sync aborted: %w", dimErr)
		}
	}

	// Fetch threads from the mailbox up to the limit
	threads, err := mi.fetchAllThreads(ctx, mailbox, config)
	if err != nil {
		stats.FinishedAt = time.Now()
		stats.Duration = stats.FinishedAt.Sub(stats.StartedAt).Seconds()
		return stats, fmt.Errorf("failed to fetch threads: %w", err)
	}

	stats.TotalThreads = len(threads)

	if len(threads) == 0 {
		stats.FinishedAt = time.Now()
		stats.Duration = stats.FinishedAt.Sub(stats.StartedAt).Seconds()
		return stats, nil
	}

	// Process threads with concurrency control using inline sync imports
	mi.processThreadsConcurrently(ctx, threads, config, stats)

	stats.FinishedAt = time.Now()
	stats.Duration = stats.FinishedAt.Sub(stats.StartedAt).Seconds()

	// Truncate error messages to avoid massive responses
	const maxErrors = 10
	if len(stats.ErrorMessages) > maxErrors {
		stats.ErrorMessages = append(stats.ErrorMessages[:maxErrors],
			fmt.Sprintf("... and %d more errors", len(stats.ErrorMessages)-maxErrors))
	}

	return stats, nil
}

// fetchAllThreads retrieves threads from a mailbox up to the specified limit.
// Pagination is delegated to each connector's ListThreads implementation.
func (mi *MailboxIndexer) fetchAllThreads(ctx context.Context, mailbox string, config BatchIndexConfig) ([]Thread, error) {
	opts := ListOptions{
		Limit:     config.Limit,
		MailboxID: mailbox,
		LabelIDs:  config.LabelIDs,
	}
	threads, err := mi.connector.ListThreads(ctx, opts)
	if err != nil {
		return nil, err
	}
	if config.Limit > 0 && len(threads) > config.Limit {
		return threads[:config.Limit], nil
	}
	return threads, nil
}

// processThreadsConcurrently processes threads with bounded concurrency.
// It uses a semaphore pattern to limit the number of concurrent goroutines.
func (mi *MailboxIndexer) processThreadsConcurrently(ctx context.Context, threads []Thread, config BatchIndexConfig, stats *IndexStats) {
	sem := make(chan struct{}, config.MaxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, thread := range threads {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot

		go func(t Thread) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore slot

			// Create timeout context for this thread
			threadCtx, cancel := context.WithTimeout(ctx, config.Timeout)
			defer cancel()

			// Fetch messages for this thread using UIDs when available
			messages, err := mi.connector.GetMessagesByThread(threadCtx, &t)
			if err != nil {
				mu.Lock()
				stats.Errors++
				stats.ErrorMessages = append(stats.ErrorMessages,
					fmt.Sprintf("thread %s: failed to fetch messages: %v", t.ID, err))
				stats.ProcessedThreads++
				mu.Unlock()
				return
			}

			// Index the thread
			result, err := mi.IndexThread(threadCtx, &t, messages, config.AccountID)
			if err != nil {
				mu.Lock()
				stats.Errors++
				if errors.Is(err, ErrEmbeddingFailed) {
					stats.EmbeddingErrors++
				}
				stats.ErrorMessages = append(stats.ErrorMessages,
					fmt.Sprintf("thread %s: failed to index: %v", t.ID, err))
				stats.ProcessedThreads++
				mu.Unlock()
				return
			}

			mu.Lock()
			stats.ProcessedThreads++
			switch result.Status {
			case "indexed":
				stats.IndexedMessages += t.MessageCount
			case "duplicate":
				stats.SkippedDuplicates++
			case "updated":
				stats.UpdatedThreads++
				stats.IndexedMessages += t.MessageCount
			}
			mu.Unlock()
		}(thread)
	}

	wg.Wait()
}
