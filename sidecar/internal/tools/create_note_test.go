package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

func TestNewCreateNoteTool(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	if tool == nil {
		t.Fatal("NewCreateNoteTool() returned nil")
	}
	if tool.store != db {
		t.Error("NewCreateNoteTool() did not set store correctly")
	}
}

func TestCreateNoteTool_Run_Success(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	req := CreateNoteRequest{
		Title:   "Test Note",
		Content: "This is the content of my test note.",
		Tags:    []string{"test", "example"},
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Verify response
	if resp.ContentID == "" {
		t.Error("ContentID should not be empty")
	}
	if !strings.HasPrefix(resp.ContentID, "note:") {
		t.Errorf("ContentID = %q, want prefix 'note:'", resp.ContentID)
	}
	if resp.Title != "Test Note" {
		t.Errorf("Title = %q, want %q", resp.Title, "Test Note")
	}
	if resp.SourceType != "note" {
		t.Errorf("SourceType = %q, want %q", resp.SourceType, "note")
	}
	if resp.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// Verify item was saved to database
	item, err := db.GetKnowledgeItem(ctx, resp.ContentID)
	if err != nil {
		t.Fatalf("GetKnowledgeItem() failed: %v", err)
	}
	if item == nil {
		t.Fatal("Item was not saved to database")
	}

	// Verify item properties
	if item.SourceType != "note" {
		t.Errorf("Item.SourceType = %q, want %q", item.SourceType, "note")
	}
	if item.Title != "Test Note" {
		t.Errorf("Item.Title = %q, want %q", item.Title, "Test Note")
	}
	if item.SourcePath != nil {
		t.Error("Item.SourcePath should be nil for notes")
	}

	// Verify normalized text
	expectedNormalized := "this is the content of my test note."
	if item.NormalizedText != expectedNormalized {
		t.Errorf("Item.NormalizedText = %q, want %q", item.NormalizedText, expectedNormalized)
	}

	// Verify metadata
	if item.Metadata == nil {
		t.Fatal("Item.Metadata should not be nil")
	}
	if item.Metadata["created_from"] != "tool" {
		t.Errorf("Metadata[created_from] = %v, want %q", item.Metadata["created_from"], "tool")
	}

	// Verify tags in metadata
	tags, ok := item.Metadata["tags"].([]any)
	if !ok {
		t.Fatal("Metadata[tags] should be a slice")
	}
	if len(tags) != 2 {
		t.Errorf("len(tags) = %d, want 2", len(tags))
	}
}

func TestCreateNoteTool_Run_WithProject(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a project first
	project := &store.Project{
		ProjectID:   "project-123",
		Name:        "Test Project",
		Description: nil,
		Tags:        []string{},
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(ctx, project); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	tool := NewCreateNoteTool(db)

	projectID := "project-123"
	req := CreateNoteRequest{
		Title:     "Project Note",
		Content:   "This note is linked to a project.",
		ProjectID: &projectID,
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Verify project link was created
	links, err := db.GetProjectLinks(ctx, projectID)
	if err != nil {
		t.Fatalf("GetProjectLinks() failed: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("len(links) = %d, want 1", len(links))
	}
	if links[0].ContentID != resp.ContentID {
		t.Errorf("Link.ContentID = %q, want %q", links[0].ContentID, resp.ContentID)
	}
}

func TestCreateNoteTool_Run_ProjectNotFound(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	nonExistentProjectID := "project-does-not-exist"
	req := CreateNoteRequest{
		Title:     "Orphan Note",
		Content:   "This project doesn't exist.",
		ProjectID: &nonExistentProjectID,
	}

	_, err = tool.Run(ctx, req)
	if err == nil {
		t.Error("Run() should fail when project doesn't exist")
	}
	if !strings.Contains(err.Error(), "project not found") {
		t.Errorf("Error should mention 'project not found', got: %v", err)
	}
}

func TestCreateNoteTool_Run_EmptyTitle(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	req := CreateNoteRequest{
		Title:   "",
		Content: "Some content",
	}

	_, err = tool.Run(ctx, req)
	if err == nil {
		t.Error("Run() should fail with empty title")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("Error should mention 'title is required', got: %v", err)
	}
}

func TestCreateNoteTool_Run_EmptyContent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	req := CreateNoteRequest{
		Title:   "Some Title",
		Content: "",
	}

	_, err = tool.Run(ctx, req)
	if err == nil {
		t.Error("Run() should fail with empty content")
	}
	if !strings.Contains(err.Error(), "content is required") {
		t.Errorf("Error should mention 'content is required', got: %v", err)
	}
}

func TestCreateNoteTool_Run_WithoutTags(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	req := CreateNoteRequest{
		Title:   "Note Without Tags",
		Content: "Content without tags.",
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Verify item was saved
	item, err := db.GetKnowledgeItem(ctx, resp.ContentID)
	if err != nil {
		t.Fatalf("GetKnowledgeItem() failed: %v", err)
	}

	// Verify metadata has created_from but no tags
	if item.Metadata["created_from"] != "tool" {
		t.Errorf("Metadata[created_from] = %v, want %q", item.Metadata["created_from"], "tool")
	}
	if _, exists := item.Metadata["tags"]; exists {
		t.Error("Metadata[tags] should not exist when no tags provided")
	}
}

func TestCreateNoteTool_Run_NormalizesContent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	req := CreateNoteRequest{
		Title:   "Normalization Test",
		Content: "  UPPERCASE   and   extra    spaces  \n\n\t and newlines  ",
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	item, err := db.GetKnowledgeItem(ctx, resp.ContentID)
	if err != nil {
		t.Fatalf("GetKnowledgeItem() failed: %v", err)
	}

	// Should be lowercase, trimmed, and spaces collapsed
	expected := "uppercase and extra spaces and newlines"
	if item.NormalizedText != expected {
		t.Errorf("NormalizedText = %q, want %q", item.NormalizedText, expected)
	}
}

func TestCreateNoteTool_ToolDefinition(t *testing.T) {
	tool := &CreateNoteTool{}
	def := tool.ToolDefinition()

	if def["type"] != "function" {
		t.Errorf("def[type] = %v, want %q", def["type"], "function")
	}

	fn, ok := def["function"].(map[string]any)
	if !ok {
		t.Fatal("def[function] should be a map")
	}

	if fn["name"] != "create_note" {
		t.Errorf("function.name = %v, want %q", fn["name"], "create_note")
	}

	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("function.parameters should be a map")
	}

	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("parameters.required should be a string slice")
	}

	hasTitle := false
	hasContent := false
	for _, r := range required {
		if r == "title" {
			hasTitle = true
		}
		if r == "content" {
			hasContent = true
		}
	}
	if !hasTitle || !hasContent {
		t.Error("required should contain 'title' and 'content'")
	}
}

func TestCreateNoteTool_ParseRequest(t *testing.T) {
	tool := &CreateNoteTool{}

	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*CreateNoteRequest) error
	}{
		{
			name:    "valid full request",
			input:   `{"title": "My Note", "content": "Content here", "project_id": "proj-1", "tags": ["a", "b"]}`,
			wantErr: false,
			check: func(req *CreateNoteRequest) error {
				if req.Title != "My Note" {
					return errorf("Title = %q, want %q", req.Title, "My Note")
				}
				if req.Content != "Content here" {
					return errorf("Content = %q, want %q", req.Content, "Content here")
				}
				if req.ProjectID == nil || *req.ProjectID != "proj-1" {
					return errorf("ProjectID = %v, want %q", req.ProjectID, "proj-1")
				}
				if len(req.Tags) != 2 {
					return errorf("len(Tags) = %d, want 2", len(req.Tags))
				}
				return nil
			},
		},
		{
			name:    "minimal request",
			input:   `{"title": "Title", "content": "Content"}`,
			wantErr: false,
			check: func(req *CreateNoteRequest) error {
				if req.ProjectID != nil {
					return errorf("ProjectID should be nil")
				}
				if len(req.Tags) != 0 {
					return errorf("Tags should be empty")
				}
				return nil
			},
		},
		{
			name:    "invalid JSON",
			input:   `{"title": "incomplete`,
			wantErr: true,
		},
		{
			name:    "empty JSON",
			input:   `{}`,
			wantErr: false,
			check: func(req *CreateNoteRequest) error {
				if req.Title != "" || req.Content != "" {
					return errorf("Empty JSON should produce empty strings")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := tool.ParseRequest(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && req != nil {
				if err := tt.check(req); err != nil {
					t.Error(err)
				}
			}
		})
	}
}

func TestCreateNoteRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateNoteRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid request",
			req:     CreateNoteRequest{Title: "Title", Content: "Content"},
			wantErr: false,
		},
		{
			name:    "empty title",
			req:     CreateNoteRequest{Title: "", Content: "Content"},
			wantErr: true,
			errMsg:  "title is required",
		},
		{
			name:    "empty content",
			req:     CreateNoteRequest{Title: "Title", Content: ""},
			wantErr: true,
			errMsg:  "content is required",
		},
		{
			name:    "both empty",
			req:     CreateNoteRequest{Title: "", Content: ""},
			wantErr: true,
			errMsg:  "title is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Error message = %q, should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestCreateNoteTool_Run_UniqueContentIDs(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	// Create multiple notes
	contentIDs := make(map[string]bool)
	for i := 0; i < 10; i++ {
		req := CreateNoteRequest{
			Title:   "Note",
			Content: "Content",
		}
		resp, err := tool.Run(ctx, req)
		if err != nil {
			t.Fatalf("Run() failed on iteration %d: %v", i, err)
		}
		if contentIDs[resp.ContentID] {
			t.Errorf("Duplicate ContentID: %s", resp.ContentID)
		}
		contentIDs[resp.ContentID] = true
	}
}

func TestCreateNoteTool_Run_EmptyProjectID(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := NewCreateNoteTool(db)
	ctx := context.Background()

	// Empty string project ID should be treated as no project
	emptyProjectID := ""
	req := CreateNoteRequest{
		Title:     "Note",
		Content:   "Content",
		ProjectID: &emptyProjectID,
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Should succeed without creating a project link
	links, err := db.GetProjectLinks(ctx, "")
	if err != nil {
		t.Fatalf("GetProjectLinks() failed: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("Should not create link for empty project ID")
	}

	// Verify note was still created
	item, err := db.GetKnowledgeItem(ctx, resp.ContentID)
	if err != nil {
		t.Fatalf("GetKnowledgeItem() failed: %v", err)
	}
	if item == nil {
		t.Error("Note should still be created even with empty project ID")
	}
}

func TestCreateNoteTool_ToolDefinition_Structure(t *testing.T) {
	tool := &CreateNoteTool{}
	def := tool.ToolDefinition()

	// Verify the structure can be serialized to JSON (important for LLM integration)
	jsonBytes, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("Failed to marshal ToolDefinition: %v", err)
	}

	// Verify it can be parsed back
	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal ToolDefinition: %v", err)
	}

	// Verify essential fields exist
	fn := parsed["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)

	requiredFields := []string{"title", "content", "project_id", "tags"}
	for _, field := range requiredFields {
		if _, exists := props[field]; !exists {
			t.Errorf("Property %q should exist in tool definition", field)
		}
	}
}

// errorf is a helper to return formatted errors in check functions
func errorf(format string, args ...any) error {
	return &checkError{msg: format, args: args}
}

type checkError struct {
	msg  string
	args []any
}

func (e *checkError) Error() string {
	return strings.ReplaceAll(e.msg, "%", "%%")
}
