package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	llmClient        *llm.Client // optional, used for Tier 2 NER extraction
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

// SetLLMClient sets the LLM client used for Tier 2 NER extraction.
// When unset, Tier 2 is skipped silently (Tier 1 still runs).
func (i *Ingestor) SetLLMClient(c *llm.Client) {
	i.llmClient = c
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

	// Chunk the content
	chunks := ChunkText(normalized, DefaultChunkOptions())

	// Generate a content ID
	contentID := uuid.New().String()

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
		if i.llmClient != nil {
			tier2Wait = &sync.WaitGroup{}
			tier2Wait.Add(1)
			go func() {
				defer tier2Wait.Done()
				tier2Result, tier2Err = extract.ExtractTier2(ctx, i.llmClient, content)
			}()
		}

		if err := i.store.InsertKnowledgeItem(ctx, item); err != nil {
			return nil, fmt.Errorf("failed to insert knowledge item: %w", err)
		}

		// Insert chunks and collect for embedding
		var storeChunks []store.Chunk
		for _, legacyChunk := range chunks {
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

			if err := i.store.InsertChunk(ctx, chunk); err != nil {
				return nil, fmt.Errorf("failed to insert chunk %d: %w", legacyChunk.Index, err)
			}

			storeChunks = append(storeChunks, *chunk)
		}

		// Generate embeddings for all chunks — mandatory. If this fails, the item
		// is unusable for semantic search, so we roll back the entire insert.
		if i.embeddingService != nil && len(storeChunks) > 0 {
			if err := i.embeddingService.BatchEmbedAndStore(ctx, storeChunks); err != nil {
				_ = i.store.DeleteKnowledgeItem(context.Background(), contentID)
				return nil, fmt.Errorf("embedding failed for %s: %w", contentID, err)
			}
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
		ChunkCount: len(chunks),
	}, nil
}

// hashContent returns the SHA-256 hash of the text.
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

// generateContentID creates a unique identifier for the content.
// This is a placeholder implementation.
func generateContentID(path string, metadata Metadata) string {
	// TODO: Use proper content hashing (e.g., SHA-256 of content)
	// For now, use a simple path-based ID
	base := filepath.Base(path)
	return fmt.Sprintf("content_%s", strings.ReplaceAll(base, ".", "_"))
}

// contextReader wraps an io.Reader to respect context cancellation.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *contextReader) Read(p []byte) (n int, err error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}

// NewContextReader wraps a reader to respect context cancellation.
func NewContextReader(ctx context.Context, r io.Reader) io.Reader {
	return &contextReader{ctx: ctx, r: r}
}
