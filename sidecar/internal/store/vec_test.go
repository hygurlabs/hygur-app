package store

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestSerializeDeserializeVector(t *testing.T) {
	tests := []struct {
		name string
		vec  []float32
	}{
		{
			name: "simple vector",
			vec:  []float32{1.0, 2.0, 3.0},
		},
		{
			name: "negative values",
			vec:  []float32{-1.0, 0.0, 1.0},
		},
		{
			name: "small values",
			vec:  []float32{0.001, 0.002, 0.003},
		},
		{
			name: "large vector (768 dims like nomic-embed)",
			vec:  make([]float32, 768),
		},
		{
			name: "empty vector",
			vec:  []float32{},
		},
		{
			name: "nil vector",
			vec:  nil,
		},
	}

	// Initialize large vector with values
	for i := range tests[3].vec {
		tests[3].vec[i] = float32(i) * 0.001
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serialized := SerializeVector(tt.vec)

			if tt.vec == nil {
				if serialized != nil {
					t.Errorf("expected nil serialized for nil input, got %v", serialized)
				}
				return
			}

			expectedLen := len(tt.vec) * 4
			if len(serialized) != expectedLen {
				t.Errorf("expected serialized length %d, got %d", expectedLen, len(serialized))
			}

			deserialized, err := DeserializeVector(serialized)
			if err != nil {
				t.Fatalf("failed to deserialize: %v", err)
			}

			if len(deserialized) != len(tt.vec) {
				t.Errorf("expected deserialized length %d, got %d", len(tt.vec), len(deserialized))
			}

			for i := range tt.vec {
				if deserialized[i] != tt.vec[i] {
					t.Errorf("mismatch at index %d: expected %f, got %f", i, tt.vec[i], deserialized[i])
				}
			}
		})
	}
}

func TestDeserializeVectorInvalidLength(t *testing.T) {
	// Test with length not divisible by 4
	invalidData := []byte{1, 2, 3}
	_, err := DeserializeVector(invalidData)
	if err == nil {
		t.Error("expected error for invalid data length")
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float32
		b        []float32
		expected float64
		delta    float64
	}{
		{
			name:     "identical vectors",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{1.0, 2.0, 3.0},
			expected: 1.0,
			delta:    0.0001,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{-1.0, -2.0, -3.0},
			expected: -1.0,
			delta:    0.0001,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{0.0, 1.0, 0.0},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "similar vectors",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{1.1, 2.1, 3.1},
			expected: 0.9998, // Very similar
			delta:    0.001,
		},
		{
			name:     "zero vector a",
			a:        []float32{0.0, 0.0, 0.0},
			b:        []float32{1.0, 2.0, 3.0},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "zero vector b",
			a:        []float32{1.0, 2.0, 3.0},
			b:        []float32{0.0, 0.0, 0.0},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "different lengths",
			a:        []float32{1.0, 2.0},
			b:        []float32{1.0, 2.0, 3.0},
			expected: 0.0,
			delta:    0.0001,
		},
		{
			name:     "empty vectors",
			a:        []float32{},
			b:        []float32{},
			expected: 0.0,
			delta:    0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			if math.Abs(result-tt.expected) > tt.delta {
				t.Errorf("cosineSimilarity(%v, %v) = %f, want %f (delta %f)",
					tt.a, tt.b, result, tt.expected, tt.delta)
			}
		})
	}
}

func TestInsertAndGetChunkVector(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create a knowledge item and chunk first
	item := &KnowledgeItem{
		ContentID:      "ki-vec-001",
		SourceType:     "markdown",
		Title:          "Vector Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	chunk := &Chunk{
		ChunkID:   "ch-vec-001",
		ContentID: "ki-vec-001",
		ChunkHash: "hash1",
		Text:      "Chunk text",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Insert vector
	embedding := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	if err := db.InsertChunkVector(ctx, "ch-vec-001", embedding); err != nil {
		t.Fatalf("failed to insert chunk vector: %v", err)
	}

	// Get vector
	retrieved, err := db.GetChunkVector(ctx, "ch-vec-001")
	if err != nil {
		t.Fatalf("failed to get chunk vector: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected vector, got nil")
	}
	if len(retrieved) != len(embedding) {
		t.Errorf("expected length %d, got %d", len(embedding), len(retrieved))
	}
	for i := range embedding {
		if retrieved[i] != embedding[i] {
			t.Errorf("mismatch at index %d: expected %f, got %f", i, embedding[i], retrieved[i])
		}
	}

	// Get non-existent vector
	notFound, err := db.GetChunkVector(ctx, "non-existent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent vector")
	}
}

