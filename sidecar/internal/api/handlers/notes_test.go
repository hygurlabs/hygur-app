package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

func TestNewNotesHandler(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()

	handler := NewNotesHandler(tool, logger)
	if handler == nil {
		t.Fatal("NewNotesHandler() returned nil")
	}
	if handler.tool != tool {
		t.Error("NewNotesHandler() did not set tool correctly")
	}
}

func TestNotesHandler_Create_Success(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	reqBody := CreateNoteRequest{
		Title:   "Test Note",
		Content: "This is test content",
		TagIDs:  []string{"test", "example"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	var result NoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result.ID == "" {
		t.Error("ID should not be empty")
	}
	if result.Title != "Test Note" {
		t.Errorf("Title = %q, want %q", result.Title, "Test Note")
	}
	if result.Content != "This is test content" {
		t.Errorf("Content = %q, want %q", result.Content, "This is test content")
	}
	if result.CreatedAt == "" {
		t.Error("CreatedAt should not be empty")
	}
}

func TestNotesHandler_Create_WithProject(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create a project first
	ctx := context.Background()
	project := &store.Project{
		ProjectID: "proj-123",
		Name:      "Test Project",
		Tags:      []string{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.InsertProject(ctx, project); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	projectID := "proj-123"
	reqBody := CreateNoteRequest{
		Title:     "Project Note",
		Content:   "This is linked to a project",
		ProjectID: &projectID,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestNotesHandler_Create_MissingTitle(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	reqBody := CreateNoteRequest{
		Title:   "",
		Content: "Some content",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var errResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	errObj := errResp["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("Error code = %v, want VALIDATION_ERROR", errObj["code"])
	}
}

func TestNotesHandler_Create_MissingContent(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	reqBody := CreateNoteRequest{
		Title:   "Some Title",
		Content: "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestNotesHandler_Create_InvalidJSON(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader([]byte("not valid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var errResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	errObj := errResp["error"].(map[string]any)
	if errObj["code"] != "BAD_REQUEST" {
		t.Errorf("Error code = %v, want BAD_REQUEST", errObj["code"])
	}
}

func TestNotesHandler_Create_ProjectNotFound(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tool := tools.NewCreateNoteTool(db)
	logger := zerolog.Nop()
	handler := NewNotesHandler(tool, logger)

	projectID := "non-existent-project"
	reqBody := CreateNoteRequest{
		Title:     "Test Note",
		Content:   "Content",
		ProjectID: &projectID,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	var errResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	errObj := errResp["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Errorf("Error code = %v, want NOT_FOUND", errObj["code"])
	}
}

func TestNotesHandler_Create_NilTool(t *testing.T) {
	logger := zerolog.Nop()
	handler := NewNotesHandler(nil, logger)

	reqBody := CreateNoteRequest{
		Title:   "Test",
		Content: "Content",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Create(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello", "hello", true},
		{"hello", "x", false},
		{"", "", true},
		{"hello", "", true},
		{"", "x", false},
		{"project not found", "project not found", true},
		{"error: project not found", "project not found", true},
	}

	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}
