package store

import (
	"context"
	"testing"
	"time"
)

func TestNewDB(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Verify schema version
	version, err := db.GetSchemaVersion()
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != CurrentSchemaVersion {
		t.Errorf("expected schema version %d, got %d", CurrentSchemaVersion, version)
	}
}

func TestKnowledgeItemCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	sourcePath := "/path/to/file.md"

	item := &KnowledgeItem{
		ContentID:      "ki-001",
		SourceType:     "markdown",
		SourcePath:     &sourcePath,
		Title:          "Test Document",
		NormalizedText: "This is the normalized text content.",
		Metadata: map[string]any{
			"author": "Test Author",
			"tags":   []any{"test", "document"},
		},
		VersionID: "v1",
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Insert
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Get
	retrieved, err := db.GetKnowledgeItem(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to get knowledge item: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected knowledge item, got nil")
	}
	if retrieved.ContentID != item.ContentID {
		t.Errorf("expected content_id %s, got %s", item.ContentID, retrieved.ContentID)
	}
	if retrieved.Title != item.Title {
		t.Errorf("expected title %s, got %s", item.Title, retrieved.Title)
	}
	if retrieved.SourcePath == nil || *retrieved.SourcePath != sourcePath {
		t.Errorf("expected source_path %s, got %v", sourcePath, retrieved.SourcePath)
	}
	if retrieved.Metadata["author"] != "Test Author" {
		t.Errorf("expected author 'Test Author', got %v", retrieved.Metadata["author"])
	}

	// Get non-existent
	notFound, err := db.GetKnowledgeItem(ctx, "non-existent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent item")
	}

	// List
	items, err := db.ListKnowledgeItems(ctx, 10, 0)
	if err != nil {
		t.Fatalf("failed to list knowledge items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}

	// Update
	item.Title = "Updated Title"
	err = db.UpdateKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to update knowledge item: %v", err)
	}

	updated, err := db.GetKnowledgeItem(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to get updated item: %v", err)
	}
	if updated.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %s", updated.Title)
	}

	// Delete
	err = db.DeleteKnowledgeItem(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to delete knowledge item: %v", err)
	}

	deleted, err := db.GetKnowledgeItem(ctx, "ki-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Error("expected nil after deletion")
	}

	// Delete non-existent
	err = db.DeleteKnowledgeItem(ctx, "non-existent")
	if err == nil {
		t.Error("expected error when deleting non-existent item")
	}
}

func TestChunkCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// First, create a knowledge item (parent)
	item := &KnowledgeItem{
		ContentID:      "ki-001",
		SourceType:     "markdown",
		Title:          "Test Document",
		NormalizedText: "Full text content.",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	embeddingModel := "text-embedding-ada-002"
	chunk := &Chunk{
		ChunkID:        "ch-001",
		ContentID:      "ki-001",
		ChunkHash:      "abc123hash",
		EmbeddingModel: &embeddingModel,
		Text:           "This is chunk 1 text.",
		Metadata: map[string]any{
			"position": 0,
		},
		CreatedAt: now,
	}

	// Insert
	err = db.InsertChunk(ctx, chunk)
	if err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Insert second chunk
	chunk2 := &Chunk{
		ChunkID:   "ch-002",
		ContentID: "ki-001",
		ChunkHash: "def456hash",
		Text:      "This is chunk 2 text.",
		CreatedAt: now.Add(time.Second),
	}
	err = db.InsertChunk(ctx, chunk2)
	if err != nil {
		t.Fatalf("failed to insert chunk 2: %v", err)
	}

	// Get by content ID
	chunks, err := db.GetChunksByContentID(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to get chunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ChunkID != "ch-001" {
		t.Errorf("expected first chunk to be ch-001, got %s", chunks[0].ChunkID)
	}
	if chunks[0].EmbeddingModel == nil || *chunks[0].EmbeddingModel != embeddingModel {
		t.Errorf("expected embedding model %s, got %v", embeddingModel, chunks[0].EmbeddingModel)
	}

	// Delete by content ID
	err = db.DeleteChunksByContentID(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to delete chunks: %v", err)
	}

	remainingChunks, err := db.GetChunksByContentID(ctx, "ki-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(remainingChunks) != 0 {
		t.Errorf("expected 0 chunks after deletion, got %d", len(remainingChunks))
	}
}

func TestChunkCascadeDelete(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create knowledge item
	item := &KnowledgeItem{
		ContentID:      "ki-cascade",
		SourceType:     "markdown",
		Title:          "Cascade Test",
		NormalizedText: "Content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	// Create chunk
	chunk := &Chunk{
		ChunkID:   "ch-cascade",
		ContentID: "ki-cascade",
		ChunkHash: "hash123",
		Text:      "Chunk text",
		CreatedAt: now,
	}
	err = db.InsertChunk(ctx, chunk)
	if err != nil {
		t.Fatalf("failed to insert chunk: %v", err)
	}

	// Delete knowledge item - chunks should be cascade deleted
	err = db.DeleteKnowledgeItem(ctx, "ki-cascade")
	if err != nil {
		t.Fatalf("failed to delete knowledge item: %v", err)
	}

	// Verify chunks are deleted
	chunks, err := db.GetChunksByContentID(ctx, "ki-cascade")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks after cascade delete, got %d", len(chunks))
	}
}

func TestProjectCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	description := "Test project description"

	project := &Project{
		ProjectID:   "proj-001",
		Name:        "Test Project",
		Description: &description,
		Archived:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Insert
	err = db.InsertProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	// Get
	retrieved, err := db.GetProject(ctx, "proj-001")
	if err != nil {
		t.Fatalf("failed to get project: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected project, got nil")
	}
	if retrieved.Name != project.Name {
		t.Errorf("expected name %s, got %s", project.Name, retrieved.Name)
	}
	if retrieved.Description == nil || *retrieved.Description != description {
		t.Errorf("expected description %s, got %v", description, retrieved.Description)
	}

	// Get non-existent
	notFound, err := db.GetProject(ctx, "non-existent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent project")
	}

	// List
	projects, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}

	// Update
	project.Name = "Updated Project"
	project.Archived = true
	err = db.UpdateProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to update project: %v", err)
	}

	updated, err := db.GetProject(ctx, "proj-001")
	if err != nil {
		t.Fatalf("failed to get updated project: %v", err)
	}
	if updated.Name != "Updated Project" {
		t.Errorf("expected name 'Updated Project', got %s", updated.Name)
	}
	if !updated.Archived {
		t.Error("expected archived to be true")
	}

	// Delete
	err = db.DeleteProject(ctx, "proj-001")
	if err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}

	deleted, err := db.GetProject(ctx, "proj-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Error("expected nil after deletion")
	}
}

func TestProjectLinkCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisite items
	item := &KnowledgeItem{
		ContentID:      "ki-link",
		SourceType:     "markdown",
		Title:          "Link Test Item",
		NormalizedText: "Content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	project := &Project{
		ProjectID: "proj-link",
		Name:      "Link Test Project",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = db.InsertProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	localTitle := "Local Title"
	localNotes := "Some notes"
	link := &ProjectLink{
		LinkID:     "link-001",
		ProjectID:  "proj-link",
		ContentID:  "ki-link",
		LocalTitle: &localTitle,
		LocalNotes: &localNotes,
		PinState:   true,
		LocalTags:  []string{"tag1", "tag2"},
		CreatedAt:  now,
	}

	// Insert
	err = db.InsertProjectLink(ctx, link)
	if err != nil {
		t.Fatalf("failed to insert project link: %v", err)
	}

	// Get links for project
	links, err := db.GetProjectLinks(ctx, "proj-link")
	if err != nil {
		t.Fatalf("failed to get project links: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("expected 1 link, got %d", len(links))
	}
	if links[0].LocalTitle == nil || *links[0].LocalTitle != localTitle {
		t.Errorf("expected local title %s, got %v", localTitle, links[0].LocalTitle)
	}
	if len(links[0].LocalTags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(links[0].LocalTags))
	}

	// Delete link
	err = db.DeleteProjectLink(ctx, "link-001")
	if err != nil {
		t.Fatalf("failed to delete project link: %v", err)
	}

	remainingLinks, err := db.GetProjectLinks(ctx, "proj-link")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(remainingLinks) != 0 {
		t.Errorf("expected 0 links after deletion, got %d", len(remainingLinks))
	}
}

