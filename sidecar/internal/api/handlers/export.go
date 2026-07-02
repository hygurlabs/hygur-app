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
// Envelope = OpenSSL-compatible AES-256-CBC + PBKDF2 (SHA-256, 10k iters), so the
// client decrypts with a universal one-liner — no Hygur tool required:
//
//	openssl enc -d -aes-256-cbc -pbkdf2 -pass pass:YOUR_PASSPHRASE \
//	  -in hygur-export-YYYY-MM-DD.zip.enc -out hygur-export.zip
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
	enc, err := encryptOpenSSL(archive, req.Passphrase)
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

// encryptOpenSSL wraps plaintext in the OpenSSL `enc` envelope: "Salted__" + 8-byte
// salt + AES-256-CBC ciphertext (PKCS#7), with key+IV derived by PBKDF2-SHA256
// (10000 iters) — byte-compatible with `openssl enc -aes-256-cbc -pbkdf2`.
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
