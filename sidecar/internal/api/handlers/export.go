package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/pbkdf2"
)

// exportSourceTypes are the Hygur-produced artifacts a data export (GDPR Art. 20)
// covers: notes and briefs. Original mail/files are deliberately NOT here — they
// live on the user's device / in their mailbox; Hygur Cloud only stores the text
// it derives, so re-exporting it would be redundant and bloat the archive.
var exportSourceTypes = []string{store.SourceTypeNote, "brief", "meeting_brief"}

// ExportHandler streams an encrypted archive of the user's own Hygur-produced data.
type ExportHandler struct {
	db     *store.DB
	logger zerolog.Logger
}

// NewExportHandler builds an ExportHandler over the tenant store.
func NewExportHandler(db *store.DB, logger zerolog.Logger) *ExportHandler {
	return &ExportHandler{db: db, logger: logger.With().Str("handler", "export").Logger()}
}

type exportRequest struct {
	Passphrase string `json:"passphrase"`
}

// Export (POST /admin/export) builds a zip of the user's notes + briefs (Markdown
// for reading + data.json for portability) and encrypts it with the user's OWN
// passphrase. The tenant DEK never leaves the server; the passphrase is never
// stored; the archive is streamed, never written to disk server-side.
//
// Envelope = authenticated AES-256-GCM keyed by PBKDF2 (SHA-256, 600k iters), in a
// self-describing versioned container (see encryptExport): a 5-byte magic+version
// header, then the random salt + nonce, then the GCM ciphertext+tag. GCM detects
// any tampering; the raised KDF slows an offline passphrase guess. Decrypt with the
// documented format (magic "HYGR" + version 0x01 | salt[16] | nonce[12] | ct||tag),
// mirrored by decryptExport for any future in-app import.
func (h *ExportHandler) Export(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Passphrase) < 8 {
		http.Error(w, "passphrase must be at least 8 characters", http.StatusBadRequest)
		return
	}

	archive, err := h.buildZip(r.Context())
	if err != nil {
		h.logger.Error().Err(err).Msg("build export archive")
		http.Error(w, "could not build the export", http.StatusInternalServerError)
		return
	}
	enc, err := encryptExport(archive, req.Passphrase)
	if err != nil {
		h.logger.Error().Err(err).Msg("encrypt export archive")
		http.Error(w, "could not encrypt the export", http.StatusInternalServerError)
		return
	}

	name := "hygur-export-" + time.Now().UTC().Format("2006-01-02") + ".zip.enc"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(enc)
}

