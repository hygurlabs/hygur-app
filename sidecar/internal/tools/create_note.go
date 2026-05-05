// Package tools provides callable tools for Hygur chat functionality.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// CreateNoteTool creates knowledge items of type "note" from conversations.
type CreateNoteTool struct {
	store            *store.DB
	embeddingService *llm.EmbeddingService
}

// NewCreateNoteTool creates a new CreateNoteTool with the given database.
func NewCreateNoteTool(db *store.DB) *CreateNoteTool {
	return &CreateNoteTool{
		store: db,
	}
}

// NewCreateNoteToolWithEmbeddings creates a CreateNoteTool with embedding support.
func NewCreateNoteToolWithEmbeddings(db *store.DB, embSvc *llm.EmbeddingService) *CreateNoteTool {
	return &CreateNoteTool{
		store:            db,
		embeddingService: embSvc,
	}
}

// CreateNoteRequest represents the input for creating a note.
type CreateNoteRequest struct {
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	ProjectID *string  `json:"project_id,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// CreateNoteResponse represents the output after creating a note.
type CreateNoteResponse struct {
	ContentID  string    `json:"content_id"`
	Title      string    `json:"title"`
	SourceType string    `json:"source_type"`
	CreatedAt  time.Time `json:"created_at"`
}

// Validate validates the CreateNoteRequest.
func (r *CreateNoteRequest) Validate() error {
	if r.Title == "" {
		return fmt.Errorf("title is required")
	}
	if r.Content == "" {
		return fmt.Errorf("content is required")
	}
	return nil
}

// Run creates a new note as a KnowledgeItem and optionally links it to a project.
func (t *CreateNoteTool) Run(ctx context.Context, req CreateNoteRequest) (*CreateNoteResponse, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	// Generate content ID with "note:" prefix
	contentID := "note:" + uuid.New().String()
	now := time.Now()

	// Normalize the content for full-text search
	normalizedText := ingest.NormalizeText(req.Content)

	// Build metadata
	metadata := map[string]any{
		"created_from":   "tool",
		"canonical_date": now.UTC().Format(time.RFC3339),
	}
	if len(req.Tags) > 0 {
		metadata["tags"] = req.Tags
	}

	// Create the knowledge item
	item := &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     "note",
		SourcePath:     nil, // Notes don't have a source path
		Title:          req.Title,
		NormalizedText: normalizedText,
		Metadata:       metadata,
		VersionID:      uuid.New().String(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Save to database
	if err := t.store.InsertKnowledgeItem(ctx, item); err != nil {
		return nil, fmt.Errorf("failed to save note: %w", err)
	}

	// Chunk the content for search
	chunks := ingest.ChunkText(normalizedText, ingest.DefaultChunkOptions())

	// Insert chunks and collect for embedding
	var storeChunks []store.Chunk
	for _, chunk := range chunks {
		chunkID := uuid.New().String()

		storeChunk := &store.Chunk{
			ChunkID:   chunkID,
			ContentID: contentID,
			ChunkHash: fmt.Sprintf("%x", chunkID[:8]), // Simple hash for notes
			Text:      chunk.Content,
			Metadata: map[string]any{
				"index":        chunk.Index,
				"start_offset": chunk.StartOffset,
				"end_offset":   chunk.EndOffset,
			},
			CreatedAt: now,
		}

		if err := t.store.InsertChunk(ctx, storeChunk); err != nil {
			return nil, fmt.Errorf("failed to insert chunk %d: %w", chunk.Index, err)
		}

		storeChunks = append(storeChunks, *storeChunk)
	}

	// Generate embeddings — mandatory. Roll back if this fails.
	if t.embeddingService != nil && len(storeChunks) > 0 {
		if err := t.embeddingService.BatchEmbedAndStore(ctx, storeChunks); err != nil {
			_ = t.store.DeleteKnowledgeItem(context.Background(), contentID)
			return nil, fmt.Errorf("embedding failed for note %s: %w", contentID, err)
		}
	}

	// Link to project if specified
	if req.ProjectID != nil && *req.ProjectID != "" {
		// Verify project exists
		project, err := t.store.GetProject(ctx, *req.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project: %w", err)
		}
		if project == nil {
			return nil, fmt.Errorf("project not found: %s", *req.ProjectID)
		}

		// Create project link
		link := &store.ProjectLink{
			LinkID:    uuid.New().String(),
			ProjectID: *req.ProjectID,
			ContentID: contentID,
			CreatedAt: now,
		}

		if err := t.store.InsertProjectLink(ctx, link); err != nil {
			return nil, fmt.Errorf("failed to link note to project: %w", err)
		}
	}

	return &CreateNoteResponse{
		ContentID:  contentID,
		Title:      req.Title,
		SourceType: "note",
		CreatedAt:  now,
	}, nil
}

// ToolDefinition returns the tool definition for LLM function calling.
func (t *CreateNoteTool) ToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "create_note",
			"description": "Create a note to save important information from the conversation",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Title of the note",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content of the note",
					},
					"project_id": map[string]any{
						"type":        "string",
						"description": "Optional project ID to link the note to",
					},
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional tags for categorization",
					},
				},
				"required": []string{"title", "content"},
			},
		},
	}
}

// ParseRequest parses a JSON string into a CreateNoteRequest.
func (t *CreateNoteTool) ParseRequest(jsonStr string) (*CreateNoteRequest, error) {
	var req CreateNoteRequest
	if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	return &req, nil
}
