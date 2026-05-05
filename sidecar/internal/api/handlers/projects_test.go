package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
)

// createProjectTestRouter creates a chi router with the project handler mounted.
func createProjectTestRouter(h *ProjectHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/projects", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Get("/{id}", h.Get)
		r.Put("/{id}", h.Update)
		r.Delete("/{id}", h.Delete)
	})
	return r
}

// TestProjectHandler_Create_Success tests creating a project successfully.
func TestProjectHandler_Create_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	reqBody := CreateProjectRequest{
		Name:        "Test Project",
		Description: "A test project description",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID == "" {
		t.Error("expected id to be set")
	}

	if resp.Name != "Test Project" {
		t.Errorf("expected name 'Test Project', got '%s'", resp.Name)
	}

	if resp.Description != "A test project description" {
		t.Errorf("expected description 'A test project description', got '%s'", resp.Description)
	}

	if resp.ItemCount != 0 {
		t.Errorf("expected item_count 0, got %d", resp.ItemCount)
	}

	if resp.Archived {
		t.Error("expected archived to be false")
	}
}

// TestProjectHandler_Create_EmptyName tests creating a project without name.
func TestProjectHandler_Create_EmptyName(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	reqBody := CreateProjectRequest{
		Name: "",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("expected code 'VALIDATION_ERROR', got '%s'", errorObj["code"])
	}
}

// TestProjectHandler_Create_InvalidJSON tests creating a project with invalid JSON.
func TestProjectHandler_Create_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestProjectHandler_Create_DuplicateName tests creating a project with duplicate name.
func TestProjectHandler_Create_DuplicateName(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a project first
	project := &store.Project{
		ProjectID:   "existing-project-id",
		Name:        "Existing Project",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	reqBody := CreateProjectRequest{
		Name: "Existing Project", // Same name
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

// TestProjectHandler_List_Empty tests listing projects when none exist.
func TestProjectHandler_List_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp []ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp) != 0 {
		t.Errorf("expected 0 projects, got %d", len(resp))
	}
}

// TestProjectHandler_List_WithProjects tests listing projects when some exist.
func TestProjectHandler_List_WithProjects(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert some projects
	for i := 0; i < 3; i++ {
		project := &store.Project{
			ProjectID:   "project-" + string(rune('a'+i)),
			Name:        "Project " + string(rune('A'+i)),
			Description: nil,
			Archived:    false,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := db.InsertProject(context.Background(), project); err != nil {
			t.Fatalf("failed to insert test project: %v", err)
		}
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp []ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp) != 3 {
		t.Errorf("expected 3 projects, got %d", len(resp))
	}
}

// TestProjectHandler_Get_Exists tests getting an existing project.
func TestProjectHandler_Get_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	desc := "Test description"
	project := &store.Project{
		ProjectID:   "test-project-id",
		Name:        "Test Project",
		Description: &desc,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects/test-project-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ID != "test-project-id" {
		t.Errorf("expected id 'test-project-id', got '%s'", resp.ID)
	}

	if resp.Name != "Test Project" {
		t.Errorf("expected name 'Test Project', got '%s'", resp.Name)
	}

	if resp.Description != "Test description" {
		t.Errorf("expected description 'Test description', got '%s'", resp.Description)
	}
}

// TestProjectHandler_Get_NotFound tests getting a non-existent project.
func TestProjectHandler_Get_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects/nonexistent-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}

	var errResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}

	if errorObj["code"] != "NOT_FOUND" {
		t.Errorf("expected code 'NOT_FOUND', got '%s'", errorObj["code"])
	}
}

// TestProjectHandler_Update_Success tests updating a project successfully.
func TestProjectHandler_Update_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	project := &store.Project{
		ProjectID:   "test-update-id",
		Name:        "Original Name",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	newName := "Updated Name"
	newDesc := "Updated description"
	reqBody := UpdateProjectRequest{
		Name:        &newName,
		Description: &newDesc,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/projects/test-update-id", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Name != "Updated Name" {
		t.Errorf("expected name 'Updated Name', got '%s'", resp.Name)
	}

	if resp.Description != "Updated description" {
		t.Errorf("expected description 'Updated description', got '%s'", resp.Description)
	}
}

