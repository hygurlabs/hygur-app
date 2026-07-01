package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/extract"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// MaxFileSize is the maximum allowed file size for ingestion (50MB).
const MaxFileSize int64 = 50 * 1024 * 1024

// Common errors returned by the ingestor.
var (
	ErrPathTraversal     = errors.New("path traversal detected")
	ErrNotAbsolutePath   = errors.New("path must be absolute")
	ErrSymlinkNotAllowed = errors.New("symlinks are not allowed")
	ErrFileTooLarge      = errors.New("file exceeds maximum size")
	ErrNoParser          = errors.New("no parser available for file type")
	ErrEmptyContent      = errors.New("parsed content is empty")
)

// IngestOptions configures the ingestion behavior.
type IngestOptions struct {
	// ProjectID associates the content with a specific project.
	ProjectID *string

	// Tags are labels to attach to the ingested content.
	Tags []string
}

// IngestResult contains the result of an ingestion operation.
type IngestResult struct {
	// ContentID is the unique identifier for the ingested content.
	ContentID string

	// Status indicates the result: "indexed", "duplicate", or "near_duplicate".
	Status string

	// ChunkCount is the number of chunks created from the content.
	ChunkCount int
}

// Ingestor orchestrates the document ingestion pipeline.
type Ingestor struct {
	mu               sync.RWMutex
	parsers          map[string]Parser // extension -> parser
	store            *store.DB
	embeddingService *llm.EmbeddingService
	llmClient        *llm.Client // optional, used for embeddings + fallback Tier 2 NER
	indexingClient   *llm.Client // optional, fast small model for Tier 2 NER; falls back to llmClient
	autoTagger       *AutoTagger
	broker           *events.Broker // optional; nil disables event emission
}

// NewIngestor creates a new Ingestor instance.
func NewIngestor() *Ingestor {
	return &Ingestor{
		parsers: make(map[string]Parser),
	}
}

// NewIngestorWithStore creates a new Ingestor with a database store for persistence.
func NewIngestorWithStore(db *store.DB) *Ingestor {
	var autoTagger *AutoTagger
	if db != nil {
		autoTagger = NewAutoTagger(db)
	}
	return &Ingestor{
		parsers:    make(map[string]Parser),
		store:      db,
		autoTagger: autoTagger,
	}
}

// NewIngestorWithEmbeddings creates a new Ingestor with store, embedding, and
// Tier 2 LLM extraction support. The same llmClient is used for both
// embeddings (via the EmbeddingService) and Tier 2 NER, since both run
// against the same LM Studio instance.
func NewIngestorWithEmbeddings(db *store.DB, llmClient *llm.Client) *Ingestor {
	var embSvc *llm.EmbeddingService
	if llmClient != nil && db != nil {
		embSvc = llm.NewEmbeddingService(llmClient, db)
	}
	var autoTagger *AutoTagger
	if db != nil {
		autoTagger = NewAutoTagger(db)
	}
	return &Ingestor{
		parsers:          make(map[string]Parser),
		store:            db,
		embeddingService: embSvc,
		llmClient:        llmClient,
		autoTagger:       autoTagger,
	}
}

// SetStore sets the database store for persistence.
func (i *Ingestor) SetStore(db *store.DB) {
	i.store = db
}

// SetEmbeddingService sets the embedding service for vector generation.
func (i *Ingestor) SetEmbeddingService(svc *llm.EmbeddingService) {
	i.embeddingService = svc
}

// SetLLMClient sets the LLM client used for embeddings and, unless an indexing
// client is set, Tier 2 NER extraction. When unset, Tier 2 is skipped silently
// (Tier 1 still runs).
func (i *Ingestor) SetLLMClient(c *llm.Client) {
	i.llmClient = c
}

// SetIndexingClient sets a dedicated (typically small, fast) LLM client for
// Tier 2 NER extraction at ingestion time. When unset, Tier 2 falls back to the
// main LLM client. Embeddings always use the main client.
func (i *Ingestor) SetIndexingClient(c *llm.Client) {
	i.indexingClient = c
}

// tier2Client returns the client to use for Tier 2 extraction: the dedicated
// indexing client when configured, otherwise the main LLM client.
func (i *Ingestor) tier2Client() *llm.Client {
	if i.indexingClient != nil {
		return i.indexingClient
	}
	return i.llmClient
}