func TestProjectLinkCascadeDelete(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisites
	item := &KnowledgeItem{
		ContentID:      "ki-cascade-link",
		SourceType:     "markdown",
		Title:          "Cascade Link Test",
		NormalizedText: "Content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	project := &Project{
		ProjectID: "proj-cascade-link",
		Name:      "Cascade Link Project",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = db.InsertProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	link := &ProjectLink{
		LinkID:    "link-cascade",
		ProjectID: "proj-cascade-link",
		ContentID: "ki-cascade-link",
		CreatedAt: now,
	}
	err = db.InsertProjectLink(ctx, link)
	if err != nil {
		t.Fatalf("failed to insert project link: %v", err)
	}

	// Delete project - links should be cascade deleted
	err = db.DeleteProject(ctx, "proj-cascade-link")
	if err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}

	// Verify links are deleted (by trying to get them for the deleted project)
	links, err := db.GetProjectLinks(ctx, "proj-cascade-link")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected 0 links after cascade delete, got %d", len(links))
	}
}

func TestKnowledgeItemCascadeDeletesLinks(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Create prerequisites
	item := &KnowledgeItem{
		ContentID:      "ki-cascade-link2",
		SourceType:     "markdown",
		Title:          "Cascade Link Test 2",
		NormalizedText: "Content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	project := &Project{
		ProjectID: "proj-cascade-link2",
		Name:      "Cascade Link Project 2",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = db.InsertProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	link := &ProjectLink{
		LinkID:    "link-cascade2",
		ProjectID: "proj-cascade-link2",
		ContentID: "ki-cascade-link2",
		CreatedAt: now,
	}
	err = db.InsertProjectLink(ctx, link)
	if err != nil {
		t.Fatalf("failed to insert project link: %v", err)
	}

	// Delete knowledge item - links should be cascade deleted
	err = db.DeleteKnowledgeItem(ctx, "ki-cascade-link2")
	if err != nil {
		t.Fatalf("failed to delete knowledge item: %v", err)
	}

	// Verify links are deleted
	links, err := db.GetProjectLinks(ctx, "proj-cascade-link2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected 0 links after cascade delete, got %d", len(links))
	}
}

func TestSummaryCRUD(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	summary := &Summary{
		SummaryID:     "sum-001",
		SourceRef:     "ki-001",
		ModelUsed:     "gpt-4",
		Decisions:     []string{"Decision 1", "Decision 2"},
		Actions:       []string{"Action 1"},
		OpenQuestions: []string{"Question 1", "Question 2", "Question 3"},
		CreatedAt:     now,
	}

	// Insert
	err = db.InsertSummary(ctx, summary)
	if err != nil {
		t.Fatalf("failed to insert summary: %v", err)
	}

	// Get
	retrieved, err := db.GetSummary(ctx, "sum-001")
	if err != nil {
		t.Fatalf("failed to get summary: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected summary, got nil")
	}
	if retrieved.ModelUsed != summary.ModelUsed {
		t.Errorf("expected model_used %s, got %s", summary.ModelUsed, retrieved.ModelUsed)
	}
	if len(retrieved.Decisions) != 2 {
		t.Errorf("expected 2 decisions, got %d", len(retrieved.Decisions))
	}
	if len(retrieved.Actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(retrieved.Actions))
	}
	if len(retrieved.OpenQuestions) != 3 {
		t.Errorf("expected 3 open questions, got %d", len(retrieved.OpenQuestions))
	}

	// Get non-existent
	notFound, err := db.GetSummary(ctx, "non-existent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil for non-existent summary")
	}

	// Insert another summary for same source
	summary2 := &Summary{
		SummaryID: "sum-002",
		SourceRef: "ki-001",
		ModelUsed: "gpt-4-turbo",
		Decisions: []string{"New decision"},
		CreatedAt: now.Add(time.Hour),
	}
	err = db.InsertSummary(ctx, summary2)
	if err != nil {
		t.Fatalf("failed to insert summary 2: %v", err)
	}

	// Get by source ref
	summaries, err := db.GetSummariesBySourceRef(ctx, "ki-001")
	if err != nil {
		t.Fatalf("failed to get summaries by source ref: %v", err)
	}
	if len(summaries) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(summaries))
	}
	// Should be ordered by created_at DESC
	if summaries[0].SummaryID != "sum-002" {
		t.Errorf("expected first summary to be sum-002 (newer), got %s", summaries[0].SummaryID)
	}
}

