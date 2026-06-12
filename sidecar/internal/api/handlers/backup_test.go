package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// TestBackupHandler_SaveLocal verifies the local-save path writes a real SQLite
// snapshot to disk and returns its path. HOME is redirected to a temp dir so the
// ~/Downloads probe falls back to <dataDir>/backups instead of the real home.
func TestBackupHandler_SaveLocal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hygur.db")
	db, err := store.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	h := NewBackupHandler(db, dbPath, "", zerolog.Nop())
	router := chi.NewRouter()
	router.Post("/admin/db/backup/save", h.SaveLocal)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/db/backup/save", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "saved" || resp.Path == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	b, err := os.ReadFile(resp.Path)
	if err != nil {
		t.Fatalf("read saved backup: %v", err)
	}
	if len(b) < 100 || string(b[:16]) != "SQLite format 3\x00" {
		t.Fatalf("saved file is not a plaintext SQLite DB (%d bytes)", len(b))
	}
}

// TestBackupHandler_DownloadThenRestore exercises the wiring end-to-end through
// a real chi router: GET streams a valid snapshot, POST re-uploads it and stages
// a restore, and a garbage upload is rejected before staging.
func TestBackupHandler_DownloadThenRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hygur.db")
	db, err := store.NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &store.KnowledgeItem{
		ContentID: "x", SourceType: "note", Title: "t", NormalizedText: "c",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	h := NewBackupHandler(db, dbPath, "", zerolog.Nop())
	router := chi.NewRouter()
	router.Get("/admin/db/backup", h.Download)
	router.Post("/admin/db/restore", h.Restore)

	// Download → a valid plaintext SQLite snapshot.
	dlRec := httptest.NewRecorder()
	router.ServeHTTP(dlRec, httptest.NewRequest(http.MethodGet, "/admin/db/backup", nil))
	if dlRec.Code != http.StatusOK {
		t.Fatalf("download status = %d", dlRec.Code)
	}
	snapshot := dlRec.Body.Bytes()
	if len(snapshot) < 100 || string(snapshot[:16]) != "SQLite format 3\x00" {
		t.Fatalf("download did not return a plaintext SQLite DB (%d bytes)", len(snapshot))
	}

	// Restore the snapshot → staged + restart_required.
	rsRec := httptest.NewRecorder()
	router.ServeHTTP(rsRec, multipartUpload(t, "/admin/db/restore", snapshot))
	if rsRec.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body=%s", rsRec.Code, rsRec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rsRec.Body.Bytes(), &resp)
	if resp["restart_required"] != true {
		t.Errorf("expected restart_required=true, got %v", resp)
	}
	if _, err := os.Stat(dbPath + ".restore-pending"); err != nil {
		t.Errorf("expected staged restore file: %v", err)
	}

	// Garbage upload → rejected, nothing staged.
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, multipartUpload(t, "/admin/db/restore", []byte("totally not a database")))
	if badRec.Code != http.StatusBadRequest {
		t.Errorf("garbage upload should be 400, got %d", badRec.Code)
	}
	if _, err := os.Stat(dbPath + ".restore-pending"); err == nil {
		t.Errorf("garbage upload must not leave a staged file")
	}
}

func multipartUpload(t *testing.T, path string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "backup.db")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}