// BackfillTier2NER re-runs Tier-2 NER (persons/orgs/projects/topics) across the corpus
// into item metadata, using the dedicated indexing model. PreserveTimestamp: it writes
// only metadata and never bumps updated_at, so a full-corpus pass can't make every item
// read as "recently modified" (which would flood updated_at-based recency queries like
// the meeting-brief tick). Idempotent — items already at the current Tier2Version are
// skipped. Run backfill-entity-index + backfill-entity-edges afterwards to fold the new
// NER entities into the index and graph. Returns items scanned.
// useMainModel runs the extraction on the larger generation model (higher-quality NER)
// instead of the small indexing model; force re-extracts even already-stamped items.
func (i *Ingestor) BackfillTier2NER(ctx context.Context, useMainModel, force bool) (int, error) {
	if i.store == nil {
		return 0, nil
	}
	client := i.tier2Client()
	if useMainModel && i.llmClient != nil {
		client = i.llmClient
	}
	if client == nil {
		return 0, nil // no LLM configured → nothing to extract
	}
	stats, err := extract.Backfill(ctx, i.store, client, extract.BackfillOptions{
		PreserveTimestamp: true, Concurrency: 4, Force: force,
	})
	if stats == nil {
		return 0, err
	}
	return stats.Total, err
}

// SetBroker wires an event broker so the ingestor emits ingest_start /
// ingest_complete around each document. Pass nil to disable emission.
func (i *Ingestor) SetBroker(b *events.Broker) {
	i.broker = b
}

// publishIngest is a tiny helper that no-ops when the broker isn't wired.
func (i *Ingestor) publishIngest(evtType events.EventType, p events.IngestPayload) {
	if i.broker == nil {
		return
	}
	i.broker.Publish(events.NewIngestEvent(evtType, p))
}

// RegisterParser registers a parser for its supported extensions.
// If an extension is already registered, it will be overwritten.
func (i *Ingestor) RegisterParser(p Parser) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, ext := range p.SupportedExtensions() {
		ext = normalizeExtension(ext)
		i.parsers[ext] = p
	}
}

// GetParser returns the parser for the given extension.
func (i *Ingestor) GetParser(ext string) Parser {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.parsers[normalizeExtension(ext)]
}