// exportItem is the machine-readable shape written to data.json (GDPR Art. 20).
type exportItem struct {
	ContentID  string         `json:"content_id"`
	SourceType string         `json:"source_type"`
	Title      string         `json:"title"`
	Text       string         `json:"text"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

// buildZip assembles the (plaintext) archive: a Markdown file per note/brief for
// reading, data.json for portability, and a manifest. Paginates each source type.
func (h *ExportHandler) buildZip(ctx context.Context) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	all := make([]exportItem, 0)
	counts := map[string]int{}
	const page = 500
	for _, st := range exportSourceTypes {
		for offset := 0; ; offset += page {
			items, err := h.db.ListKnowledgeItemsBySourceType(ctx, st, page, offset)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				ei := exportItem{
					ContentID:  it.ContentID,
					SourceType: it.SourceType,
					Title:      it.Title,
					Text:       it.DisplayText(),
					Metadata:   it.Metadata,
					CreatedAt:  it.CreatedAt.UTC().Format(time.RFC3339),
					UpdatedAt:  it.UpdatedAt.UTC().Format(time.RFC3339),
				}
				all = append(all, ei)
				counts[st]++

				fw, err := zw.Create(mdDir(st) + "/" + safeName(it.Title, it.ContentID) + ".md")
				if err != nil {
					return nil, err
				}
				title := it.Title
				if title == "" {
					title = "(untitled)"
				}
				if _, err := fmt.Fprintf(fw, "# %s\n\n_%s · %s_\n\n%s\n", title, st, ei.CreatedAt, it.DisplayText()); err != nil {
					return nil, err
				}
			}
			if len(items) < page {
				break
			}
		}
	}

	if err := writeJSONEntry(zw, "data.json", all); err != nil {
		return nil, err
	}
	if err := writeJSONEntry(zw, "manifest.json", map[string]any{
		"format":      "hygur-export/1",
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"counts":      counts,
		"total":       len(all),
		"note":        "Original emails and files are not included — they remain on your device / in your mailbox. This archive contains the notes and briefs Hygur produced.",
	}); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeJSONEntry(zw *zip.Writer, name string, v any) error {
	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(fw)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func mdDir(sourceType string) string {
	if sourceType == store.SourceTypeNote {
		return "notes"
	}
	return "briefs"
}

var nonSafe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// safeName builds a filesystem-safe archive entry name from a title + a short id
// suffix (keeping entries unique even when titles collide or are empty).
func safeName(title, id string) string {
	s := strings.Trim(nonSafe.ReplaceAllString(strings.TrimSpace(title), "-"), "-")
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		s = "untitled"
	}
	short := id
	if i := strings.LastIndexAny(id, ":/"); i >= 0 && i+1 < len(id) {
		short = id[i+1:]
	}
	short = nonSafe.ReplaceAllString(short, "")
	if len(short) > 12 {
		short = short[:12]
	}
	if short == "" {
		return s
	}
	return s + "-" + short
}

// Versioned export envelope. The container is self-describing so the format can
// evolve without ambiguity: a fixed magic + a single version byte prefix a v1
// AES-256-GCM payload. Version 0 is the legacy OpenSSL AES-256-CBC ("Salted__")
// format still produced by encryptOpenSSL (kept for reference/fixtures).
const (
	exportMagic     = "HYGR" // 4-byte marker; distinguishes from the legacy "Salted__"
	exportVersionV1 = 0x01   // AES-256-GCM + PBKDF2-SHA256 (600k)
	exportKDFIters  = 600_000
	exportSaltLen   = 16
)

// encryptExport wraps plaintext in the v1 authenticated envelope:
//
//	"HYGR" | 0x01 | salt[16] | nonce[12] | AES-256-GCM(ciphertext||tag)
//
// The 32-byte key is PBKDF2-SHA256(passphrase, salt, 600k). The 5-byte magic+version
// header is fed to GCM as additional authenticated data, so a version/downgrade
// flip is detected alongside any ciphertext tamper. Salt and nonce are fresh random
// per export and stored in the header.
func encryptExport(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, exportSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key := pbkdf2.Key([]byte(passphrase), salt, exportKDFIters, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	header := append([]byte(exportMagic), exportVersionV1)
	ct := gcm.Seal(nil, nonce, plaintext, header)

	out := make([]byte, 0, len(header)+len(salt)+len(nonce)+len(ct))
	out = append(out, header...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// decryptExport reverses encryptExport, dispatching on the marker: the v1 GCM
// container ("HYGR"|0x01) or the legacy OpenSSL AES-256-CBC ("Salted__") format.
// No handler reads exports back today (export is one-way, for GDPR portability), so
// this exists for round-trip/tamper tests and any future in-app import path — the
// dual dispatch means already-produced legacy files stay decryptable.
func decryptExport(blob []byte, passphrase string) ([]byte, error) {
	if len(blob) >= 5 && string(blob[:4]) == exportMagic {
		if blob[4] != exportVersionV1 {
			return nil, errors.New("export: unsupported version")
		}
		rest := blob[5:]
		block, err := aes.NewCipher(pbkdf2.Key([]byte(passphrase), safeSlice(rest, 0, exportSaltLen), exportKDFIters, 32, sha256.New))
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		ns := gcm.NonceSize()
		if len(rest) < exportSaltLen+ns {
			return nil, errors.New("export: truncated header")
		}
		nonce := rest[exportSaltLen : exportSaltLen+ns]
		ct := rest[exportSaltLen+ns:]
		return gcm.Open(nil, nonce, ct, blob[:5]) // AAD = magic+version
	}
	return decryptOpenSSLCBC(blob, passphrase)
}

// safeSlice returns b[lo:hi] clamped to len(b) (callers validate lengths after).
func safeSlice(b []byte, lo, hi int) []byte {
	if hi > len(b) {
		hi = len(b)
	}
	if lo > hi {
		lo = hi
	}
	return b[lo:hi]
}

// decryptOpenSSLCBC decrypts the legacy OpenSSL AES-256-CBC ("Salted__") envelope
// produced by encryptOpenSSL / `openssl enc -aes-256-cbc -pbkdf2` (10k iters).
func decryptOpenSSLCBC(blob []byte, passphrase string) ([]byte, error) {
	if len(blob) < 16 || string(blob[:8]) != "Salted__" {
		return nil, errors.New("export: bad header")
	}
	salt, ct := blob[8:16], blob[16:]
	dk := pbkdf2.Key([]byte(passphrase), salt, 10000, 48, sha256.New)
	block, err := aes.NewCipher(dk[:32])
	if err != nil {
		return nil, err
	}
	if len(ct) == 0 || len(ct)%block.BlockSize() != 0 {
		return nil, errors.New("export: bad ciphertext length")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, dk[32:48]).CryptBlocks(pt, ct)
	pad := int(pt[len(pt)-1])
	if pad <= 0 || pad > block.BlockSize() || pad > len(pt) {
		return nil, errors.New("export: bad padding")
	}
	return pt[:len(pt)-pad], nil
}

// encryptOpenSSL wraps plaintext in the OpenSSL `enc` envelope: "Salted__" + 8-byte
// salt + AES-256-CBC ciphertext (PKCS#7), with key+IV derived by PBKDF2-SHA256
// (10000 iters) — byte-compatible with `openssl enc -aes-256-cbc -pbkdf2`. Legacy
// v0 format; superseded by encryptExport (kept to produce/verify legacy fixtures).
func encryptOpenSSL(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	dk := pbkdf2.Key([]byte(passphrase), salt, 10000, 48, sha256.New)
	block, err := aes.NewCipher(dk[:32])
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	pad := bs - len(plaintext)%bs
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, dk[32:48]).CryptBlocks(ct, padded)

	out := make([]byte, 0, 16+len(ct))
	out = append(out, []byte("Salted__")...)
	out = append(out, salt...)
	out = append(out, ct...)
	return out, nil
}
