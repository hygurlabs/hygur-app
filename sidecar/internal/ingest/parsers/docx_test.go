package parsers

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// createTestDOCX creates a minimal valid DOCX file in memory.
// DOCX is a ZIP archive with specific XML files.
func createTestDOCX(content string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// [Content_Types].xml - required for DOCX
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	f, err := w.Create("[Content_Types].xml")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(contentTypes)); err != nil {
		return nil, err
	}

	// _rels/.rels - required relationships
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	f, err = w.Create("_rels/.rels")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(rels)); err != nil {
		return nil, err
	}

	// word/document.xml - the actual document content
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:r>
        <w:t>` + content + `</w:t>
      </w:r>
    </w:p>
  </w:body>
</w:document>`

	f, err = w.Create("word/document.xml")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(document)); err != nil {
		return nil, err
	}

	// word/_rels/document.xml.rels - document relationships (empty but required)
	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`

	f, err = w.Create("word/_rels/document.xml.rels")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(docRels)); err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// createMultiParagraphDOCX creates a DOCX with multiple paragraphs.
func createMultiParagraphDOCX(paragraphs []string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// [Content_Types].xml
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	f, err := w.Create("[Content_Types].xml")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(contentTypes)); err != nil {
		return nil, err
	}

	// _rels/.rels
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	f, err = w.Create("_rels/.rels")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(rels)); err != nil {
		return nil, err
	}

	// Build paragraphs XML
	var paraXML strings.Builder
	for _, p := range paragraphs {
		paraXML.WriteString(`<w:p><w:r><w:t>`)
		paraXML.WriteString(p)
		paraXML.WriteString(`</w:t></w:r></w:p>`)
	}

	// word/document.xml
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>` + paraXML.String() + `</w:body>
</w:document>`

	f, err = w.Create("word/document.xml")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(document)); err != nil {
		return nil, err
	}

	// word/_rels/document.xml.rels
	docRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`

	f, err = w.Create("word/_rels/document.xml.rels")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(docRels)); err != nil {
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func TestNewDOCXParser(t *testing.T) {
	p := NewDOCXParser()
	if p == nil {
		t.Fatal("NewDOCXParser returned nil")
	}
}

func TestDOCXParser_SupportedExtensions(t *testing.T) {
	p := NewDOCXParser()
	exts := p.SupportedExtensions()

	if len(exts) != 1 {
		t.Errorf("expected 1 extension, got %d", len(exts))
	}

	if exts[0] != ".docx" {
		t.Errorf("expected .docx, got %s", exts[0])
	}
}

func TestDOCXParser_Parse_Simple(t *testing.T) {
	p := NewDOCXParser()

	docxData, err := createTestDOCX("Hello World")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	content, metadata, err := p.Parse(ctx, bytes.NewReader(docxData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Check content (raw text: case preserved)
	if content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", content)
	}

	// Check metadata
	if metadata["format"] != "docx" {
		t.Errorf("expected format=docx, got %v", metadata["format"])
	}
}

func TestDOCXParser_Parse_MultipleParagraphs(t *testing.T) {
	p := NewDOCXParser()

	paragraphs := []string{"First paragraph.", "Second paragraph.", "Third paragraph."}
	docxData, err := createMultiParagraphDOCX(paragraphs)
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	content, _, err := p.Parse(ctx, bytes.NewReader(docxData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Content should contain all paragraphs separated by spaces (raw text: case preserved)
	for _, p := range paragraphs {
		if !strings.Contains(content, p) {
			t.Errorf("content should contain %q, got %q", p, content)
		}
	}
}

func TestDOCXParser_Parse_InvalidFile(t *testing.T) {
	p := NewDOCXParser()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "random bytes",
			data: []byte("not a valid docx file"),
		},
		{
			name: "empty zip",
			data: createEmptyZip(t),
		},
		{
			name: "partial zip header",
			data: []byte{0x50, 0x4B, 0x03, 0x04},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, _, err := p.Parse(ctx, bytes.NewReader(tt.data))
			if err == nil {
				t.Error("expected error for invalid file, got nil")
			}
		})
	}
}

func TestDOCXParser_Parse_EmptyContent(t *testing.T) {
	p := NewDOCXParser()

	// Create a DOCX with empty text
	docxData, err := createTestDOCX("")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	_, _, err = p.Parse(ctx, bytes.NewReader(docxData))
	if err == nil {
		t.Error("expected error for empty content, got nil")
	}
	if err != ErrEmptyDOCX {
		t.Errorf("expected ErrEmptyDOCX, got %v", err)
	}
}

func TestDOCXParser_Parse_EmptyReader(t *testing.T) {
	p := NewDOCXParser()

	ctx := context.Background()
	_, _, err := p.Parse(ctx, bytes.NewReader([]byte{}))
	if err == nil {
		t.Error("expected error for empty reader, got nil")
	}
	if err != ErrEmptyDOCX {
		t.Errorf("expected ErrEmptyDOCX, got %v", err)
	}
}

func TestDOCXParser_Parse_ContextCanceled(t *testing.T) {
	p := NewDOCXParser()

	docxData, err := createTestDOCX("Hello World")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, _, err = p.Parse(ctx, bytes.NewReader(docxData))
	if err == nil {
		t.Error("expected error for canceled context, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDOCXParser_Parse_ContextTimeout(t *testing.T) {
	p := NewDOCXParser()

	docxData, err := createTestDOCX("Hello World")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	_, _, err = p.Parse(ctx, bytes.NewReader(docxData))
	if err == nil {
		t.Error("expected error for timed out context, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestDOCXParser_Parse_SpecialCharacters(t *testing.T) {
	p := NewDOCXParser()

	// Note: XML special chars would need escaping in real DOCX
	docxData, err := createTestDOCX("Text with special chars: accents eaiou")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	content, _, err := p.Parse(ctx, bytes.NewReader(docxData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !strings.Contains(content, "special chars") {
		t.Errorf("content should contain special chars, got %q", content)
	}
}

func TestDOCXParser_Parse_WhitespacePreserved(t *testing.T) {
	p := NewDOCXParser()

	docxData, err := createTestDOCX("Multiple   spaces   here")
	if err != nil {
		t.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	content, _, err := p.Parse(ctx, bytes.NewReader(docxData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// The parser returns raw text: multiple spaces are preserved
	// (normalization happens later in the ingest layer, not here).
	if !strings.Contains(content, "Multiple   spaces   here") {
		t.Errorf("content should preserve multiple spaces, got %q", content)
	}
}

func TestExtractTextFromXML(t *testing.T) {
	tests := []struct {
		name    string
		xml     string
		want    string
		wantErr bool
	}{
		{
			name: "simple text",
			xml:  `<w:document><w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p></w:body></w:document>`,
			want: "Hello",
		},
		{
			name: "multiple text elements",
			xml:  `<w:document><w:body><w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:t> World</w:t></w:r></w:p></w:body></w:document>`,
			want: "Hello World",
		},
		{
			name: "multiple paragraphs",
			xml:  `<w:document><w:body><w:p><w:r><w:t>First</w:t></w:r></w:p><w:p><w:r><w:t>Second</w:t></w:r></w:p></w:body></w:document>`,
			want: "First Second",
		},
		{
			name: "with line break",
			xml:  `<w:document><w:body><w:p><w:r><w:t>Before</w:t><w:br/><w:t>After</w:t></w:r></w:p></w:body></w:document>`,
			want: "Before After",
		},
		{
			name: "empty document",
			xml:  `<w:document><w:body></w:body></w:document>`,
			want: "",
		},
		{
			name:    "invalid xml",
			xml:     `<w:document><w:body>`,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractTextFromXML(tt.xml)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractTextFromXML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Trim spaces for comparison
			got = strings.TrimSpace(got)
			if got != tt.want {
				t.Errorf("extractTextFromXML() = %q, want %q", got, tt.want)
			}
		})
	}
}

// createEmptyZip creates a valid but empty ZIP file.
func createEmptyZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("failed to create empty zip: %v", err)
	}
	return buf.Bytes()
}

// BenchmarkDOCXParser_Parse benchmarks the DOCX parsing performance.
func BenchmarkDOCXParser_Parse(b *testing.B) {
	p := NewDOCXParser()

	// Create a DOCX with some content
	content := strings.Repeat("This is a test paragraph with some text. ", 100)
	docxData, err := createTestDOCX(content)
	if err != nil {
		b.Fatalf("failed to create test DOCX: %v", err)
	}

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _, err := p.Parse(ctx, bytes.NewReader(docxData))
		if err != nil {
			b.Fatalf("Parse failed: %v", err)
		}
	}
}
