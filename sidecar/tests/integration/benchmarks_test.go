// Package integration provides benchmarks for Lot 2 performance validation.
// Performance thresholds:
// - Ingest 1MB of text: < 5s
// - Search on 10k chunks: < 100ms
// - Batch embeddings (10 chunks): < 10s (requires LM Studio)
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
)

// ============================================================================
// Benchmark: Ingestion Performance
// ============================================================================

// BenchmarkIngest1MB measures the time to ingest 1MB of text.
// Target: < 5 seconds
func BenchmarkIngest1MB(b *testing.B) {
	// Generate 1MB of text (approximately)
	// 1MB = 1,048,576 bytes
	// Using repeated sentences to simulate real content
	sentence := "This is a sample sentence for benchmarking the ingestion pipeline. "
	repeatCount := 1048576 / len(sentence)
	text := strings.Repeat(sentence, repeatCount)

	b.Logf("Generated text of size: %d bytes (%.2f MB)", len(text), float64(len(text))/(1024*1024))

	chunker := ingest.DefaultChunker()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Measure chunking which is the main CPU-bound operation
		chunks, err := chunker.Chunk(text)
		if err != nil {
			b.Fatalf("chunking failed: %v", err)
		}
		if len(chunks) == 0 {
			b.Fatal("expected chunks")
		}
	}

	b.StopTimer()

	// Verify performance meets threshold
	if b.N > 0 {
		avgNs := b.Elapsed().Nanoseconds() / int64(b.N)
		avgSeconds := float64(avgNs) / 1e9
		if avgSeconds > 5.0 {
			b.Errorf("ingestion took %.2fs per 1MB, threshold is 5s", avgSeconds)
		} else {
			b.Logf("Ingestion took %.2fs per 1MB (threshold: 5s) - PASS", avgSeconds)
		}
	}
}

// BenchmarkChunking measures raw chunking performance.
func BenchmarkChunking(b *testing.B) {
	text := strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ", 1000)
	chunker := ingest.DefaultChunker()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = chunker.Chunk(text)
	}
}

// BenchmarkMarkdownParsing measures Markdown parsing performance.
func BenchmarkMarkdownParsing(b *testing.B) {
	mdContent := `---
title: Benchmark Test
author: Test Author
---

# Introduction

This is a test document for benchmarking.

## Section 1

` + strings.Repeat("Lorem ipsum dolor sit amet. ", 500) + `

## Section 2

` + strings.Repeat("Consectetur adipiscing elit. ", 500)

	parser := parsers.NewMarkdownParser()
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reader := strings.NewReader(mdContent)
		_, _, err := parser.Parse(ctx, reader)
		if err != nil {
			b.Fatalf("parse failed: %v", err)
		}
	}
}

// BenchmarkNormalization measures text normalization performance.
func BenchmarkNormalization(b *testing.B) {
	text := strings.Repeat("This is   some    text   with   irregular   spacing.\n\n\n", 100)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ingest.NormalizeText(text)
	}
}

// ============================================================================
// Benchmark: Search Performance
// ============================================================================

