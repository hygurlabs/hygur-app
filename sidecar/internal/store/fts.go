// Package store provides SQLite database access for the Hygur knowledge base.
//
// This file implements the lexical (BM25) half of the hybrid retriever on top
// of the chunks_fts FTS5 index (created in migration 9). It REQUIRES the
// sqlite_fts5 build tag — without it the index does not exist and every query
// here errors with "no such module: fts5".
package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// FTSResult is a single lexical (BM25) search hit.
type FTSResult struct {
	ChunkID   string
	ContentID string
	// Score is normalised so higher = better, consistent with VecResult.
	// (SQLite's bm25() returns lower-is-better, so we negate it.)
	Score float64
}

// SearchChunksFTS runs a BM25 full-text search over chunk text via chunks_fts
// and returns up to `limit` hits ordered best-first. A query whose sanitised
// form has no usable terms yields an empty slice and no error — the caller
// (the hybrid ranker) simply falls back to the vector side.
func (d *DB) SearchChunksFTS(ctx context.Context, query string, limit int) ([]FTSResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultVecTimeout)
	defer cancel()

	match := buildFTSMatchQuery(query)
	if match == "" {
		return []FTSResult{}, nil
	}
	if limit <= 0 || limit > maxVecLimit {
		limit = maxVecLimit
	}

	rows, err := d.db.QueryContext(ctx, `
		SELECT chunk_id, content_id, bm25(chunks_fts) AS score
		FROM chunks_fts
		WHERE chunks_fts MATCH ?
		ORDER BY score
		LIMIT ?
	`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var results []FTSResult
	for rows.Next() {
		var r FTSResult
		var bm25 float64
		if err := rows.Scan(&r.ChunkID, &r.ContentID, &bm25); err != nil {
			return nil, fmt.Errorf("fts scan: %w", err)
		}
		r.Score = -bm25 // bm25() is lower-is-better; flip so higher = better
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fts iterate: %w", err)
	}
	return results, nil
}

// buildFTSMatchQuery turns free user text into a safe FTS5 MATCH expression.
// Each alphanumeric token is double-quoted (so punctuation, accents and FTS5
// operators like AND/OR/NEAR/"*" can never break the query syntax) and the
// terms are ORed together for recall — precision is the hybrid ranker's job.
// Single-rune tokens are dropped as noise. Returns "" when nothing is usable.
func buildFTSMatchQuery(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if len([]rune(f)) < 2 {
			continue
		}
		// FTS5 string literals escape an embedded double quote by doubling it.
		f = strings.ReplaceAll(f, `"`, `""`)
		terms = append(terms, `"`+f+`"`)
	}
	return strings.Join(terms, " OR ")
}

// RebuildChunksFTS clears and repopulates the FTS index from the chunks table.
// The row triggers keep chunks_fts in sync for normal inserts/deletes; this is
// for bulk paths that may bypass them and for the reindex CLI.
func (d *DB) RebuildChunksFTS(ctx context.Context) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM chunks_fts`); err != nil {
		return fmt.Errorf("clear fts: %w", err)
	}
	if _, err := d.db.ExecContext(ctx, `
		INSERT INTO chunks_fts(chunk_id, content_id, text)
		SELECT chunk_id, content_id, text FROM chunks
	`); err != nil {
		return fmt.Errorf("rebuild fts: %w", err)
	}
	return nil
}
