// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

// VecResult represents a vector similarity search result.
type VecResult struct {
	ChunkID   string
	ContentID string
	Score     float64 // cosine similarity (higher is better, range [-1, 1])
}

// ChunkVector represents a chunk with its embedding vector.
type ChunkVector struct {
	ChunkID   string
	Embedding []float32
	CreatedAt time.Time
}

// maxVecLimit is the maximum allowed limit for vector searches.
// Set to 1000 so that with ~2500 email chunks the top documents are never
// cut off before deduplication — the real bottleneck is in-memory cosine,
// not this cap.
const maxVecLimit = 1000

// defaultVecTimeout is the default timeout for vector operations.
const defaultVecTimeout = 10 * time.Second

// SerializeVector serializes a float32 vector to bytes using little-endian encoding.
// Each float32 takes 4 bytes, so the output length is len(vec) * 4.
func SerializeVector(vec []float32) []byte {
	if vec == nil {
		return nil
	}

	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeVector deserializes bytes to a float32 vector.
// Returns an error if the byte slice length is not a multiple of 4.
func DeserializeVector(data []byte) ([]float32, error) {
	if data == nil {
		return nil, nil
	}

	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector data length: %d (must be multiple of 4)", len(data))
	}

	vec := make([]float32, len(data)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vec, nil
}

// cosineSimilarity calculates the cosine similarity between two vectors.
// Returns a value in the range [-1, 1] where 1 means identical direction.
// Returns 0 if either vector has zero magnitude.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// InsertChunkVector inserts or updates a vector embedding for a chunk.
func (d *DB) InsertChunkVector(ctx context.Context, chunkID string, embedding []float32) error {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	if embedding == nil {
		return fmt.Errorf("embedding cannot be nil")
	}

	data := SerializeVector(embedding)

	_, err := d.db.ExecContext(ctx, `
		INSERT INTO chunk_vectors (chunk_id, embedding, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chunk_id) DO UPDATE SET
			embedding = excluded.embedding,
			created_at = CURRENT_TIMESTAMP
	`, chunkID, data)
	if err != nil {
		return fmt.Errorf("failed to insert chunk vector: %w", err)
	}

	return nil
}

// GetChunkVector retrieves the embedding vector for a chunk.
// Returns nil, nil if the chunk has no vector.
func (d *DB) GetChunkVector(ctx context.Context, chunkID string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	var data []byte
	err := d.db.QueryRowContext(ctx, `
		SELECT embedding FROM chunk_vectors WHERE chunk_id = ?
	`, chunkID).Scan(&data)

	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get chunk vector: %w", err)
	}

	return DeserializeVector(data)
}

// DeleteChunkVector deletes the embedding vector for a chunk.
func (d *DB) DeleteChunkVector(ctx context.Context, chunkID string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	_, err := d.db.ExecContext(ctx, `
		DELETE FROM chunk_vectors WHERE chunk_id = ?
	`, chunkID)
	if err != nil {
		return fmt.Errorf("failed to delete chunk vector: %w", err)
	}

	return nil
}

// SearchChunksVec performs a vector similarity search using cosine similarity.
// Returns the top-k chunks most similar to the query vector, ordered by score (highest first).
// This implementation loads all vectors into memory and computes distances in Go.
// For datasets < 100k vectors, this is acceptably fast.
func (d *DB) SearchChunksVec(ctx context.Context, queryVec []float32, limit int) ([]VecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	if queryVec == nil {
		return nil, fmt.Errorf("query vector cannot be nil")
	}

	// Enforce limit
	if limit <= 0 || limit > maxVecLimit {
		limit = maxVecLimit
	}

	// Load all vectors with their chunk info
	rows, err := d.db.QueryContext(ctx, `
		SELECT cv.chunk_id, c.content_id, cv.embedding
		FROM chunk_vectors cv
		JOIN chunks c ON cv.chunk_id = c.chunk_id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query chunk vectors: %w", err)
	}
	defer rows.Close()

	// Calculate similarities
	type scoredResult struct {
		chunkID   string
		contentID string
		score     float64
	}
	var scored []scoredResult

	for rows.Next() {
		var chunkID, contentID string
		var data []byte

		if err := rows.Scan(&chunkID, &contentID, &data); err != nil {
			return nil, fmt.Errorf("failed to scan chunk vector: %w", err)
		}

		vec, err := DeserializeVector(data)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize vector for chunk %s: %w", chunkID, err)
		}

		if len(vec) != len(queryVec) {
			// Skip vectors with different dimensions
			continue
		}

		score := cosineSimilarity(queryVec, vec)
		scored = append(scored, scoredResult{
			chunkID:   chunkID,
			contentID: contentID,
			score:     score,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating chunk vectors: %w", err)
	}

	// Sort by score descending (higher similarity first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top-k
	if len(scored) > limit {
		scored = scored[:limit]
	}

	// Convert to results
	results := make([]VecResult, len(scored))
	for i, s := range scored {
		results[i] = VecResult{
			ChunkID:   s.chunkID,
			ContentID: s.contentID,
			Score:     s.score,
		}
	}

	return results, nil
}

// BatchInsertChunkVectors inserts multiple chunk vectors in a single transaction.
// This is more efficient than inserting one at a time.
func (d *DB) BatchInsertChunkVectors(ctx context.Context, vectors map[string][]float32) error {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	if len(vectors) == 0 {
		return nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunk_vectors (chunk_id, embedding, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chunk_id) DO UPDATE SET
			embedding = excluded.embedding,
			created_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for chunkID, embedding := range vectors {
		if embedding == nil {
			continue
		}
		data := SerializeVector(embedding)
		if _, err := stmt.ExecContext(ctx, chunkID, data); err != nil {
			return fmt.Errorf("failed to insert vector for chunk %s: %w", chunkID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// CountChunkVectors returns the total number of stored vectors.
func (d *DB) CountChunkVectors(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunk_vectors").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count chunk vectors: %w", err)
	}

	return count, nil
}
