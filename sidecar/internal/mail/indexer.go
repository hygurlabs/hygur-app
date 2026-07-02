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

	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// maxAttachmentTextChars caps how much extracted attachment text is appended to
// a single mail's indexed body — enough for an invoice/recharge statement
// without letting a giant PDF dominate the document.
const maxAttachmentTextChars = 40_000

// maxThreadMessages caps how many messages a single "thread" may contain before
// the indexer skips it. Generic-subject buckets (Re:/no-subject/newsletters)
// mis-grouped by subject in a large All Mail folder can balloon to thousands of
// messages; fetching all their full bodies at once is a multi-GB memory bomb.
// Real conversations are far smaller, so anything past this is indexing noise.
const maxThreadMessages = 500

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
	// notifyRecencyDays bounds priority_mail emission to mail received within
	// this many days. <= 0 disables the gate (emit regardless of mail age).
	notifyRecencyDays int
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

// SetNotifyRecencyDays bounds priority_mail emission to mail received within
// the given number of days. <= 0 disables the recency gate.
func (idx *EmailIndexer) SetNotifyRecencyDays(days int) {
	idx.notifyRecencyDays = days
}

// mailIsRecentEnough reports whether a mail of the given date may trigger a
// notification. With the gate disabled (days <= 0) everything passes; with it
// enabled, an undated mail is treated as too old (we won't notify on mail we
// can't date — that's exactly the backfill case we want to suppress).
func (idx *EmailIndexer) mailIsRecentEnough(mailDate time.Time) bool {
	if idx.notifyRecencyDays <= 0 {
		return true
	}
	if mailDate.IsZero() {
		return false
	}
	return time.Since(mailDate) <= time.Duration(idx.notifyRecencyDays)*24*time.Hour
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

// CountAllMail returns the total number of indexed email items across every
// account and provider. Used to seed the unified connector's item count for the
// UI, since the per-account CountItems can't represent the aggregate (rows are
// tagged with a real account id, not the provider name).
func (idx *EmailIndexer) CountAllMail(ctx context.Context) int64 {
	if idx.store == nil {
		return 0
	}
	n, err := idx.store.CountKnowledgeItemsBySourceTypes(ctx, store.MailSourceTypes)
	if err != nil {
		return 0
	}
	return int64(n)
}

// ReconcileAccount deletes indexed mail items of accountID whose content_id is
// not in `seen` (the set of "email:<threadID>" ids present in the latest full
// sweep). Returns the number of pruned items. Callers must only invoke this
// after an UNBOUNDED sweep, or valid items would be wrongly deleted.
func (idx *EmailIndexer) ReconcileAccount(ctx context.Context, accountID string, seen map[string]struct{}) (int, error) {
	if idx.store == nil {
		return 0, fmt.Errorf("store is required")
	}
	existing, err := idx.store.ListMailContentIDsByAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range existing {
		if _, ok := seen[id]; ok {
			continue
		}
		if err := idx.store.DeleteKnowledgeItem(ctx, id); err != nil {
			idx.logger.Warn().Err(err).Str("content_id", id).Msg("reconcile: delete failed")
			continue
		}
		removed++
	}
	return removed, nil
}

// extractAttachmentText parses the downloaded PDF attachments across all
// messages and returns their concatenated text (capped at
// maxAttachmentTextChars). Returns "" when no attachment bytes are present or
// nothing parses. Best-effort — per-attachment failures are logged and skipped.
func (idx *EmailIndexer) extractAttachmentText(ctx context.Context, messages []Message) string {
	var b strings.Builder
	// Text-only + process-isolated: the pure-Go PDF parser can allocate tens of
	// GB on a malformed/bomb PDF (the cause of the sidecar's RAM blowups), so
	// each attachment is parsed in a short-lived, heap-capped child process that
	// is killed on timeout. OCR is never run inline (it would saturate the
	// inference model and stall the sync). Failures yield "" and are skipped.
	for _, msg := range messages {
		for _, att := range msg.Attachments {
			if len(att.Data) == 0 || !IsPDFAttachment(att) {
				continue
			}
			// The PDF parser returns RAW text; collapse it here so mail keeps the
			// exact normalized attachment text it always stored (mail is unaffected
			// by the notes/files raw_text change).
			text := ingest.NormalizeText(parsers.ExtractPDFTextIsolated(ctx, att.Data, parsers.DefaultPDFExtractTimeout))
			if strings.TrimSpace(text) == "" {
				continue
			}
			name := att.Filename
			if name == "" {
				name = "document.pdf"
			}
			b.WriteString("\n[Pièce jointe : ")
			b.WriteString(name)
			b.WriteString("]\n")
			b.WriteString(strings.TrimSpace(text))
			b.WriteString("\n")
			if b.Len() >= maxAttachmentTextChars {
				return strings.TrimSpace(b.String())
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// IndexThread indexes a complete email thread into the knowledge store.
// It normalizes the thread content, checks for duplicates, creates chunks,
// and optionally generates embeddings.
// accountID tags the resulting KnowledgeItem with the owner account; pass ""
// when the account is unknown or not relevant.
// provider (optional) stamps a provider-scoped source_ref ("<provider>:<threadID>",
// e.g. "gmail:..."), the key used by recycle-bin deletion reconciliation; omit it
// (legacy callers) to leave source_ref unset.
func (idx *EmailIndexer) IndexThread(ctx context.Context, thread *Thread, messages []Message, accountID string, provider ...string) (*IndexResult, error) {
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

	// Step 1b: Append text extracted from PDF attachments whose bytes the
	// connector downloaded. Many statements (recharge totals, invoices) put the
	// key figures ONLY in the attached PDF; without this they'd be invisible to
	// both FTS and embeddings. Best-effort — extraction failures are logged and
	// skipped, never fatal.
	if attText := idx.extractAttachmentText(ctx, messages); attText != "" {
		normalizedText = normalizedText + "\n\n" + attText
	}
	// Release attachment bytes now that their text is extracted: each can be
	// several MB and is unused downstream. Holding them across the whole batch
	// of fetched threads is the main driver of the sidecar's RAM during mail
	// backfills, so free them per-thread to let the GC reclaim eagerly.
	for i := range messages {
		for j := range messages[i].Attachments {
			messages[i].Attachments[j].Data = nil
		}
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
	// source_ref: provider-scoped reconcile key ("<provider>:<threadID>"). Additive —
	// only set when the caller knows the provider; legacy callers leave it unset.
	if len(provider) > 0 && provider[0] != "" {
		metadata["source_ref"] = provider[0] + ":" + thread.ID
	}

	// Tier 1 entity extraction (regex, ~0ms). Writes IBAN, amounts, structured
	// communications, VAT numbers, phones, URLs into metadata for retrieval-time
	// filtering and high_priority detection.
	tier1, highPriority := enrichMetadataWithTier1(metadata, thread.Subject, normalizedText, mailFrom)

	item := &store.KnowledgeItem{
		ContentID: contentID,
		// Unified on "mail" (the connector/edge value) so both ingestion paths
		// agree; legacy "email" rows are still read via store.MailSourceTypes.
		SourceType:     store.SourceTypeMail,
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

	// Steps 6-8: Chunk the thread into hierarchical sections + embed-sized
	// chunks and persist them via the single shared indexing path, then embed.
	// On failure, roll back to keep the DB clean — a KnowledgeItem without
	// vectors is worse than no item at all.
	secCount, chunkCount, idxErr := ingest.IndexSections(ctx, idx.store, idx.embeddingService, contentID, normalizedText, ingest.DefaultChunkTokenBudget, now)
	if idxErr != nil {
		idx.logger.Error().
			Err(idxErr).
			Str("content_id", contentID).
			Msg("indexing failed; rolling back knowledge item")
		if delErr := idx.store.DeleteKnowledgeItem(ctx, contentID); delErr != nil {
			idx.logger.Error().
				Err(delErr).
				Str("content_id", contentID).
				Msg("rollback of failed knowledge item failed; DB may be inconsistent")
		}
		return nil, fmt.Errorf("%w: indexing %s: %v", ErrEmbeddingFailed, contentID, idxErr)
	}

	idx.logger.Info().
		Str("content_id", contentID).
		Str("title", item.Title).
		Int("sections", secCount).
		Int("chunks", chunkCount).
		Str("version_id", versionID).
		Msg("successfully indexed email thread")

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

	// Step 10: Emit a priority_mail candidate when this is a fresh insert, the
	// mail was RECEIVED recently (recency gate — stops a backfill of last
	// year's mail from notifying), it is high-priority (accounting keywords /
	// known sender), AND it is actionable (amount or due date). The post-sync
	// digest then has the LLM judge each candidate for real notification-
	// worthiness, vetoing receipts/confirmations and writing the one-liner.
	// Only "indexed" status fires — re-indexes are noisy and duplicates
	// already returned earlier in the function.
	if idx.broker != nil && status == "indexed" && highPriority && idx.mailIsRecentEnough(mailDate) {
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
		ChunkCount: chunkCount,
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

	// Provider, when set ("gmail"|"proton"|…), stamps a provider-scoped source_ref
	// on each indexed item, enabling recycle-bin deletion reconciliation.
	Provider string

	// ReconcileDeletions, when true AND Limit==0 (a full unbounded sweep),
	// removes previously-indexed mail of this account that wasn't seen in the
	// sweep — i.e. messages deleted/spammed on the server. Gated on the
	// unbounded sweep so a capped sync never purges items beyond the cap.
	ReconcileDeletions bool

	// LabelIDs is forwarded to ListOptions.LabelIDs when fetching threads.
	LabelIDs []string

	// Since, when non-zero, limits the fetch to threads newer than this time
	// (incremental sync) — forwarded to ListOptions.Since so the connector does
	// a SINCE search instead of re-fetching the most-recent Limit threads every
	// run. Zero means a full fetch.
	Since time.Time
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

	// Mail-deletion reconciliation (opt-in, full unbounded sweeps only): remove
	// indexed mail of this account the server no longer returns — deleted/spam
	// messages stop polluting retrieval. Gated on Limit<=0 so a capped sync
	// never purges valid items beyond the cap.
	if config.ReconcileDeletions && config.Limit <= 0 && config.AccountID != "" {
		seen := make(map[string]struct{}, len(threads))
		for _, t := range threads {
			seen["email:"+t.ID] = struct{}{}
		}
		if removed, rerr := mi.ReconcileAccount(ctx, config.AccountID, seen); rerr != nil {
			mi.logger.Warn().Err(rerr).Str("account", config.AccountID).Msg("mail reconcile failed")
		} else if removed > 0 {
			mi.logger.Info().Int("removed", removed).Str("account", config.AccountID).Msg("mail reconcile: pruned deleted items")
		}
	}

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
	if !config.Since.IsZero() {
		s := config.Since
		opts.Since = &s
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

	// Progress reporter: emit a throttled sync event (~1/s) carrying
	// processed/total/eta so the UI can show a live loading bar. Also keeps the
	// activity indicator's watchdog alive during long syncs.
	progressDone := make(chan struct{})
	if mi.broker != nil && len(threads) > 0 {
		go mi.reportSyncProgress(ctx, stats, &mu, config.AccountID, progressDone)
	}

	for _, thread := range threads {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot

		go func(t Thread) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore slot

			// Create timeout context for this thread
			threadCtx, cancel := context.WithTimeout(ctx, config.Timeout)
			defer cancel()

			// Guard against pathologically large "threads": generic-subject
			// buckets (Re:/no-subject/newsletters) mis-grouped in All Mail can
			// hold thousands of messages, and fetching all their full bodies at
			// once is a multi-GB memory bomb (the cause of the sidecar's RAM
			// blowup). A real conversation is rarely >maxThreadMessages, so skip
			// these — they're indexing noise, not conversations.
			if t.MessageCount > maxThreadMessages {
				mi.logger.Warn().
					Str("thread_id", t.ID).
					Int("messages", t.MessageCount).
					Msg("skipping oversized thread to bound sync memory")
				mu.Lock()
				stats.ProcessedThreads++
				stats.SkippedDuplicates++
				mu.Unlock()
				return
			}

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
			result, err := mi.IndexThread(threadCtx, &t, messages, config.AccountID, config.Provider)
			if err != nil {
				retriable := errors.Is(err, ErrEmbeddingFailed)
				mu.Lock()
				stats.Errors++
				if retriable {
					stats.EmbeddingErrors++
				}
				stats.ErrorMessages = append(stats.ErrorMessages,
					fmt.Sprintf("thread %s: failed to index: %v", t.ID, err))
				stats.ProcessedThreads++
				mu.Unlock()
				// R1: a transient embedding failure must not become a SILENT
				// permanent gap in the KB. Park the thread in the retry queue,
				// drained before the next incremental sync. Re-indexing dedups by
				// content hash, so recovery yields no duplicate item.
				if retriable && mi.store != nil && config.AccountID != "" {
					next := time.Now().Add(backoffFor(0))
					if qerr := mi.store.EnqueueIndexRetry(threadCtx, config.Provider, config.AccountID, t.ID, "embedding_failed", err.Error(), next); qerr != nil {
						mi.logger.Warn().Err(qerr).Str("thread_id", t.ID).Msg("failed to enqueue index retry")
					}
				}
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
	close(progressDone)

	// Final 100% tick so the bar completes before the connector's "completed"
	// event clears it.
	if mi.broker != nil && len(threads) > 0 {
		mi.emitSyncProgress(config.AccountID, len(threads), len(threads), 0)
	}
}

// reportSyncProgress emits a sync event roughly once per second with the
// processed/total thread counts and an ETA, until processing finishes.
func (mi *MailboxIndexer) reportSyncProgress(ctx context.Context, stats *IndexStats, mu *sync.Mutex, source string, done <-chan struct{}) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			mu.Lock()
			processed, total, started := stats.ProcessedThreads, stats.TotalThreads, stats.StartedAt
			mu.Unlock()
			mi.emitSyncProgress(source, processed, total, etaSeconds(started, processed, total))
		}
	}
}

// emitSyncProgress publishes a running sync event carrying progress + ETA for
// the UI loading bar. No-op when the broker is unset.
func (mi *MailboxIndexer) emitSyncProgress(source string, processed, total int, eta float64) {
	if mi.broker == nil {
		return
	}
	mi.broker.PublishWithType(events.EventTypeSync, events.StatusRunning, source, "syncing mail",
		map[string]any{"processed": processed, "total": total, "eta_seconds": eta})
}

// etaSeconds estimates the remaining seconds from the processing rate so far.
// Returns 0 when it can't be computed yet (no progress, or already complete).
func etaSeconds(start time.Time, processed, total int) float64 {
	if processed <= 0 || total <= processed || start.IsZero() {
		return 0
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	rate := float64(processed) / elapsed // threads/sec
	if rate <= 0 {
		return 0
	}
	return float64(total-processed) / rate
}

// retryBackoff is the per-attempt delay before re-trying a parked index. It
// climbs so a persistently-failing item backs off instead of spinning, capped at
// 24h so a recovered embedder still heals the backlog within a day.
var retryBackoff = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// maxRetryAttempts bounds re-tries. Past it the gap is logged at ERROR (no longer
// silent — the whole point of R1) and the row dropped; a full re-sync remains the
// ultimate recovery.
const maxRetryAttempts = 8

// backoffFor returns the delay for the next attempt given how many have already
// been made (clamped to the last bucket).
func backoffFor(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts >= len(retryBackoff) {
		return retryBackoff[len(retryBackoff)-1]
	}
	return retryBackoff[attempts]
}

// DrainRetryQueue re-indexes threads parked in the index_retry queue for this
// (connectorID, accountID), to be called BEFORE the incremental window — so a
// thread that failed to embed during an earlier sync is recovered without a full
// re-sync (RELIABILITY_BACKLOG R1). Re-indexing flows through IndexThread's
// content-hash dedup, so a recovered thread yields exactly one knowledge item,
// never a duplicate. Skips entirely when the embedder is unreachable (no point
// burning the attempt budget during a global outage). Returns
// (indexed, requeued, dropped).
func (mi *MailboxIndexer) DrainRetryQueue(ctx context.Context, connectorID, accountID string, limit int) (indexed, requeued, dropped int) {
	if mi.store == nil || accountID == "" {
		return 0, 0, 0
	}
	// Don't drain into a known-down embedder: it would only re-fail every queued
	// item and exhaust their attempt budget. The main sync's pre-flight makes the
	// same check; this one protects the dead-letter cap.
	if mi.embeddingService != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ok, perr := mi.embeddingService.GetClient().PingEmbedding(pingCtx)
		cancel()
		if !ok || perr != nil {
			return 0, 0, 0
		}
	}

	now := time.Now()
	due, err := mi.store.DueIndexRetries(ctx, connectorID, accountID, now, limit)
	if err != nil {
		mi.logger.Warn().Err(err).Str("account", accountID).Msg("index retry: failed to read queue")
		return 0, 0, 0
	}

	for _, r := range due {
		next := now.Add(backoffFor(r.Attempts + 1))

		th, err := mi.connector.GetThread(ctx, r.SourceRef)
		if err != nil {
			if errors.Is(err, ErrThreadNotFound) {
				// Vanished from the server since it failed — nothing to recover.
				_ = mi.store.DeleteIndexRetry(ctx, connectorID, accountID, r.SourceRef)
				dropped++
				continue
			}
			_ = mi.store.BumpIndexRetry(ctx, connectorID, accountID, r.SourceRef, next, err.Error())
			requeued++
			continue
		}

		messages, err := mi.connector.GetMessagesByThread(ctx, th)
		if err != nil {
			if errors.Is(err, ErrThreadNotFound) {
				_ = mi.store.DeleteIndexRetry(ctx, connectorID, accountID, r.SourceRef)
				dropped++
				continue
			}
			_ = mi.store.BumpIndexRetry(ctx, connectorID, accountID, r.SourceRef, next, err.Error())
			requeued++
			continue
		}

		_, ierr := mi.IndexThread(ctx, th, messages, accountID, connectorID)
		switch {
		case ierr == nil:
			_ = mi.store.DeleteIndexRetry(ctx, connectorID, accountID, r.SourceRef)
			indexed++
		case !errors.Is(ierr, ErrEmbeddingFailed):
			// Permanent (parse/normalize) — not retriable; drop + log so it's
			// visible rather than spinning forever.
			mi.logger.Warn().Err(ierr).Str("thread_id", r.SourceRef).Msg("index retry: permanent failure, dropping")
			_ = mi.store.DeleteIndexRetry(ctx, connectorID, accountID, r.SourceRef)
			dropped++
		case r.Attempts+1 >= maxRetryAttempts:
			// Give up — but LOUDLY: a KB gap is never left silent.
			mi.logger.Error().Err(ierr).Str("thread_id", r.SourceRef).Int("attempts", r.Attempts+1).
				Msg("index retry exhausted; thread left unindexed (run a full re-sync to recover)")
			_ = mi.store.DeleteIndexRetry(ctx, connectorID, accountID, r.SourceRef)
			dropped++
		default:
			_ = mi.store.BumpIndexRetry(ctx, connectorID, accountID, r.SourceRef, next, ierr.Error())
			requeued++
		}
	}

	if indexed > 0 || requeued > 0 || dropped > 0 {
		mi.logger.Info().Str("account", accountID).
			Int("indexed", indexed).Int("requeued", requeued).Int("dropped", dropped).
			Msg("index retry queue drained")
	}
	return indexed, requeued, dropped
}