// Ingest processes a file and prepares it for indexing.
func (i *Ingestor) Ingest(ctx context.Context, path string, opts IngestOptions) (res *IngestResult, retErr error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Emit ingest_start as soon as we have a path. ingest_complete (or a
	// failed variant) is emitted from a deferred closure so every exit path
	// produces exactly one matching event with measured duration.
	startedAt := time.Now()
	sourceType := sourceTypeFromExt(filepath.Ext(path))
	i.publishIngest(events.EventTypeIngestStart, events.IngestPayload{
		Path:       path,
		SourceType: sourceType,
	})
	defer func() {
		payload := events.IngestPayload{
			Path:       path,
			SourceType: sourceType,
			DurationMs: time.Since(startedAt).Milliseconds(),
		}
		if res != nil {
			payload.ContentID = res.ContentID
		}
		if retErr != nil {
			payload.ErrorMsg = retErr.Error()
		}
		i.publishIngest(events.EventTypeIngestComplete, payload)
	}()

	// Validate the path
	if err := ValidatePath(path); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Get file info and check size
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file")
	}

	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("%w: %d bytes (max: %d)", ErrFileTooLarge, info.Size(), MaxFileSize)
	}

	// Get parser for this file type
	ext := filepath.Ext(path)
	parser := i.GetParser(ext)
	if parser == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoParser, ext)
	}

	// Check context before opening file
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Open and parse the file
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create a context-aware reader
	content, metadata, err := parser.Parse(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file: %w", err)
	}

	// Normalize the content
	normalized := NormalizeText(content)
	if normalized == "" {
		// Fail-soft for source types that may legitimately produce no text
		// (e.g. image/audio when OCR/Whisper is unavailable or returns nothing).
		// Index the item with metadata only so the file is tracked in the store.
		if sourceType == "image" || sourceType == "audio" {
			slog.WarnContext(ctx, "ingest.empty_text",
				"path", path,
				"source_type", sourceType,
			)
			return &IngestResult{
				ContentID:  uuid.New().String(),
				Status:     "indexed",
				ChunkCount: 0,
			}, nil
		}
		return nil, ErrEmptyContent
	}

	// Generate content hash for deduplication
	contentHash := hashContent(normalized)

	// Check for exact duplicate if store is available
	if i.store != nil {
		existing, err := i.store.GetKnowledgeItemByHash(ctx, contentHash)
		if err == nil && existing != nil {
			return &IngestResult{
				ContentID:  existing.ContentID,
				Status:     "duplicate",
				ChunkCount: 0,
			}, nil
		}
	}

	// Generate a content ID
	contentID := uuid.New().String()

	// Build the hierarchical sections + embed-sized chunks once. The chunk
	// count is reported even when no store is configured (parse-only path);
	// persistence + embedding happen in the store block below.
	builtSections := BuildSections(contentID, normalized, DefaultChunkTokenBudget)
	chunkCount := TotalChunks(builtSections)

	// Persist to store if available
	if i.store != nil {
		now := time.Now()

		// Create and insert knowledge item
		// Seed mtime so CanonicalDate can use it as a fallback.
		metadata["file_mtime"] = info.ModTime().Format(time.RFC3339)

		// Determine canonical date: frontmatter date > file mtime > now.
		canonicalDate := CanonicalDate(metadata, now)

		item := &store.KnowledgeItem{
			ContentID:      contentID,
			SourceType:     sourceType,
			SourcePath:     &path,
			Title:          filepath.Base(path),
			NormalizedText: normalized,
			Metadata: map[string]any{
				"content_hash":   contentHash,
				"file_size":      info.Size(),
				"canonical_date": canonicalDate.UTC().Format(time.RFC3339),
			},
			VersionID: contentHash[:16],
			CreatedAt: now,
			UpdatedAt: now,
		}

		// Merge parser metadata (canonical_date is already set; don't overwrite it)
		for k, v := range metadata {
			if k != "canonical_date" {
				item.Metadata[k] = v
			}
		}

		// Tier 1 entity extraction (regex, ~0ms). Applied uniformly to all
		// source types so that retrieval/entity_search can filter on
		// extracted_iban / extracted_amounts / extracted_vat_numbers / etc.
		// regardless of whether the document came from email, markdown, PDF,
		// or any other parser. Mail-specific enrichment (high_priority,
		// accounting_keywords) is layered on top by the mail indexer.
		//
		// Run on raw parser output (not `normalized`): IBAN/VAT regexes
		// require uppercase country prefixes that NormalizeText would have
		// lowercased.
		extract.EnrichMetadataWithTier1(item.Metadata, content)

		// Tier 2 NER extraction (LLM, ~5-15s). Launched in parallel with the
		// chunk insertion + embedding pipeline below — both touch disjoint
		// rows so there's no contention. Results are merged into metadata via
		// UpdateKnowledgeItem after both finish. Fail-soft: any LLM/timeout/
		// parse error is logged and the document is persisted without Tier 2
		// metadata; the backfill CLI can re-process it later.
		var (
			tier2Wait   *sync.WaitGroup
			tier2Result extract.Tier2Entities
			tier2Err    error
		)
		if t2 := i.tier2Client(); t2 != nil {
			tier2Wait = &sync.WaitGroup{}
			tier2Wait.Add(1)
			go func() {
				defer tier2Wait.Done()
				tier2Result, tier2Err = extract.ExtractTier2(ctx, t2, content)
			}()
		}

		if err := i.store.InsertKnowledgeItem(ctx, item); err != nil {
			return nil, fmt.Errorf("failed to insert knowledge item: %w", err)
		}

		// Persist the prebuilt sections + chunks and embed them. On failure
		// roll back the item so a KnowledgeItem never lingers without vectors.
		if _, _, idxErr := PersistSections(ctx, i.store, i.embeddingService, builtSections, now); idxErr != nil {
			_ = i.store.DeleteKnowledgeItem(context.Background(), contentID)
			return nil, fmt.Errorf("indexing failed for %s: %w", contentID, idxErr)
		}

		// Wait for Tier 2 to finish (it ran concurrently with the embedding).
		// Merge its result into metadata via UpdateKnowledgeItem. Fail-soft:
		// log on error and keep the document persisted without Tier 2 keys.
		if tier2Wait != nil {
			tier2Wait.Wait()
			if tier2Err != nil {
				log.Printf("[ingest] tier2 extraction failed for %s: %v", contentID, tier2Err)
			} else {
				extract.MergeTier2IntoMetadata(item.Metadata, tier2Result)
				if err := i.store.UpdateKnowledgeItem(ctx, item); err != nil {
					log.Printf("[ingest] tier2 metadata update failed for %s: %v", contentID, err)
				}
			}
		}

		// Link to project if specified
		if opts.ProjectID != nil && *opts.ProjectID != "" {
			link := &store.ProjectLink{
				LinkID:    uuid.New().String(),
				ProjectID: *opts.ProjectID,
				ContentID: contentID,
				CreatedAt: now,
			}
			// Ignore error if project doesn't exist
			_ = i.store.InsertProjectLink(ctx, link)
		}

		// Apply auto-tags based on folder path
		if i.autoTagger != nil {
			_, _ = i.autoTagger.TagDocument(ctx, contentID, path)
		}

		// Apply manual tags if provided
		if i.autoTagger != nil && len(opts.Tags) > 0 {
			_ = i.autoTagger.ApplyManualTags(ctx, contentID, opts.Tags)
		}
	}

	return &IngestResult{
		ContentID:  contentID,
		Status:     "indexed",
		ChunkCount: chunkCount,
	}, nil
}

