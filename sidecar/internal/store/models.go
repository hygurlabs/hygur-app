// Package store provides SQLite database access for the Hygur knowledge base.
package store

import (
	"time"
)

// KnowledgeItem represents a piece of knowledge stored in the database.
// SourceType is a free-form string set by the ingestor based on file
// extension. Known values: "markdown", "pdf", "docx", "txt", "html",
// "note", "email", "thread", "mail", "image", "audio", "file", "unknown".
type KnowledgeItem struct {
	ContentID  string
	SourceType string
	SourcePath *string
	Title      string
	// NormalizedText is the collapsed + lowercased index text. It feeds FTS,
	// embeddings and the dedup hash; it must never be shown to a human or fed to
	// an LLM as the item's content (line breaks and case are lost).
	NormalizedText string
	// RawText is the ORIGINAL text with line breaks and case preserved — what a
	// note/file body actually looked like. It is what the Library and the LLM
	// should read as the item's content. Empty for items ingested before the
	// raw_text column existed (fall back to NormalizedText via DisplayText).
	RawText   string
	Metadata  map[string]any
	VersionID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DisplayText returns the text to show to a human or feed to an LLM as the
// item's content: the case- and line-break-preserving RawText when present,
// falling back to NormalizedText for items ingested before raw_text existed (or
// for sources — e.g. mail — that keep their formatting in NormalizedText).
func (k *KnowledgeItem) DisplayText() string {
	if k.RawText != "" {
		return k.RawText
	}
	return k.NormalizedText
}

// Chunk represents a chunk of a knowledge item for embedding purposes.
type Chunk struct {
	ChunkID   string
	ContentID string
	// SectionID links the chunk to its parent logical block (see Section).
	// nil for chunks produced before hierarchical chunking (schema < v9).
	SectionID      *string
	ChunkHash      string
	EmbeddingModel *string
	Text           string
	Metadata       map[string]any
	CreatedAt      time.Time
}

// Section is a complete logical block of a document — a heading and its body
// down to the next same-or-higher heading. Sections are the "big" unit of the
// small-to-big retrieval strategy: chunks give precise recall, but the full
// section is what gets handed to the LLM so it reasons over a coherent block
// instead of an arbitrary fixed-size slice.
type Section struct {
	SectionID       string
	ContentID       string
	ParentSectionID *string  // nil for top-level sections
	Heading         string   // this section's heading text ("" for preamble/root)
	HeadingPath     []string // ancestor headings incl. self, root-first
	Level           int      // heading depth: 1=H1, 2=H2…; 0 = preamble/root
	Ordinal         int      // order within the document
	FullText        string   // the complete logical block
	TokenCount      int
	Metadata        map[string]any
	CreatedAt       time.Time
}

// Project represents a project that groups knowledge items.
type Project struct {
	ProjectID   string
	Name        string
	Description *string
	Tags        []string
	Archived    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectLink represents a many-to-many relationship between projects and knowledge items.
type ProjectLink struct {
	LinkID     string
	ProjectID  string
	ContentID  string
	LocalTitle *string
	LocalNotes *string
	PinState   bool
	LocalTags  []string
	CreatedAt  time.Time
}

// Summary represents an AI-generated summary of a knowledge item or thread.
type Summary struct {
	SummaryID     string
	SourceRef     string
	ModelUsed     string
	Decisions     []string
	Actions       []string
	OpenQuestions []string
	CreatedAt     time.Time
}

// MemoryType represents the type of memory.
type MemoryType string

const (
	MemoryFact       MemoryType = "fact"
	MemoryAction     MemoryType = "action"
	MemoryPreference MemoryType = "preference"
)

// MemorySource discriminates between user-pinned and LLM-distilled memories.
// Phase 3.3 introduces 'extracted' for the long-term memory pipeline; 'manual'
// is the default and matches every memory inserted before the feature shipped.
type MemorySource string

const (
	// MemorySourceManual is for memories the user explicitly added (or that
	// the older auto-extractor stored before Phase 3.3 introduced acceptance).
	// Manual memories are auto-accepted on insert.
	MemorySourceManual MemorySource = "manual"
	// MemorySourceExtracted is for memories distilled by the LLM from a chat
	// session. They land with AcceptedAt=nil (pending) and require explicit
	// user acceptance before being eligible for prompt injection.
	MemorySourceExtracted MemorySource = "extracted"
)

// Memory represents a persistent memory entry.
type Memory struct {
	MemoryID  string
	Type      MemoryType
	Content   string
	ContextID string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Score     float64
	// Phase 3.3 — long-term chat memory.
	Source     MemorySource
	AcceptedAt *time.Time // nil = pending review (only valid when Source = extracted)
	Embedding  []float32  // nil when not embedded yet (e.g. legacy rows)
	SessionID  string     // session that produced this memory; "" when unknown
}
