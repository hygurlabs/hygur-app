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
	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func setupTagTestDB(t *testing.T) *store.DB {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	return db
}

func setupTagHandler(db *store.DB) *TagHandler {
	logger := zerolog.Nop()
	return NewTagHandler(db, logger)
}

func TestTagHandler_List(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	ctx := context.Background()

	// Create some tags
	for _, name := range []string{"Tag1", "Tag2", "Tag3"} {
		tag := &store.Tag{
			ID:    uuid.New().String(),
			Name:  name,
			Color: store.DefaultTagColor(name),
		}
		err := db.CreateTag(ctx, tag)
		if err != nil {
			t.Fatalf("failed to create tag: %v", err)
		}
	}

	// Test list
	req := httptest.NewRequest(http.MethodGet, "/tags", nil)
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp TagListResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	if err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Tags) != 3 {
		t.Errorf("expected 3 tags, got %d", len(resp.Tags))
	}
}

func TestTagHandler_Create(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	t.Run("Success", func(t *testing.T) {
		body := CreateTagRequest{
			Name:  "NewTag",
			Color: "#FF5733",
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/tags", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.Create(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("expected status %d, got %d: %s", http.StatusCreated, w.Code, w.Body.String())
		}

		var tag TagResponse
		err := json.NewDecoder(w.Body).Decode(&tag)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if tag.Name != "NewTag" {
			t.Errorf("expected name 'NewTag', got '%s'", tag.Name)
		}
		if tag.Color != "#FF5733" {
			t.Errorf("expected color '#FF5733', got '%s'", tag.Color)
		}
	})

	t.Run("Missing name", func(t *testing.T) {
		body := CreateTagRequest{
			Color: "#FF5733",
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/tags", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.Create(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Duplicate name", func(t *testing.T) {
		body := CreateTagRequest{
			Name:  "NewTag",
			Color: "#10B981",
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/tags", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.Create(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("expected status %d, got %d", http.StatusConflict, w.Code)
		}
	})
}

func TestTagHandler_Get(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	ctx := context.Background()

	// Create a tag
	tag := &store.Tag{
		ID:    uuid.New().String(),
		Name:  "TestTag",
		Color: "#3B82F6",
	}
	err := db.CreateTag(ctx, tag)
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tags/"+tag.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", tag.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Get(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var resp TagResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Name != "TestTag" {
			t.Errorf("expected name 'TestTag', got '%s'", resp.Name)
		}
	})

	t.Run("Not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tags/nonexistent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Get(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})
}

func TestTagHandler_Update(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	ctx := context.Background()

	// Create a tag
	tag := &store.Tag{
		ID:    uuid.New().String(),
		Name:  "OldName",
		Color: "#3B82F6",
	}
	err := db.CreateTag(ctx, tag)
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		newName := "NewName"
		newColor := "#EF4444"
		body := UpdateTagRequest{
			Name:  &newName,
			Color: &newColor,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPut, "/tags/"+tag.ID, bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", tag.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Update(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
		}

		var resp TagResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Name != "NewName" {
			t.Errorf("expected name 'NewName', got '%s'", resp.Name)
		}
		if resp.Color != "#EF4444" {
			t.Errorf("expected color '#EF4444', got '%s'", resp.Color)
		}
	})

	t.Run("Not found", func(t *testing.T) {
		newName := "Test"
		body := UpdateTagRequest{
			Name: &newName,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPut, "/tags/nonexistent", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Update(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})
}

func TestTagHandler_Delete(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	ctx := context.Background()

	// Create a tag
	tag := &store.Tag{
		ID:    uuid.New().String(),
		Name:  "ToDelete",
		Color: "#3B82F6",
	}
	err := db.CreateTag(ctx, tag)
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	t.Run("Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/tags/"+tag.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", tag.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Delete(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status %d, got %d", http.StatusNoContent, w.Code)
		}

		// Verify deleted
		_, err := db.GetTag(ctx, tag.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/tags/nonexistent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.Delete(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})
}

func TestTagHandler_ItemTags(t *testing.T) {
	db := setupTagTestDB(t)
	defer db.Close()
	handler := setupTagHandler(db)

	ctx := context.Background()

	// Create a knowledge item
	item := &store.KnowledgeItem{
		ContentID:      uuid.New().String(),
		SourceType:     "test",
		Title:          "Test Item",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err := db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	// Create a tag
	tag := &store.Tag{
		ID:    uuid.New().String(),
		Name:  "ItemTag",
		Color: "#3B82F6",
	}
	err = db.CreateTag(ctx, tag)
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}

	t.Run("GetItemTags_Empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/knowledge/"+item.ContentID+"/tags", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", item.ContentID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.GetItemTags(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var tags []TagResponse
		err := json.NewDecoder(w.Body).Decode(&tags)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(tags) != 0 {
			t.Errorf("expected 0 tags, got %d", len(tags))
		}
	})

	t.Run("AddTagToItem_ByID", func(t *testing.T) {
		body := AddTagToItemRequest{
			TagID: tag.ID,
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/knowledge/"+item.ContentID+"/tags", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", item.ContentID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.AddTagToItem(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
		}
	})

	t.Run("GetItemTags_WithTag", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/knowledge/"+item.ContentID+"/tags", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", item.ContentID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.GetItemTags(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		var tags []TagResponse
		err := json.NewDecoder(w.Body).Decode(&tags)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if len(tags) != 1 {
			t.Errorf("expected 1 tag, got %d", len(tags))
		}
		if tags[0].Name != "ItemTag" {
			t.Errorf("expected name 'ItemTag', got '%s'", tags[0].Name)
		}
	})

	t.Run("AddTagToItem_ByName", func(t *testing.T) {
		body := AddTagToItemRequest{
			TagName: "NewTagByName",
			Color:   "#10B981",
		}
		bodyBytes, _ := json.Marshal(body)

		req := httptest.NewRequest(http.MethodPost, "/knowledge/"+item.ContentID+"/tags", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", item.ContentID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.AddTagToItem(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
		}

		var resp TagResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Name != "NewTagByName" {
			t.Errorf("expected name 'NewTagByName', got '%s'", resp.Name)
		}
	})

	t.Run("RemoveTagFromItem", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/knowledge/"+item.ContentID+"/tags/"+tag.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", item.ContentID)
		rctx.URLParams.Add("tag_id", tag.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.RemoveTagFromItem(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected status %d, got %d", http.StatusNoContent, w.Code)
		}
	})

	t.Run("GetItemTags_ItemNotFound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/knowledge/nonexistent/tags", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("content_id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()

		handler.GetItemTags(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
		}
	})
}