// hashContent returns the SHA-256 hash of the text.
// IngestTextInput is a pre-extracted text document pushed by a client (the
// "Add files" path / future edge agent) — the server does NO file parsing.
type IngestTextInput struct {
	Title      string
	Text       string // already-extracted plain text
	SourceType string // "file" | "mail" | "note" | "event" | … (default "text")
	SourceRef  string // idempotency key, e.g. "files:/path" or "imap:<id>"
	URL        string
	Author     string
	Metadata   map[string]any // extra fields merged into the item metadata
	CreatedAt  time.Time      // zero → now
}

// IngestText indexes a text document supplied directly by a client, reusing the
// chunk → embed → store pipeline but skipping file parsing (VDSL-friendly: only
// text crosses the wire). Idempotent by SourceRef: re-pushing the same ref
// updates the existing item in place (stable content_id); identical text is a
// no-op ("duplicate"). On embed failure the item is kept (FTS-searchable) rather
// than rolled back — a failed embed must never delete content (RELIABILITY R2).
func (i *Ingestor) IngestText(ctx context.Context, in IngestTextInput) (*IngestResult, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, ErrEmptyContent
	}
	if i.store == nil {
		return nil, fmt.Errorf("store not configured")
	}

	contentHash := hashContent(text)
	now := time.Now()
	created := now
	if !in.CreatedAt.IsZero() {
		created = in.CreatedAt
	}

	contentID := uuid.New().String()
	status := "indexed"

	switch {
	case in.SourceRef != "":
		existing, err := i.store.GetKnowledgeItemBySourceRef(ctx, in.SourceRef)
		if err == nil && existing != nil {
			if h, _ := existing.Metadata["content_hash"].(string); h == contentHash {
				return &IngestResult{ContentID: existing.ContentID, Status: "duplicate", ChunkCount: 0}, nil
			}
			// Content changed for this source_ref → replace in place, keeping the
			// content_id so existing references stay stable.
			contentID = existing.ContentID
			status = "updated"
			if delErr := i.store.DeleteKnowledgeItem(ctx, existing.ContentID); delErr != nil {
				return nil, fmt.Errorf("replacing item for source_ref %q: %w", in.SourceRef, delErr)
			}
		}
	default:
		if existing, err := i.store.GetKnowledgeItemByHash(ctx, contentHash); err == nil && existing != nil {
			return &IngestResult{ContentID: existing.ContentID, Status: "duplicate", ChunkCount: 0}, nil
		}
	}

	sourceType := strings.TrimSpace(in.SourceType)
	if sourceType == "" {
		sourceType = "text"
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = text
		if nl := strings.IndexByte(title, '\n'); nl >= 0 {
			title = title[:nl]
		}
		if r := []rune(strings.TrimSpace(title)); len(r) > 80 {
			title = string(r[:80])
		}
		if strings.TrimSpace(title) == "" {
			title = "Untitled"
		}
	}

	metadata := map[string]any{
		"content_hash":   contentHash,
		"canonical_date": created.UTC().Format(time.RFC3339),
	}
	if in.SourceRef != "" {
		metadata["source_ref"] = in.SourceRef
	}
	if in.URL != "" {
		metadata["url"] = in.URL
	}
	if in.Author != "" {
		metadata["author"] = in.Author
	}
	for k, v := range in.Metadata {
		if k != "content_hash" && k != "source_ref" {
			metadata[k] = v
		}
	}

	// Tier 1 entity extraction (regex) for entity_search parity with file/mail ingest.
	extract.EnrichMetadataWithTier1(metadata, text)

	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     sourceType,
		Title:          title,
		NormalizedText: text,
		Metadata:       metadata,
		VersionID:      contentHash[:16],
		CreatedAt:      created,
		UpdatedAt:      now,
	}
	if err := i.store.InsertKnowledgeItem(ctx, item); err != nil {
		return nil, fmt.Errorf("failed to insert knowledge item: %w", err)
	}

	// Auto-tag mail pushed as text (the edge agent's Proton path). The file and
	// direct-IMAP ingest paths tag at their own ingest time, but this text-push
	// path didn't — so cloud/edge mail had no tags at all. Tags = mailbox folder
	// + semantic topics from the Tier-2 extractor, run inline so a freshly-synced
	// mail is classified immediately (one bounded LLM call per new mail).
	if store.IsMailSourceType(sourceType) {
		i.TagItem(ctx, item, true)
	}

	_, chunkCount, idxErr := IndexSections(ctx, i.store, i.embeddingService, contentID, text, DefaultChunkTokenBudget, now)
	if idxErr != nil {
		// Keep the item (chunks are FTS-indexed); do NOT roll back on embed
		// failure — that would delete content. A re-push or re-embed fills vectors.
		slog.WarnContext(ctx, "ingest_text.embed_failed", "content_id", contentID, "source_ref", in.SourceRef, "err", idxErr)
	}

	return &IngestResult{ContentID: contentID, Status: status, ChunkCount: chunkCount}, nil
}