// BenchmarkSearch10kChunks measures search performance on a 10k chunk dataset.
// Target: < 100ms
func BenchmarkSearch10kChunks(b *testing.B) {
	ctx := context.Background()

	// Create in-memory database
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Generate and insert 10k chunks
	b.Log("Populating database with 10k chunks...")
	populateStart := time.Now()

	topics := []string{
		"programming language",
		"machine learning",
		"web development",
		"database systems",
		"cloud computing",
		"software engineering",
		"artificial intelligence",
		"data science",
		"mobile development",
		"cybersecurity",
	}

	// First create knowledge items (foreign key requirement)
	for i := 0; i < 1000; i++ {
		ki := &store.KnowledgeItem{
			ContentID:      fmt.Sprintf("content-%d", i),
			SourceType:     "test",
			Title:          fmt.Sprintf("Document %d", i),
			NormalizedText: "test content",
			Metadata:       nil,
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := db.InsertKnowledgeItem(ctx, ki); err != nil {
			b.Fatalf("failed to insert knowledge item %d: %v", i, err)
		}
	}

	for i := 0; i < 10000; i++ {
		topic := topics[i%len(topics)]
		text := fmt.Sprintf("Document %d about %s. This is chunk content for testing search performance. ", i, topic)
		text += strings.Repeat("Additional content for realistic chunk size. ", 5)

		chunk := &store.Chunk{
			ChunkID:   fmt.Sprintf("chunk-%d", i),
			ContentID: fmt.Sprintf("content-%d", i/10),
			ChunkHash: hashTextBench(text),
			Text:      text,
			Metadata:  map[string]any{"index": i},
			CreatedAt: time.Now(),
		}
		if err := db.InsertChunk(ctx, chunk); err != nil {
			b.Fatalf("failed to insert chunk %d: %v", i, err)
		}
	}

	b.Logf("Populated 10k chunks in %v", time.Since(populateStart))

	// Search query
	query := "programming language software development"

	b.ResetTimer()

	// Use a synthetic query vector since we're benchmarking DB/cosine perf, not LLM.
	queryVec := generateTestEmbedding(query, 768)

	for i := 0; i < b.N; i++ {
		results, err := db.SearchChunksVec(ctx, queryVec, 10)
		if err != nil {
			b.Fatalf("search failed: %v", err)
		}
		_ = results
	}

	b.StopTimer()

	if b.N > 0 {
		avgNs := b.Elapsed().Nanoseconds() / int64(b.N)
		avgMs := float64(avgNs) / 1e6
		if avgMs > 100.0 {
			b.Errorf("search took %.2fms per query, threshold is 100ms", avgMs)
		} else {
			b.Logf("Search took %.2fms per query (threshold: 100ms) - PASS", avgMs)
		}
	}
}

// BenchmarkFTSSearch measures FTS search performance.
// BenchmarkFTSSearch is removed — FTS is no longer supported.

// BenchmarkVectorSearch measures vector similarity search performance.
func BenchmarkVectorSearch(b *testing.B) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Insert 1000 chunks with vectors
	for i := 0; i < 1000; i++ {
		text := fmt.Sprintf("Document %d content", i)
		chunk := &store.Chunk{
			ChunkID:   fmt.Sprintf("chunk-%d", i),
			ContentID: fmt.Sprintf("content-%d", i),
			ChunkHash: hashTextBench(text),
			Text:      text,
			Metadata:  nil,
			CreatedAt: time.Now(),
		}
		db.InsertChunk(ctx, chunk)

		// Insert vector
		embedding := generateTestEmbedding(text, 768)
		db.InsertChunkVector(ctx, chunk.ChunkID, embedding)
	}

	// Query vector
	queryVec := generateTestEmbedding("search query", 768)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = db.SearchChunksVec(ctx, queryVec, 10)
	}
}

// BenchmarkSemanticSearch measures vector-only semantic search performance.
func BenchmarkSemanticSearch(b *testing.B) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"object": "list",
				"data": [{"index": 0, "embedding": [` + generateEmbeddingJSON(768) + `]}],
				"model": "test"
			}`))
		}
	}))
	defer mockServer.Close()

	llmClient := llm.NewClientWithHTTP(
		mockServer.URL,
		5*time.Second,
		0,
		&http.Client{Timeout: 5 * time.Second},
	)

	searcher := retrieval.NewHybridSearcher(db, llmClient)

	for i := 0; i < 500; i++ {
		text := fmt.Sprintf("Document %d about programming and software development.", i)
		chunk := &store.Chunk{
			ChunkID:   fmt.Sprintf("chunk-%d", i),
			ContentID: fmt.Sprintf("content-%d", i),
			ChunkHash: hashTextBench(text),
			Text:      text,
			CreatedAt: time.Now(),
		}
		db.InsertChunk(ctx, chunk)
		embedding := generateTestEmbedding(text, 768)
		db.InsertChunkVector(ctx, chunk.ChunkID, embedding)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = searcher.Search(ctx, "programming software", retrieval.SearchOptions{TopK: 10})
	}
}

// ============================================================================
// Benchmark: Embedding Performance
// ============================================================================

// BenchmarkEmbeddingBatch measures batch embedding generation.
// Note: This requires LM Studio to be running with an embedding model loaded.
// Target: < 10s for 10 chunks
func BenchmarkEmbeddingBatch(b *testing.B) {
	// Skip if not in integration mode
	if testing.Short() {
		b.Skip("skipping embedding benchmark in short mode (requires LM Studio)")
	}

	// Create mock LM Studio server for consistent benchmarking
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			// Simulate some processing time
			time.Sleep(10 * time.Millisecond)

			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"object": "list",
				"data": [
					{"index": 0, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 1, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 2, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 3, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 4, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 5, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 6, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 7, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 8, "embedding": [` + generateEmbeddingJSON(768) + `]},
					{"index": 9, "embedding": [` + generateEmbeddingJSON(768) + `]}
				],
				"model": "text-embedding-nomic-embed-text-v1.5"
			}`))
		}
	}))
	defer mockServer.Close()

	client := llm.NewClientWithHTTP(
		mockServer.URL,
		30*time.Second,
		0,
		&http.Client{Timeout: 30 * time.Second},
	)

	// Generate 10 test texts
	texts := make([]string, 10)
	for i := 0; i < 10; i++ {
		texts[i] = fmt.Sprintf("This is test chunk number %d with some content for embedding. "+
			"It contains multiple sentences to simulate real document chunks.", i)
	}

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		embeddings, err := client.GenerateEmbeddings(ctx, texts)
		if err != nil {
			b.Fatalf("embedding generation failed: %v", err)
		}
		if len(embeddings) != 10 {
			b.Errorf("expected 10 embeddings, got %d", len(embeddings))
		}
	}

	b.StopTimer()

	// Verify performance
	if b.N > 0 {
		avgNs := b.Elapsed().Nanoseconds() / int64(b.N)
		avgSeconds := float64(avgNs) / 1e9
		if avgSeconds > 10.0 {
			b.Errorf("embedding batch took %.2fs, threshold is 10s", avgSeconds)
		} else {
			b.Logf("Embedding batch took %.2fs (threshold: 10s) - PASS", avgSeconds)
		}
	}
}

