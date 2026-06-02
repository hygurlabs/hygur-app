package llm

import (
	"context"
	"fmt"

	"github.com/hygur/sidecar/internal/store"
)

// EmbeddingService orchestrates embedding generation and storage.
type EmbeddingService struct {
	client *Client
	store  *store.DB
}

// NewEmbeddingService creates a new embedding service.
func NewEmbeddingService(client *Client, db *store.DB) *EmbeddingService {
	return &EmbeddingService{
		client: client,
		store:  db,
	}
}

// EmbedAndStore generates an embedding for the given text and stores it.
func (s *EmbeddingService) EmbedAndStore(ctx context.Context, chunkID, text string) error {
	if chunkID == "" {
		return fmt.Errorf("chunkID cannot be empty")
	}
	if text == "" {
		return fmt.Errorf("text cannot be empty")
	}

	embedding, err := s.client.GenerateEmbedding(ctx, text)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	if err := s.store.InsertChunkVector(ctx, chunkID, embedding); err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}

	return nil
}

// BatchEmbedAndStore generates embeddings for multiple chunks and stores them.
// Processes in batches of MaxBatchSize to avoid timeouts.
func (s *EmbeddingService) BatchEmbedAndStore(ctx context.Context, chunks []store.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	// Process in batches of the configured size (fewer HTTP round-trips on
	// bulk indexing; see DefaultEmbeddingBatchSize / lm_studio.embedding_batch_size).
	step := s.client.EmbeddingBatchSize()
	for i := 0; i < len(chunks); i += step {
		end := i + step
		if end > len(chunks) {
			end = len(chunks)
		}

		batch := chunks[i:end]
		if err := s.processBatch(ctx, batch); err != nil {
			return fmt.Errorf("failed to process batch starting at index %d: %w", i, err)
		}
	}

	return nil
}

// processBatch handles a single batch of chunks.
func (s *EmbeddingService) processBatch(ctx context.Context, chunks []store.Chunk) error {
	// Extract texts
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Text
	}

	// Generate embeddings
	embeddings, err := s.client.GenerateEmbeddings(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to generate embeddings: %w", err)
	}

	// Build vectors map for batch insert
	vectors := make(map[string][]float32, len(chunks))
	for i, chunk := range chunks {
		if embeddings[i] != nil {
			vectors[chunk.ChunkID] = embeddings[i]
		}
	}

	// Store all vectors
	if err := s.store.BatchInsertChunkVectors(ctx, vectors); err != nil {
		return fmt.Errorf("failed to store embeddings: %w", err)
	}

	return nil
}

// SearchSimilar finds chunks similar to the given text.
func (s *EmbeddingService) SearchSimilar(ctx context.Context, text string, limit int) ([]store.VecResult, error) {
	if text == "" {
		return nil, fmt.Errorf("search text cannot be empty")
	}

	// Generate embedding for query
	queryVec, err := s.client.GenerateEmbedding(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search for similar chunks
	results, err := s.store.SearchChunksVec(ctx, queryVec, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search chunks: %w", err)
	}

	return results, nil
}

// ValidateDimensionConsistency checks whether vectors already stored in the DB
// are consistent with ExpectedEmbeddingDimension. Returns a non-nil error if a
// mismatch is detected. Returns nil when the DB is empty (no vectors yet).
func (s *EmbeddingService) ValidateDimensionConsistency(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	stored, err := s.store.GetMaxEmbeddingDimension(ctx)
	if err != nil {
		return fmt.Errorf("dimension check: %w", err)
	}
	if stored == 0 {
		return nil // no vectors yet, any model is fine
	}
	if stored != ExpectedEmbeddingDimension {
		return fmt.Errorf("%w: stored=%d expected=%d (embedding model may have changed)",
			ErrDimensionMismatch, stored, ExpectedEmbeddingDimension)
	}
	return nil
}

// GetClient returns the underlying LLM client.
func (s *EmbeddingService) GetClient() *Client {
	return s.client
}

// GetStore returns the underlying store.
func (s *EmbeddingService) GetStore() *store.DB {
	return s.store
}