// classifyItem returns the taxonomy categories for an item: the ones cached in
// metadata, or a fresh LLM classification. fresh=true means the caller should
// persist them. Pure of DB writes (safe to run concurrently). Empty when there's
// no indexing LLM client or no text.
func (i *Ingestor) classifyItem(ctx context.Context, item *store.KnowledgeItem) (cats []string, fresh bool) {
	if cachedFresh(item.Metadata, "mail_categories", mailCategoryVersion) {
		if cached := categoriesFromMetadata(item.Metadata); len(cached) > 0 {
			return cached, false
		}
	}
	// Classify on the MAIN model, not the small indexing model: the latter
	// follows the closed-taxonomy instruction poorly (it emitted the same garbage
	// pair for unrelated mail), whereas the main model classifies cleanly. Tier-2
	// NER stays on the small model (tier2Client) — only classification moved.
	c := i.llmClient
	if c == nil || strings.TrimSpace(item.NormalizedText) == "" {
		return nil, false
	}
	got, err := classifyMail(ctx, c, item.NormalizedText)
	if err != nil {
		log.Printf("[ingest] classify failed for %s: %v", item.ContentID, err)
		return nil, false
	}
	return got, len(got) > 0
}

// applyItemTags writes the mailbox-folder tag (mail only) + topic category tags,
// and caches fresh categories. DB writes only — in a concurrent backfill the
// caller must serialize these (SQLite is a single writer).
func (i *Ingestor) applyItemTags(ctx context.Context, item *store.KnowledgeItem, cats []string, fresh bool) {
	if mailbox, _ := item.Metadata["mailbox"].(string); mailbox != "" {
		i.autoTagger.tagMailFolder(ctx, item.ContentID, mailbox)
	}
	if fresh && len(cats) > 0 {
		item.Metadata["mail_categories"] = cats
		item.Metadata["mail_categories_version"] = mailCategoryVersion
		if uerr := i.store.UpdateKnowledgeItem(ctx, item); uerr != nil {
			log.Printf("[ingest] category metadata update failed for %s: %v", item.ContentID, uerr)
		}
	}
	i.autoTagger.tagTopics(ctx, item.ContentID, cats)
}

// TagItem classifies one mail or note into the fixed taxonomy and applies its
// tags (mailbox folder for mail + topic categories). Used inline on ingest /
// note creation. prune caps the topic-tag set afterwards.
func (i *Ingestor) TagItem(ctx context.Context, item *store.KnowledgeItem, prune bool) {
	if i.autoTagger == nil || item == nil {
		return
	}
	cats, fresh := i.classifyItem(ctx, item)
	i.applyItemTags(ctx, item, cats, fresh)
	// W6: extract + cache semantic claims (skips junk senders / Notifications).
	claims, cfresh := i.extractClaimsForItem(ctx, item, cats)
	i.applyItemClaims(ctx, item, claims, cfresh, false) // live ingestion: normal timestamp
	// Inline project suggestion (W4): cache the best-matching project so the
	// detail panel can offer "Add to <project>".
	if projects := i.activeProjects(ctx); len(projects) > 0 {
		i.suggestProjectForItem(ctx, item, projects)
	}
	if prune {
		_ = i.store.PruneAutoTags(ctx)
	}
}

