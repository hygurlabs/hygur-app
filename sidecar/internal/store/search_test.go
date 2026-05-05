package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchChunksVecBySourceType(t *testing.T) {
	db, err := NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	// Insert test knowledge items with different source types
	items := []*KnowledgeItem{
		{
			ContentID:      "file1",
			SourceType:     "file",
			Title:          "Test File",
			NormalizedText: "This is a test file",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ContentID:      "mail1",
			SourceType:     "mail",
			Title:          "Test Email",
			NormalizedText: "This is a test email",
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	}

	for _, item := range items {
		err := db.InsertKnowledgeItem(ctx, item)
		require.NoError(t, err)
	}

	// Insert chunks for each item
	chunks := []*Chunk{
		{
			ChunkID:   "chunk-file1",
			ContentID: "file1",
			ChunkHash: "hash1",
			Text:      "file content",
			CreatedAt: time.Now(),
		},
		{
			ChunkID:   "chunk-mail1",
			ContentID: "mail1",
			ChunkHash: "hash2",
			Text:      "mail content",
			CreatedAt: time.Now(),
		},
	}

	for _, chunk := range chunks {
		err := db.InsertChunk(ctx, chunk)
		require.NoError(t, err)
	}

	// Insert vectors
	fileVec := []float32{1.0, 0.0, 0.0}
	mailVec := []float32{0.0, 1.0, 0.0}

	err = db.InsertChunkVector(ctx, "chunk-file1", fileVec)
	require.NoError(t, err)
	err = db.InsertChunkVector(ctx, "chunk-mail1", mailVec)
	require.NoError(t, err)

	// Query vector similar to file
	queryVec := []float32{0.9, 0.1, 0.0}

	tests := []struct {
		name        string
		sourceTypes []string
		wantCount   int
		wantIDs     []string
	}{
		{
			name:        "search all source types",
			sourceTypes: nil,
			wantCount:   2,
			wantIDs:     []string{"chunk-file1", "chunk-mail1"},
		},
		{
			name:        "search file only",
			sourceTypes: []string{"file"},
			wantCount:   1,
			wantIDs:     []string{"chunk-file1"},
		},
		{
			name:        "search mail only",
			sourceTypes: []string{"mail"},
			wantCount:   1,
			wantIDs:     []string{"chunk-mail1"},
		},
		{
			name:        "search non-existent type",
			sourceTypes: []string{"thread"},
			wantCount:   0,
			wantIDs:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := db.SearchChunksVecBySourceType(ctx, queryVec, 10, tt.sourceTypes)
			require.NoError(t, err)
			assert.Len(t, results, tt.wantCount)

			if tt.wantIDs != nil {
				resultIDs := make([]string, len(results))
				for i, r := range results {
					resultIDs[i] = r.ChunkID
				}
				for _, wantID := range tt.wantIDs {
					assert.Contains(t, resultIDs, wantID)
				}
			}
		})
	}
}

func TestGetKnowledgeItemWithMailData(t *testing.T) {
	db, err := NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	tests := []struct {
		name        string
		item        *KnowledgeItem
		wantFrom    string
		wantDate    string
		wantSubject string
	}{
		{
			name: "mail with full metadata",
			item: &KnowledgeItem{
				ContentID:      "mail1",
				SourceType:     "mail",
				Title:          "Test Email",
				NormalizedText: "Email body",
				VersionID:      "v1",
				Metadata: map[string]any{
					"mail_from":    "sender@example.com",
					"mail_date":    "2024-04-22",
					"mail_subject": "Test Subject",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantFrom:    "sender@example.com",
			wantDate:    "2024-04-22",
			wantSubject: "Test Subject",
		},
		{
			name: "mail with partial metadata",
			item: &KnowledgeItem{
				ContentID:      "mail2",
				SourceType:     "mail",
				Title:          "Another Email",
				NormalizedText: "Email body 2",
				VersionID:      "v1",
				Metadata: map[string]any{
					"mail_from": "another@example.com",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantFrom:    "another@example.com",
			wantDate:    "",
			wantSubject: "",
		},
		{
			name: "file without mail metadata",
			item: &KnowledgeItem{
				ContentID:      "file1",
				SourceType:     "file",
				Title:          "Test File",
				NormalizedText: "File content",
				VersionID:      "v1",
				Metadata: map[string]any{
					"file_path": "/test/file.md",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantFrom:    "",
			wantDate:    "",
			wantSubject: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.InsertKnowledgeItem(ctx, tt.item)
			require.NoError(t, err)

			result, err := db.GetKnowledgeItemWithMailData(ctx, tt.item.ContentID)
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.wantFrom, result.MailFrom)
			assert.Equal(t, tt.wantDate, result.MailDate)
			assert.Equal(t, tt.wantSubject, result.MailSubject)
		})
	}
}

func TestGetKnowledgeItemWithMailData_NotFound(t *testing.T) {
	db, err := NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	result, err := db.GetKnowledgeItemWithMailData(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestSearchChunksVecBySourceType_NilVector(t *testing.T) {
	db, err := NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	_, err = db.SearchChunksVecBySourceType(ctx, nil, 10, []string{"file"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query vector cannot be nil")
}

func TestSearchChunksVecByMail_MailFilter(t *testing.T) {
	db, err := NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()

	item := &KnowledgeItem{
		ContentID:      "email-vec1",
		SourceType:     "email",
		Title:          "Vec email alice",
		NormalizedText: "vec email content",
		VersionID:      "v1",
		Metadata: map[string]any{
			"account_id":   "alice@gmail.com",
			"gmail_labels": []any{"INBOX", "Recharge"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, db.InsertKnowledgeItem(ctx, item))

	chunk := &Chunk{
		ChunkID:   "chunk-vec1",
		ContentID: "email-vec1",
		ChunkHash: "hashvec1",
		Text:      "vec email content",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.InsertChunk(ctx, chunk))

	vec := []float32{1.0, 0.0, 0.0}
	require.NoError(t, db.InsertChunkVector(ctx, "chunk-vec1", vec))

	queryVec := []float32{1.0, 0.0, 0.0}

	t.Run("filter by account_id alice finds result", func(t *testing.T) {
		results, err := db.SearchChunksVecByMail(ctx, queryVec, 10, MailFilter{AccountID: "alice@gmail.com"})
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, "chunk-vec1", results[0].ChunkID)
	})

	t.Run("filter by unknown account returns empty", func(t *testing.T) {
		results, err := db.SearchChunksVecByMail(ctx, queryVec, 10, MailFilter{AccountID: "bob@gmail.com"})
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("nil vector returns error", func(t *testing.T) {
		_, err := db.SearchChunksVecByMail(ctx, nil, 10, MailFilter{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "query vector cannot be nil")
	})
}