// TestProjectHandler_Update_Archive tests archiving a project.
func TestProjectHandler_Update_Archive(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	project := &store.Project{
		ProjectID:   "test-archive-id",
		Name:        "Project to Archive",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	archived := true
	reqBody := UpdateProjectRequest{
		Archived: &archived,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/projects/test-archive-id", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Archived {
		t.Error("expected archived to be true")
	}
}

// TestProjectHandler_Update_NotFound tests updating a non-existent project.
func TestProjectHandler_Update_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	newName := "New Name"
	reqBody := UpdateProjectRequest{
		Name: &newName,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/projects/nonexistent-id", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestProjectHandler_Update_EmptyName tests updating with empty name.
func TestProjectHandler_Update_EmptyName(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	project := &store.Project{
		ProjectID:   "test-empty-name-id",
		Name:        "Original Name",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	emptyName := ""
	reqBody := UpdateProjectRequest{
		Name: &emptyName,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/projects/test-empty-name-id", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// TestProjectHandler_Delete_Success tests deleting a project successfully.
func TestProjectHandler_Delete_Success(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	project := &store.Project{
		ProjectID:   "test-delete-id",
		Name:        "Project to Delete",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/projects/test-delete-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d: %s", http.StatusNoContent, rec.Code, rec.Body.String())
	}

	// Verify project was deleted
	deletedProject, err := db.GetProject(context.Background(), "test-delete-id")
	if err != nil {
		t.Fatalf("failed to check deleted project: %v", err)
	}
	if deletedProject != nil {
		t.Error("expected project to be deleted")
	}
}

// TestProjectHandler_Delete_NotFound tests deleting a non-existent project.
func TestProjectHandler_Delete_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodDelete, "/projects/nonexistent-id", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
}

// TestProjectHandler_List_WithItemCount tests that item count is properly returned.
func TestProjectHandler_List_WithItemCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a project
	project := &store.Project{
		ProjectID:   "project-with-items",
		Name:        "Project With Items",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	// Insert a knowledge item
	item := &store.KnowledgeItem{
		ContentID:      "test-content-id",
		SourceType:     "file",
		Title:          "Test Document",
		NormalizedText: "Content for project.",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
		t.Fatalf("failed to insert test item: %v", err)
	}

	// Link item to project
	link := &store.ProjectLink{
		LinkID:    "test-link-id",
		ProjectID: "project-with-items",
		ContentID: "test-content-id",
		PinState:  false,
		CreatedAt: time.Now(),
	}
	if err := db.InsertProjectLink(context.Background(), link); err != nil {
		t.Fatalf("failed to insert project link: %v", err)
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp []ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp) != 1 {
		t.Fatalf("expected 1 project, got %d", len(resp))
	}

	if resp[0].ItemCount != 1 {
		t.Errorf("expected item_count 1, got %d", resp[0].ItemCount)
	}
}

// TestProjectHandler_Get_WithItemCount tests that item count is returned for single project.
func TestProjectHandler_Get_WithItemCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert a project
	project := &store.Project{
		ProjectID:   "project-get-items",
		Name:        "Project for Get",
		Description: nil,
		Archived:    false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := db.InsertProject(context.Background(), project); err != nil {
		t.Fatalf("failed to insert test project: %v", err)
	}

	// Insert knowledge items and link them
	for i := 0; i < 5; i++ {
		item := &store.KnowledgeItem{
			ContentID:      "content-" + string(rune('a'+i)),
			SourceType:     "file",
			Title:          "Document " + string(rune('A'+i)),
			NormalizedText: "Content " + string(rune('a'+i)),
			VersionID:      "v1",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := db.InsertKnowledgeItem(context.Background(), item); err != nil {
			t.Fatalf("failed to insert test item: %v", err)
		}

		link := &store.ProjectLink{
			LinkID:    "link-" + string(rune('a'+i)),
			ProjectID: "project-get-items",
			ContentID: "content-" + string(rune('a'+i)),
			PinState:  false,
			CreatedAt: time.Now(),
		}
		if err := db.InsertProjectLink(context.Background(), link); err != nil {
			t.Fatalf("failed to insert project link: %v", err)
		}
	}

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/projects/project-get-items", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.ItemCount != 5 {
		t.Errorf("expected item_count 5, got %d", resp.ItemCount)
	}
}

// TestProjectHandler_Create_WithoutDescription tests creating a project without description.
func TestProjectHandler_Create_WithoutDescription(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	h := NewProjectHandler(db, testLogger())
	router := createProjectTestRouter(h)

	reqBody := CreateProjectRequest{
		Name: "Project Without Description",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var resp ProjectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Description != "" {
		t.Errorf("expected empty description, got '%s'", resp.Description)
	}
}