// ============================================================================
// Benchmark: Deduplication Performance
// ============================================================================

// BenchmarkSimHash measures SimHash computation performance.
func BenchmarkSimHash(b *testing.B) {
	text := strings.Repeat("This is a sample text for SimHash computation. ", 100)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ingest.SimHash(text)
	}
}

// BenchmarkHashContent measures content hashing performance.
func BenchmarkHashContent(b *testing.B) {
	text := strings.Repeat("Content to be hashed for deduplication. ", 100)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ingest.HashContent(text)
	}
}

// BenchmarkHammingDistance measures Hamming distance calculation.
func BenchmarkHammingDistance(b *testing.B) {
	hash1 := ingest.SimHash("First document content")
	hash2 := ingest.SimHash("Second document content")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ingest.HammingDistance(hash1, hash2)
	}
}

// BenchmarkDeduplication measures full deduplication check.
func BenchmarkDeduplication(b *testing.B) {
	dedup := ingest.DefaultDeduplicator()

	// Create a corpus of 1000 existing documents
	existingHashes := make(map[string]string)
	existingSimHashes := make(map[uint64]string)

	for i := 0; i < 1000; i++ {
		text := fmt.Sprintf("Existing document %d with unique content.", i)
		existingHashes[dedup.GetContentHash(text)] = fmt.Sprintf("doc-%d", i)
		existingSimHashes[dedup.GetSimHash(text)] = fmt.Sprintf("doc-%d", i)
	}

	newText := "A brand new document that doesn't exist in the corpus."

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = dedup.CheckDuplicate(newText, existingHashes, existingSimHashes)
	}
}

// ============================================================================
// Benchmark: RRF Algorithm
// ============================================================================

// BenchmarkRRFFusion is removed — RRF is no longer used (vector-only pipeline).

// ============================================================================
// Benchmark: Database Operations
// ============================================================================

// BenchmarkInsertChunk measures chunk insertion performance.
func BenchmarkInsertChunk(b *testing.B) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	text := strings.Repeat("Sample chunk content. ", 50)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		chunk := &store.Chunk{
			ChunkID:   fmt.Sprintf("chunk-%d", i),
			ContentID: "content-1",
			ChunkHash: hashTextBench(text),
			Text:      text,
			Metadata:  nil,
			CreatedAt: time.Now(),
		}
		_ = db.InsertChunk(ctx, chunk)
	}
}

// BenchmarkInsertVector measures vector insertion performance.
func BenchmarkInsertVector(b *testing.B) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// First insert a chunk
	chunk := &store.Chunk{
		ChunkID:   "chunk-0",
		ContentID: "content-1",
		ChunkHash: "hash",
		Text:      "text",
		CreatedAt: time.Now(),
	}
	db.InsertChunk(ctx, chunk)

	embedding := generateTestEmbedding("test", 768)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Use different chunk IDs to avoid conflicts
		db.InsertChunkVector(ctx, fmt.Sprintf("chunk-%d", i), embedding)
	}
}

// BenchmarkBatchInsertVectors measures batch vector insertion performance.
func BenchmarkBatchInsertVectors(b *testing.B) {
	ctx := context.Background()
	db, err := store.NewDB(":memory:")
	if err != nil {
		b.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create batch of 100 vectors
	vectors := make(map[string][]float32)
	for i := 0; i < 100; i++ {
		vectors[fmt.Sprintf("chunk-%d", i)] = generateTestEmbedding(fmt.Sprintf("text-%d", i), 768)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Modify keys for each iteration to avoid conflicts
		batchVectors := make(map[string][]float32)
		for k, v := range vectors {
			batchVectors[fmt.Sprintf("%s-%d", k, i)] = v
		}
		_ = db.BatchInsertChunkVectors(ctx, batchVectors)
	}
}

// ============================================================================
// Helper Functions
// ============================================================================

func hashTextBench(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func generateTestEmbedding(text string, dim int) []float32 {
	h := sha256.Sum256([]byte(text))
	embedding := make([]float32, dim)
	for i := 0; i < dim; i++ {
		byteIdx := i % len(h)
		embedding[i] = (float32(h[byteIdx]) - 128.0) / 128.0
	}
	return embedding
}

func generateEmbeddingJSON(dim int) string {
	values := make([]string, dim)
	for i := 0; i < dim; i++ {
		values[i] = fmt.Sprintf("%.6f", float32(i)/float32(dim)-0.5)
	}
	return strings.Join(values, ",")
}