func TestInsertChunkVectorUpsert(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisite data
	item := &KnowledgeItem{
		ContentID:      "ki-vec-upsert",
		SourceType:     "markdown",
		Title:          "Upsert Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	chunk := &Chunk{
		ChunkID:   "ch-vec-upsert",
		ContentID: "ki-vec-upsert",
		ChunkHash: "hash1",
		Text:      "Chunk text",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Insert initial vector
	embedding1 := []float32{1.0, 2.0, 3.0}
	if err := db.InsertChunkVector(ctx, "ch-vec-upsert", embedding1); err != nil {
		t.Fatalf("failed to insert initial vector: %v", err)
	}

	// Update with new vector (upsert)
	embedding2 := []float32{4.0, 5.0, 6.0}
	if err := db.InsertChunkVector(ctx, "ch-vec-upsert", embedding2); err != nil {
		t.Fatalf("failed to upsert vector: %v", err)
	}

	// Verify updated vector
	retrieved, err := db.GetChunkVector(ctx, "ch-vec-upsert")
	if err != nil {
		t.Fatalf("failed to get chunk vector: %v", err)
	}
	for i := range embedding2 {
		if retrieved[i] != embedding2[i] {
			t.Errorf("mismatch at index %d: expected %f, got %f", i, embedding2[i], retrieved[i])
		}
	}
}

func TestInsertChunkVectorNilError(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Try to insert nil vector
	err = db.InsertChunkVector(ctx, "some-chunk", nil)
	if err == nil {
		t.Error("expected error when inserting nil vector")
	}
}

func TestDeleteChunkVector(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisite data
	item := &KnowledgeItem{
		ContentID:      "ki-vec-delete",
		SourceType:     "markdown",
		Title:          "Delete Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	chunk := &Chunk{
		ChunkID:   "ch-vec-delete",
		ContentID: "ki-vec-delete",
		ChunkHash: "hash1",
		Text:      "Chunk text",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Insert vector
	embedding := []float32{1.0, 2.0, 3.0}
	if err := db.InsertChunkVector(ctx, "ch-vec-delete", embedding); err != nil {
		t.Fatalf("failed to insert vector: %v", err)
	}

	// Delete vector
	if err := db.DeleteChunkVector(ctx, "ch-vec-delete"); err != nil {
		t.Fatalf("failed to delete vector: %v", err)
	}

	// Verify deleted
	retrieved, err := db.GetChunkVector(ctx, "ch-vec-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Error("expected nil after deletion")
	}

	// Delete non-existent (should not error)
	if err := db.DeleteChunkVector(ctx, "non-existent"); err != nil {
		t.Errorf("unexpected error deleting non-existent: %v", err)
	}
}

func TestSearchChunksVec(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create a knowledge item
	item := &KnowledgeItem{
		ContentID:      "ki-vec-search",
		SourceType:     "markdown",
		Title:          "Vector Search Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Create chunks with vectors
	testData := []struct {
		chunkID   string
		embedding []float32
	}{
		{"ch-search-001", []float32{1.0, 0.0, 0.0}}, // Unit vector along x
		{"ch-search-002", []float32{0.0, 1.0, 0.0}}, // Unit vector along y
		{"ch-search-003", []float32{0.0, 0.0, 1.0}}, // Unit vector along z
		{"ch-search-004", []float32{0.9, 0.1, 0.0}}, // Close to x
		{"ch-search-005", []float32{0.8, 0.2, 0.0}}, // Closer to x but less than 004
	}

	for _, td := range testData {
		chunk := &Chunk{
			ChunkID:   td.chunkID,
			ContentID: "ki-vec-search",
			ChunkHash: "hash-" + td.chunkID,
			Text:      "Chunk " + td.chunkID,
			CreatedAt: now,
		}
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
		if err := db.InsertChunkVector(ctx, td.chunkID, td.embedding); err != nil {
			t.Fatalf("failed to insert vector: %v", err)
		}
	}

	// Search with query vector similar to x-axis
	queryVec := []float32{1.0, 0.0, 0.0}
	results, err := db.SearchChunksVec(ctx, queryVec, 3)
	if err != nil {
		t.Fatalf("failed to search vectors: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// First result should be ch-search-001 (exact match)
	if len(results) > 0 && results[0].ChunkID != "ch-search-001" {
		t.Errorf("expected first result to be ch-search-001, got %s", results[0].ChunkID)
	}

	// First result should have score ~1.0
	if len(results) > 0 && math.Abs(results[0].Score-1.0) > 0.0001 {
		t.Errorf("expected first result score ~1.0, got %f", results[0].Score)
	}

	// Results should be in descending order of score
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not in descending order: %f > %f at index %d",
				results[i].Score, results[i-1].Score, i)
		}
	}
}

func TestSearchChunksVecLimit(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create a knowledge item
	item := &KnowledgeItem{
		ContentID:      "ki-vec-limit",
		SourceType:     "markdown",
		Title:          "Vector Limit Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Create 10 chunks with vectors
	for i := 0; i < 10; i++ {
		chunkID := "ch-limit-" + string(rune('A'+i))
		chunk := &Chunk{
			ChunkID:   chunkID,
			ContentID: "ki-vec-limit",
			ChunkHash: "hash-" + chunkID,
			Text:      "Chunk " + chunkID,
			CreatedAt: now,
		}
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}

		embedding := []float32{float32(i) * 0.1, float32(10-i) * 0.1, 0.5}
		if err := db.InsertChunkVector(ctx, chunkID, embedding); err != nil {
			t.Fatalf("failed to insert vector: %v", err)
		}
	}

	queryVec := []float32{0.5, 0.5, 0.5}

	// Test limit = 5
	results, err := db.SearchChunksVec(ctx, queryVec, 5)
	if err != nil {
		t.Fatalf("failed to search vectors: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results with limit=5, got %d", len(results))
	}

	// Test limit = 0 (should default to maxVecLimit)
	results, err = db.SearchChunksVec(ctx, queryVec, 0)
	if err != nil {
		t.Fatalf("failed to search vectors: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("expected 10 results with limit=0, got %d", len(results))
	}

	// Test limit > maxVecLimit
	results, err = db.SearchChunksVec(ctx, queryVec, 200)
	if err != nil {
		t.Fatalf("failed to search vectors: %v", err)
	}
	if len(results) != 10 {
		t.Errorf("expected 10 results with limit=200, got %d", len(results))
	}
}

func TestSearchChunksVecNilQueryError(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Search with nil query vector
	_, err = db.SearchChunksVec(ctx, nil, 10)
	if err == nil {
		t.Error("expected error when searching with nil query vector")
	}
}

func TestSearchChunksVecDifferentDimensions(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create a knowledge item
	item := &KnowledgeItem{
		ContentID:      "ki-vec-dims",
		SourceType:     "markdown",
		Title:          "Dimensions Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Create chunks with different dimension vectors
	chunk1 := &Chunk{
		ChunkID:   "ch-dims-001",
		ContentID: "ki-vec-dims",
		ChunkHash: "hash1",
		Text:      "Chunk 1",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk1); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}
	// 3D vector
	if err := db.InsertChunkVector(ctx, "ch-dims-001", []float32{1.0, 0.0, 0.0}); err != nil {
		t.Fatalf("failed to insert vector: %v", err)
	}

	chunk2 := &Chunk{
		ChunkID:   "ch-dims-002",
		ContentID: "ki-vec-dims",
		ChunkHash: "hash2",
		Text:      "Chunk 2",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk2); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}
	// 5D vector (different dimensions)
	if err := db.InsertChunkVector(ctx, "ch-dims-002", []float32{1.0, 0.0, 0.0, 0.0, 0.0}); err != nil {
		t.Fatalf("failed to insert vector: %v", err)
	}

	// Search with 3D query - should only match 3D vector
	queryVec := []float32{1.0, 0.0, 0.0}
	results, err := db.SearchChunksVec(ctx, queryVec, 10)
	if err != nil {
		t.Fatalf("failed to search vectors: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result (only matching dimension), got %d", len(results))
	}
	if len(results) > 0 && results[0].ChunkID != "ch-dims-001" {
		t.Errorf("expected ch-dims-001, got %s", results[0].ChunkID)
	}
}

func TestBatchInsertChunkVectors(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create a knowledge item
	item := &KnowledgeItem{
		ContentID:      "ki-vec-batch",
		SourceType:     "markdown",
		Title:          "Batch Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Create chunks
	for i := 0; i < 5; i++ {
		chunkID := "ch-batch-" + string(rune('A'+i))
		chunk := &Chunk{
			ChunkID:   chunkID,
			ContentID: "ki-vec-batch",
			ChunkHash: "hash-" + chunkID,
			Text:      "Chunk " + chunkID,
			CreatedAt: now,
		}
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
	}

	// Batch insert vectors
	vectors := map[string][]float32{
		"ch-batch-A": {1.0, 0.0, 0.0},
		"ch-batch-B": {0.0, 1.0, 0.0},
		"ch-batch-C": {0.0, 0.0, 1.0},
		"ch-batch-D": {0.5, 0.5, 0.0},
		"ch-batch-E": {0.0, 0.5, 0.5},
	}

	if err := db.BatchInsertChunkVectors(ctx, vectors); err != nil {
		t.Fatalf("failed to batch insert vectors: %v", err)
	}

	// Verify all vectors were inserted
	count, err := db.CountChunkVectors(ctx)
	if err != nil {
		t.Fatalf("failed to count vectors: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 vectors, got %d", count)
	}

	// Verify individual vectors
	for chunkID, expected := range vectors {
		retrieved, err := db.GetChunkVector(ctx, chunkID)
		if err != nil {
			t.Fatalf("failed to get vector for %s: %v", chunkID, err)
		}
		if len(retrieved) != len(expected) {
			t.Errorf("vector length mismatch for %s", chunkID)
		}
		for i := range expected {
			if retrieved[i] != expected[i] {
				t.Errorf("vector mismatch for %s at index %d", chunkID, i)
			}
		}
	}
}

func TestBatchInsertChunkVectorsEmpty(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Batch insert empty map (should not error)
	err = db.BatchInsertChunkVectors(ctx, map[string][]float32{})
	if err != nil {
		t.Errorf("unexpected error for empty batch: %v", err)
	}
}

func TestCountChunkVectors(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Initial count should be 0
	count, err := db.CountChunkVectors(ctx)
	if err != nil {
		t.Fatalf("failed to count vectors: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 vectors initially, got %d", count)
	}

	// Create prerequisite data
	item := &KnowledgeItem{
		ContentID:      "ki-vec-count",
		SourceType:     "markdown",
		Title:          "Count Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Add some chunks and vectors
	for i := 0; i < 3; i++ {
		chunkID := "ch-count-" + string(rune('A'+i))
		chunk := &Chunk{
			ChunkID:   chunkID,
			ContentID: "ki-vec-count",
			ChunkHash: "hash-" + chunkID,
			Text:      "Chunk " + chunkID,
			CreatedAt: now,
		}
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}
		if err := db.InsertChunkVector(ctx, chunkID, []float32{float32(i), 0.0, 0.0}); err != nil {
			t.Fatalf("failed to insert vector: %v", err)
		}
	}

	// Count should be 3
	count, err = db.CountChunkVectors(ctx)
	if err != nil {
		t.Fatalf("failed to count vectors: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 vectors, got %d", count)
	}
}

func TestChunkVectorCascadeDelete(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisite data
	item := &KnowledgeItem{
		ContentID:      "ki-vec-cascade",
		SourceType:     "markdown",
		Title:          "Cascade Test",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	chunk := &Chunk{
		ChunkID:   "ch-vec-cascade",
		ContentID: "ki-vec-cascade",
		ChunkHash: "hash1",
		Text:      "Chunk text",
		CreatedAt: now,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Insert vector
	if err := db.InsertChunkVector(ctx, "ch-vec-cascade", []float32{1.0, 2.0, 3.0}); err != nil {
		t.Fatalf("failed to insert vector: %v", err)
	}

	// Verify vector exists
	count, err := db.CountChunkVectors(ctx)
	if err != nil {
		t.Fatalf("failed to count vectors: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 vector before cascade, got %d", count)
	}

	// Delete the chunk (should cascade delete the vector)
	if err := db.DeleteChunksByContentID(ctx, "ki-vec-cascade"); err != nil {
		t.Fatalf("failed to delete chunks: %v", err)
	}

	// Verify vector was cascade deleted
	count, err = db.CountChunkVectors(ctx)
	if err != nil {
		t.Fatalf("failed to count vectors after cascade: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 vectors after cascade delete, got %d", count)
	}
}
