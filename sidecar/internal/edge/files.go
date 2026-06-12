package edge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/ingest"
	"github.com/hygur/sidecar/internal/ingest/parsers"
)

// TextParsers returns the device-extractable text parsers (txt, markdown, docx,
// pdf). These run locally so only text leaves the device. PDF is TEXT-LAYER ONLY
// (NewPDFParserTextOnly: in-process, panic-recovered, no poppler/vision) — a
// SCANNED/image-only PDF yields no text and is skipped here; it would go to the
// central multimodal pipeline (per EDGE_AGENT_DESIGN §2, the dual path for
// scans/images/audio — not yet routed from the edge).
func TextParsers() map[string]ingest.Parser {
	out := map[string]ingest.Parser{}
	register := func(p ingest.Parser) {
		for _, ext := range p.SupportedExtensions() {
			out[strings.ToLower(ext)] = p
		}
	}
	register(parsers.NewTXTParser())
	register(parsers.NewMarkdownParser())
	register(parsers.NewDOCXParser())
	register(parsers.NewPDFParserTextOnly())
	return out
}

// noiseDirs are dependency/build/VCS directories never worth indexing — they
// hold thousands of boilerplate README/LICENSE/CHANGELOG files that would drown
// the KB in noise. Skipped wholesale (plus any dot-directory).
var noiseDirs = map[string]bool{
	"node_modules": true, "bower_components": true, "vendor": true,
	"dist": true, "build": true, "out": true, "target": true,
	"__pycache__": true, "venv": true, "Pods": true, "DerivedData": true,
	".git": true, ".svn": true, ".hg": true, // (also covered by the dot-dir rule)
}

// skipDir reports whether a directory should be pruned from the walk. The scan
// root is never pruned (the caller passes path != root).
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || noiseDirs[name]
}

// FileSync walks a folder, extracts text locally from supported files, and pushes
// each (idempotent source_ref) to the central server. Files unchanged since the
// watermark are skipped. Dependency/build/VCS + hidden directories are pruned so
// the KB isn't flooded with boilerplate README/LICENSE files.
type FileSync struct {
	parsers map[string]ingest.Parser
	client  *Client
}

// NewFileSync wires a FileSync to a push client + a parser set.
func NewFileSync(client *Client, p map[string]ingest.Parser) *FileSync {
	return &FileSync{parsers: p, client: client}
}

// SyncStats reports a run's outcome. Newest is the latest mtime pushed (the new
// watermark to persist).
type SyncStats struct {
	Pushed  int
	Skipped int
	Errors  int
	Newest  time.Time
}

// Run walks folder; for each supported file modified after `since`, extracts text
// and pushes it. Returns stats incl. the new watermark (Newest). Walk errors on
// individual files are counted, not fatal.
func (fs *FileSync) Run(ctx context.Context, folder string, since time.Time) (SyncStats, error) {
	st := SyncStats{Newest: since}
	walkErr := filepath.WalkDir(folder, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if path != folder && skipDir(d.Name()) {
				return filepath.SkipDir // prune deps/build/VCS/hidden trees
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil // hidden file
		}
		p := fs.parsers[strings.ToLower(filepath.Ext(path))]
		if p == nil {
			return nil // unsupported extension
		}
		info, err := d.Info()
		if err != nil {
			st.Errors++
			return nil
		}
		mt := info.ModTime()
		if !mt.After(since) {
			st.Skipped++
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err // cancelled
		}

		f, err := os.Open(path)
		if err != nil {
			st.Errors++
			return nil
		}
		text, _, perr := p.Parse(ctx, f)
		f.Close()
		if perr != nil || strings.TrimSpace(text) == "" {
			st.Errors++
			return nil
		}
		if _, err := fs.client.PushText(ctx, IngestText{
			Title:      filepath.Base(path),
			Text:       text,
			SourceType: "file",
			SourceRef:  "files:" + path,
			Metadata:   map[string]any{"path": path},
		}); err != nil {
			st.Errors++
			return nil
		}
		st.Pushed++
		if mt.After(st.Newest) {
			st.Newest = mt
		}
		return nil
	})
	return st, walkErr
}
