// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GetCanonicalDate reads the canonical_date field from a KnowledgeItem's
// metadata, with fallback to mail_date for items indexed before canonical_date
// was added to the indexer schema. Returns the zero value if neither field
// yields a parseable RFC3339 timestamp.
func GetCanonicalDate(item *KnowledgeItem) time.Time {
	if item == nil || item.Metadata == nil {
		return time.Time{}
	}
	for _, key := range []string{"canonical_date", "mail_date"} {
		v, ok := item.Metadata[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// SearchChunksVecBySourceType performs a vector similarity search filtered by source types.
// It joins chunks with knowledge_items to filter by source_type.
// Returns the top-k chunks most similar to the query vector, ordered by score (highest first).
func (d *DB) SearchChunksVecBySourceType(ctx context.Context, queryVec []float32, limit int, sourceTypes []string) ([]VecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	if queryVec == nil {
		return nil, fmt.Errorf("query vector cannot be nil")
	}

	// Enforce limit
	if limit <= 0 || limit > maxVecLimit {
		limit = maxVecLimit
	}

	// If no source types specified, search all
	if len(sourceTypes) == 0 {
		return d.SearchChunksVec(ctx, queryVec, limit)
	}

	// Build placeholders for source types
	placeholders := make([]string, len(sourceTypes))
	args := make([]any, len(sourceTypes))
	for i, st := range sourceTypes {
		placeholders[i] = "?"
		args[i] = st
	}

	// Load vectors with source type filter
	sqlQuery := fmt.Sprintf(`
		SELECT cv.chunk_id, c.content_id, cv.embedding
		FROM chunk_vectors cv
		JOIN chunks c ON cv.chunk_id = c.chunk_id
		JOIN knowledge_items ki ON c.content_id = ki.content_id
		WHERE ki.source_type IN (%s)
	`, strings.Join(placeholders, ", "))

	rows, err := d.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query chunk vectors by source type: %w", err)
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

// MailFilter scopes mail chunk searches by account and/or label.
// Zero-value fields are ignored (no filter applied for that field).
type MailFilter struct {
	// AccountID matches items where metadata.account_id equals this value exactly.
	AccountID string
	// LabelIDs matches items where metadata.gmail_labels contains ANY of these IDs.
	// Uses JSON array membership via SQLite json_each.
	LabelIDs []string
}

// SearchChunksVecByMail loads vectors for mail chunks matching MailFilter and
// returns the top-k by cosine similarity.
func (d *DB) SearchChunksVecByMail(ctx context.Context, queryVec []float32, limit int, filter MailFilter) ([]VecResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	if queryVec == nil {
		return nil, fmt.Errorf("query vector cannot be nil")
	}
	if limit <= 0 || limit > maxVecLimit {
		limit = maxVecLimit
	}

	conditions := []string{"ki.source_type IN ('mail', 'email', 'thread')"}
	args := []any{}

	if filter.AccountID != "" {
		conditions = append(conditions, "json_extract(ki.metadata, '$.account_id') = ?")
		args = append(args, filter.AccountID)
	}
	if len(filter.LabelIDs) > 0 {
		labelClauses := make([]string, len(filter.LabelIDs))
		for i, label := range filter.LabelIDs {
			labelClauses[i] = "EXISTS (SELECT 1 FROM json_each(json_extract(ki.metadata, '$.gmail_labels')) WHERE value = ?)"
			args = append(args, label)
		}
		conditions = append(conditions, "("+strings.Join(labelClauses, " OR ")+")")
	}

	sqlQuery := `
		SELECT cv.chunk_id, c.content_id, cv.embedding
		FROM chunk_vectors cv
		JOIN chunks c ON cv.chunk_id = c.chunk_id
		JOIN knowledge_items ki ON c.content_id = ki.content_id
		WHERE ` + strings.Join(conditions, " AND ")

	rows, err := d.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("SearchChunksVecByMail: %w", err)
	}
	defer rows.Close()

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
			return nil, fmt.Errorf("SearchChunksVecByMail scan: %w", err)
		}
		vec, err := DeserializeVector(data)
		if err != nil {
			continue
		}
		if len(vec) != len(queryVec) {
			continue
		}
		scored = append(scored, scoredResult{chunkID, contentID, cosineSimilarity(queryVec, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]VecResult, len(scored))
	for i, s := range scored {
		results[i] = VecResult{ChunkID: s.chunkID, ContentID: s.contentID, Score: s.score}
	}
	return results, nil
}

// GetKnowledgeItemWithMailMetadata retrieves a knowledge item with mail-specific metadata.
// This extracts mail_from, mail_date, and mail_subject from the metadata JSON.
type KnowledgeItemWithMail struct {
	*KnowledgeItem
	MailFrom    string
	MailDate    string
	MailSubject string
}

// GetKnowledgeItemWithMailData retrieves a knowledge item and extracts mail-specific fields if present.
func (d *DB) GetKnowledgeItemWithMailData(ctx context.Context, contentID string) (*KnowledgeItemWithMail, error) {
	item, err := d.GetKnowledgeItem(ctx, contentID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}

	result := &KnowledgeItemWithMail{
		KnowledgeItem: item,
	}

	// Extract mail-specific metadata if present
	if item.Metadata != nil {
		if from, ok := item.Metadata["mail_from"].(string); ok {
			result.MailFrom = from
		}
		if date, ok := item.Metadata["mail_date"].(string); ok {
			result.MailDate = date
		}
		if subject, ok := item.Metadata["mail_subject"].(string); ok {
			result.MailSubject = subject
		}
	}

	return result, nil
}