// retagConcurrency bounds parallel classify (LLM) calls during a backfill. The
// indexing model handles several concurrent requests comfortably; tag writes
// stay serialized behind a mutex since SQLite is a single writer.
const retagConcurrency = 4

// RetagItems rebuilds auto-tags across all mail + notes: it purges existing
// auto-tags (dropping stale rules), then classifies + tags every item, reusing
// categories already cached in metadata. Classification runs up to
// retagConcurrency in parallel; tag writes are serialized. Pruning happens once
// at the end. Long-running — callers should run it async. Returns items processed.
func (i *Ingestor) RetagItems(ctx context.Context) (int, error) {
	if i.autoTagger == nil || i.store == nil {
		return 0, nil
	}
	if err := i.purgeAutoTags(ctx); err != nil {
		log.Printf("[ingest] retag: purge auto-tags failed: %v", err)
	}

	// Collect the corpus to tag (mail + notes).
	var items []*store.KnowledgeItem
	for _, src := range store.MailAndSourceTypes(store.SourceTypeNote) {
		const batch = 500
		for offset := 0; ; offset += batch {
			page, err := i.store.ListKnowledgeItemsBySourceType(ctx, src, batch, offset)
			if err != nil {
				return 0, err
			}
			items = append(items, page...)
			if len(page) < batch {
				break
			}
		}
	}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		sem       = make(chan struct{}, retagConcurrency)
		processed int
	)
	for _, it := range items {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *store.KnowledgeItem) {
			defer wg.Done()
			defer func() { <-sem }()
			cats, fresh := i.classifyItem(ctx, it) // LLM — runs in parallel
			mu.Lock()
			i.applyItemTags(ctx, it, cats, fresh) // DB — serialized
			processed++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	_ = i.store.PruneAutoTags(ctx)
	return processed, nil
}

// purgeAutoTags deletes every auto-generated tag (and its item links), leaving
// user-created tags untouched. Used before a full retag so the taxonomy isn't a
// mix of old and new auto-rules.
func (i *Ingestor) purgeAutoTags(ctx context.Context) error {
	tags, err := i.store.ListTags(ctx)
	if err != nil {
		return err
	}
	for _, t := range tags {
		if t.IsAuto {
			if derr := i.store.DeleteTag(ctx, t.ID); derr != nil {
				log.Printf("[ingest] retag: delete auto-tag %s failed: %v", t.ID, derr)
			}
		}
	}
	return nil
}

func hashContent(text string) string {
	h := sha256.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// sourceTypeFromExt determines the source type from file extension.
func sourceTypeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return "markdown"
	case ".pdf":
		return "pdf"
	case ".docx", ".doc":
		return "docx"
	case ".txt":
		return "txt"
	case ".html", ".htm":
		return "html"
	case ".png", ".jpg", ".jpeg", ".heic", ".webp":
		return "image"
	case ".mp3", ".m4a", ".wav", ".ogg":
		return "audio"
	default:
		return "unknown"
	}
}

// ValidatePath checks that a path is safe for ingestion.
// It verifies:
//   - The path is absolute
//   - The path contains no ".." components
//   - The path is not a symlink
func ValidatePath(path string) error {
	// Must be absolute
	if !filepath.IsAbs(path) {
		return ErrNotAbsolutePath
	}

	// Check for path traversal
	cleanPath := filepath.Clean(path)
	if strings.Contains(cleanPath, "..") {
		return ErrPathTraversal
	}

	// Check if different from original (could indicate traversal attempt)
	if cleanPath != path && filepath.Clean(path) != path {
		// Allow minor differences like trailing slashes
		if strings.TrimSuffix(path, "/") != cleanPath && strings.TrimSuffix(path, string(filepath.Separator)) != cleanPath {
			return ErrPathTraversal
		}
	}

	// Check for symlinks
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return err // File doesn't exist, let caller handle
		}
		return fmt.Errorf("failed to check path: %w", err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkNotAllowed
	}

	return nil
}

// normalizeExtension ensures the extension is lowercase and has a leading dot.
func normalizeExtension(ext string) string {
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}
