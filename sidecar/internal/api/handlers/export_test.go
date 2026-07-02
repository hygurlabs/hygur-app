package handlers

import (
	"archive/zip"
	"bytes"
	"context"
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
)

// TestExportHandler_RoundTrip builds an export, decrypts it via the versioned
// decryptExport (v1 AES-256-GCM), and checks the archive holds the notes + briefs
// (as Markdown + data.json) and EXCLUDES mail-derived items.
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

	out := rec.Body.Bytes()
	// The envelope must be the self-describing v1 GCM container.
	if len(out) < 5 || string(out[:4]) != exportMagic || out[4] != exportVersionV1 {
		t.Fatalf("export is not the v1 GCM container: % x", out[:min(5, len(out))])
	}
	plain, err := decryptExport(out, pass)
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

// TestExport_GCMRoundTripAndTamper proves the v1 envelope round-trips and that GCM
// authentication rejects a single flipped ciphertext byte (no MAC-less CBC any more).
func TestExport_GCMRoundTripAndTamper(t *testing.T) {
	const pass = "correct horse battery"
	plain := []byte("sensitive export bytes — fictional data only\n")
	blob, err := encryptExport(plain, pass)
	if err != nil {
		t.Fatalf("encryptExport: %v", err)
	}
	got, err := decryptExport(blob, pass)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round-trip: got %q err %v", got, err)
	}
	// Wrong passphrase → GCM tag fails.
	if _, err := decryptExport(blob, "wrong passphrase"); err == nil {
		t.Fatal("wrong passphrase should fail the GCM tag")
	}
	// Flip a byte in the ciphertext region (past the 5+16+12 header) → tamper caught.
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := decryptExport(tampered, pass); err == nil {
		t.Fatal("a flipped ciphertext byte must fail the GCM tag")
	}
	// Flip the version byte (AAD) → downgrade/tamper caught.
	verFlip := append([]byte(nil), blob...)
	verFlip[4] ^= 0x01
	if _, err := decryptExport(verFlip, pass); err == nil {
		t.Fatal("a flipped version byte must be rejected")
	}
}

// TestExport_DecryptDispatchesLegacyCBC proves the marker dispatch keeps the legacy
// OpenSSL AES-256-CBC ("Salted__") format decryptable (already-produced exports).
func TestExport_DecryptDispatchesLegacyCBC(t *testing.T) {
	const pass = "a strong passphrase"
	plain := []byte("older CBC-format export\n")
	legacy, err := encryptOpenSSL(plain, pass)
	if err != nil {
		t.Fatalf("encryptOpenSSL: %v", err)
	}
	got, err := decryptExport(legacy, pass) // dispatch → CBC branch
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("legacy dispatch: got %q err %v", got, err)
	}
}
