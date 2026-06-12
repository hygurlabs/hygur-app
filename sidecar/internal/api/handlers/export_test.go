package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/pbkdf2"
)

// TestExportHandler_RoundTrip builds an export, decrypts it the same way
// `openssl enc -d -aes-256-cbc -pbkdf2` would, and checks the archive holds the
// notes + briefs (as Markdown + data.json) and EXCLUDES mail-derived items.
func TestExportHandler_RoundTrip(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	ins := func(id, st, title, text string) {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: id, SourceType: st, Title: title, NormalizedText: text, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	ins("note:1", "note", "My Note", "hello world")
	ins("brief:2026-06-09", "brief", "Daily", "today's recap")
	ins("mail:x:1", "mail", "secret mail", "should not appear")

	h := NewExportHandler(db, zerolog.Nop())
	router := chi.NewRouter()
	router.Post("/admin/export", h.Export)

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/export", strings.NewReader(body)))
		return rec
	}

	if rec := post(`{"passphrase":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short passphrase: status %d, want 400", rec.Code)
	}

	const pass = "correct horse battery"
	rec := post(`{"passphrase":"` + pass + `"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}

	plain, err := decryptOpenSSL(rec.Body.Bytes(), pass)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(b)
	}

	dj, ok := files["data.json"]
	if !ok {
		t.Fatal("data.json missing")
	}
	if strings.Contains(dj, "should not appear") || strings.Contains(dj, "mail:x:1") {
		t.Error("mail item leaked into the export")
	}
	if !strings.Contains(dj, "note:1") || !strings.Contains(dj, "brief:2026-06-09") {
		t.Error("note/brief missing from data.json")
	}
	if _, ok := files["manifest.json"]; !ok {
		t.Error("manifest.json missing")
	}
	var hasNoteMd, hasBriefMd bool
	for n := range files {
		hasNoteMd = hasNoteMd || (strings.HasPrefix(n, "notes/") && strings.HasSuffix(n, ".md"))
		hasBriefMd = hasBriefMd || (strings.HasPrefix(n, "briefs/") && strings.HasSuffix(n, ".md"))
	}
	if !hasNoteMd || !hasBriefMd {
		t.Errorf("missing markdown renders: note=%v brief=%v", hasNoteMd, hasBriefMd)
	}
}

// TestEncryptOpenSSL_OpenSSLCompat proves the client's documented decrypt path
// actually works: encrypt in Go, decrypt with the real `openssl enc -d` CLI.
// Skips where openssl isn't installed (keeps CI portable).
func TestEncryptOpenSSL_OpenSSLCompat(t *testing.T) {
	bin, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not on PATH")
	}
	const pass = "a strong passphrase"
	plain := []byte("hygur export compatibility check\n")
	blob, err := encryptOpenSSL(plain, pass)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	in := filepath.Join(t.TempDir(), "blob.enc")
	if err := os.WriteFile(in, blob, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := exec.Command(bin, "enc", "-d", "-aes-256-cbc", "-pbkdf2",
		"-pass", "pass:"+pass, "-in", in).Output()
	if err != nil {
		t.Fatalf("openssl decrypt failed: %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("openssl output mismatch: got %q want %q", out, plain)
	}
}

func decryptOpenSSL(blob []byte, passphrase string) ([]byte, error) {
	if len(blob) < 16 || string(blob[:8]) != "Salted__" {
		return nil, errors.New("bad header")
	}
	salt, ct := blob[8:16], blob[16:]
	dk := pbkdf2.Key([]byte(passphrase), salt, 10000, 48, sha256.New)
	block, err := aes.NewCipher(dk[:32])
	if err != nil {
		return nil, err
	}
	if len(ct) == 0 || len(ct)%block.BlockSize() != 0 {
		return nil, errors.New("bad ciphertext length")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, dk[32:48]).CryptBlocks(pt, ct)
	pad := int(pt[len(pt)-1])
	if pad <= 0 || pad > block.BlockSize() || pad > len(pt) {
		return nil, errors.New("bad padding")
	}
	return pt[:len(pt)-pad], nil
}
