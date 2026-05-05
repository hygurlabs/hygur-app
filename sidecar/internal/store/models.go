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
	ContentID      string
	SourceType     string
	SourcePath     *string
	Title          string
	NormalizedText string
	Metadata       map[string]any
	VersionID      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Chunk represents a chunk of a knowledge item for embedding purposes.
type Chunk struct {
	ChunkID        string
	ContentID      string
	ChunkHash      string
	EmbeddingModel *string
	Text           string
	Metadata       map[string]any
	CreatedAt      time.Time
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

// Memory represents a persistent memory entry.
type Memory struct {
	MemoryID  string
	Type      MemoryType
	Content   string
	ContextID string
	CreatedAt time.Time
	ExpiresAt *time.Time
	Score     float64
}