func TestForeignKeyConstraint(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Try to insert a chunk without a parent knowledge item
	chunk := &Chunk{
		ChunkID:   "ch-orphan",
		ContentID: "non-existent-ki",
		ChunkHash: "hash",
		Text:      "Orphan chunk",
		CreatedAt: now,
	}
	err = db.InsertChunk(ctx, chunk)
	if err == nil {
		t.Error("expected error when inserting chunk with non-existent content_id")
	}

	// Try to insert a project link without parent project
	link := &ProjectLink{
		LinkID:    "link-orphan",
		ProjectID: "non-existent-proj",
		ContentID: "non-existent-ki",
		CreatedAt: now,
	}
	err = db.InsertProjectLink(ctx, link)
	if err == nil {
		t.Error("expected error when inserting project link with non-existent project_id")
	}
}

func TestUniqueConstraints(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Test unique project name
	project1 := &Project{
		ProjectID: "proj-unique-1",
		Name:      "Unique Name",
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = db.InsertProject(ctx, project1)
	if err != nil {
		t.Fatalf("failed to insert project 1: %v", err)
	}

	project2 := &Project{
		ProjectID: "proj-unique-2",
		Name:      "Unique Name", // Same name
		CreatedAt: now,
		UpdatedAt: now,
	}
	err = db.InsertProject(ctx, project2)
	if err == nil {
		t.Error("expected error when inserting project with duplicate name")
	}

	// Test unique project link (project_id, content_id)
	item := &KnowledgeItem{
		ContentID:      "ki-unique",
		SourceType:     "markdown",
		Title:          "Unique Test",
		NormalizedText: "Content",
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item: %v", err)
	}

	link1 := &ProjectLink{
		LinkID:    "link-unique-1",
		ProjectID: "proj-unique-1",
		ContentID: "ki-unique",
		CreatedAt: now,
	}
	err = db.InsertProjectLink(ctx, link1)
	if err != nil {
		t.Fatalf("failed to insert link 1: %v", err)
	}

	link2 := &ProjectLink{
		LinkID:    "link-unique-2",
		ProjectID: "proj-unique-1",
		ContentID: "ki-unique", // Same project_id + content_id
		CreatedAt: now,
	}
	err = db.InsertProjectLink(ctx, link2)
	if err == nil {
		t.Error("expected error when inserting duplicate project link")
	}
}

func TestNullableFields(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Test knowledge item with nil optional fields
	item := &KnowledgeItem{
		ContentID:      "ki-nullable",
		SourceType:     "markdown",
		SourcePath:     nil, // nullable
		Title:          "Nullable Test",
		NormalizedText: "Content",
		Metadata:       nil, // nullable
		VersionID:      "v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to insert knowledge item with nil fields: %v", err)
	}

	retrieved, err := db.GetKnowledgeItem(ctx, "ki-nullable")
	if err != nil {
		t.Fatalf("failed to get knowledge item: %v", err)
	}
	if retrieved.SourcePath != nil {
		t.Errorf("expected nil source_path, got %v", retrieved.SourcePath)
	}
	if retrieved.Metadata != nil {
		t.Errorf("expected nil metadata, got %v", retrieved.Metadata)
	}

	// Test project with nil description
	project := &Project{
		ProjectID:   "proj-nullable",
		Name:        "Nullable Project",
		Description: nil, // nullable
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = db.InsertProject(ctx, project)
	if err != nil {
		t.Fatalf("failed to insert project with nil description: %v", err)
	}

	retrievedProj, err := db.GetProject(ctx, "proj-nullable")
	if err != nil {
		t.Fatalf("failed to get project: %v", err)
	}
	if retrievedProj.Description != nil {
		t.Errorf("expected nil description, got %v", retrievedProj.Description)
	}

	// Test chunk with nil embedding model
	chunk := &Chunk{
		ChunkID:        "ch-nullable",
		ContentID:      "ki-nullable",
		ChunkHash:      "hash",
		EmbeddingModel: nil, // nullable
		Text:           "Chunk text",
		Metadata:       nil, // nullable
		CreatedAt:      now,
	}
	err = db.InsertChunk(ctx, chunk)
	if err != nil {
		t.Fatalf("failed to insert chunk with nil fields: %v", err)
	}

	chunks, err := db.GetChunksByContentID(ctx, "ki-nullable")
	if err != nil {
		t.Fatalf("failed to get chunks: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].EmbeddingModel != nil {
		t.Errorf("expected nil embedding_model, got %v", chunks[0].EmbeddingModel)
	}
}

func TestPagination(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert multiple items
	for i := 0; i < 5; i++ {
		item := &KnowledgeItem{
			ContentID:      "ki-page-" + string(rune('A'+i)),
			SourceType:     "markdown",
			Title:          "Page Test " + string(rune('A'+i)),
			NormalizedText: "Content",
			VersionID:      "v1",
			CreatedAt:      time.Now().Add(time.Duration(i) * time.Second),
			UpdatedAt:      time.Now().Add(time.Duration(i) * time.Second),
		}
		err := db.InsertKnowledgeItem(ctx, item)
		if err != nil {
			t.Fatalf("failed to insert item %d: %v", i, err)
		}
	}

	// Test pagination
	page1, err := db.ListKnowledgeItems(ctx, 2, 0)
	if err != nil {
		t.Fatalf("failed to get page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("expected 2 items in page 1, got %d", len(page1))
	}

	page2, err := db.ListKnowledgeItems(ctx, 2, 2)
	if err != nil {
		t.Fatalf("failed to get page 2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("expected 2 items in page 2, got %d", len(page2))
	}

	page3, err := db.ListKnowledgeItems(ctx, 2, 4)
	if err != nil {
		t.Fatalf("failed to get page 3: %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("expected 1 item in page 3, got %d", len(page3))
	}

	// Verify different items on different pages
	if page1[0].ContentID == page2[0].ContentID {
		t.Error("expected different items on different pages")
	}
}

func TestCountMailItemsByAccount(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	// Mail items use the real indexer format: content_id = "email:{thread_id}",
	// source_type = "email", and the originating account is recorded in
	// metadata.account_id. CountMailItemsByAccount filters via json_extract so
	// the test fixture must mirror that shape.
	insert := func(contentID, sourceType, accountID string, when time.Time) {
		t.Helper()
		var metadata map[string]any
		if accountID != "" {
			metadata = map[string]any{"account_id": accountID}
		}
		err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID:      contentID,
			SourceType:     sourceType,
			Title:          contentID,
			NormalizedText: "x",
			Metadata:       metadata,
			VersionID:      "v1",
			CreatedAt:      when,
			UpdatedAt:      when,
		})
		if err != nil {
			t.Fatalf("insert %s: %v", contentID, err)
		}
	}

	// Three threads for alice (multi-account format), one legacy gmail row,
	// one for bob, one note that must be ignored.
	insert("email:t1", "email", "alice@gmail.com", now.Add(-2*time.Hour))
	insert("email:t2", "email", "alice@gmail.com", now.Add(-1*time.Hour))
	insert("email:t3", "email", "alice@gmail.com", now)
	insert("email:t4", "email", "gmail", now) // legacy: provider name as account_id
	insert("email:t5", "email", "bob@proton.me", now)
	insert("note:n1", "markdown", "alice@gmail.com", now) // wrong source_type — ignore

	// Multi-account path: exact email match only (no provider fallback).
	count, lastIndexed, err := db.CountMailItemsByAccount(ctx, "alice@gmail.com", "")
	if err != nil {
		t.Fatalf("count alice: %v", err)
	}
	if count != 3 {
		t.Errorf("alice count (no fallback) = %d, want 3", count)
	}
	if lastIndexed.IsZero() {
		t.Error("alice last indexed should not be zero")
	}

	// With provider fallback: picks up the legacy "gmail" row as well.
	count, _, err = db.CountMailItemsByAccount(ctx, "alice@gmail.com", "gmail")
	if err != nil {
		t.Fatalf("count alice with fallback: %v", err)
	}
	if count != 4 {
		t.Errorf("alice count (with gmail fallback) = %d, want 4", count)
	}

	count, _, err = db.CountMailItemsByAccount(ctx, "bob@proton.me", "proton")
	if err != nil {
		t.Fatalf("count bob: %v", err)
	}
	if count != 1 {
		t.Errorf("bob count = %d, want 1", count)
	}

	count, lastIndexed, err = db.CountMailItemsByAccount(ctx, "noone@example.com", "")
	if err != nil {
		t.Fatalf("count empty: %v", err)
	}
	if count != 0 {
		t.Errorf("noone count = %d, want 0", count)
	}
	if !lastIndexed.IsZero() {
		t.Errorf("noone last indexed should be zero, got %v", lastIndexed)
	}

	if _, _, err := db.CountMailItemsByAccount(ctx, "", ""); err == nil {
		t.Error("empty account_id should return error")
	}
}
