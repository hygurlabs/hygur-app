package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hygur/sidecar/internal/store"
)

func TestExtractFolderTags(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected []string
	}{
		{
			name:     "Empty path",
			path:     "",
			expected: nil,
		},
		{
			name:     "Root path",
			path:     "/",
			expected: nil,
		},
		{
			name:     "Single folder",
			path:     "/Projects/file.txt",
			expected: []string{"Projects"},
		},
		{
			name:     "Two folders",
			path:     "/Work/Projects/file.txt",
			expected: []string{"Work", "Projects"},
		},
		{
			name:     "Three folders",
			path:     "/Category/Work/Projects/file.txt",
			expected: []string{"Category", "Work", "Projects"},
		},
		{
			name:     "More than three folders - takes last 3",
			path:     "/Root/Category/Work/Projects/file.txt",
			expected: []string{"Category", "Work", "Projects"},
		},
		{
			name:     "Skip common root folders",
			path:     "/Users/john/Documents/Projects/file.txt",
			expected: []string{"john", "Projects"},
		},
		{
			name:     "Skip hidden folders",
			path:     "/Projects/.hidden/visible/file.txt",
			expected: []string{"Projects", "visible"},
		},
		{
			name:     "Real macOS path",
			path:     "/Users/john/Documents/Work/Client/ProjectA/docs/report.pdf",
			expected: []string{"Client", "ProjectA", "docs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFolderTags(tt.path)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tags, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for i, tag := range result {
				if tag != tt.expected[i] {
					t.Errorf("expected tag[%d] '%s', got '%s'", i, tt.expected[i], tag)
				}
			}
		})
	}
}

func TestExtractDomainTag(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		expected string
	}{
		{
			name:     "Empty email",
			email:    "",
			expected: "",
		},
		{
			name:     "Invalid email - no @",
			email:    "notanemail",
			expected: "",
		},
		{
			name:     "Company domain",
			email:    "john@acme.com",
			expected: "acme.com",
		},
		{
			name:     "Skip Gmail",
			email:    "user@gmail.com",
			expected: "",
		},
		{
			name:     "Skip Outlook",
			email:    "user@outlook.com",
			expected: "",
		},
		{
			name:     "Skip Protonmail",
			email:    "user@protonmail.com",
			expected: "",
		},
		{
			name:     "Skip iCloud",
			email:    "user@icloud.com",
			expected: "",
		},
		{
			name:     "Corporate domain",
			email:    "contact@company.co.uk",
			expected: "company.co.uk",
		},
		{
			name:     "Subdomain preserved",
			email:    "support@mail.company.com",
			expected: "mail.company.com",
		},
		{
			name:     "Case insensitive",
			email:    "User@GMAIL.COM",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDomainTag(tt.email)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractMailboxFolderTag(t *testing.T) {
	tests := []struct {
		name     string
		mailbox  string
		expected string
	}{
		{
			name:     "Empty mailbox",
			mailbox:  "",
			expected: "",
		},
		{
			name:     "INBOX only",
			mailbox:  "INBOX",
			expected: "",
		},
		{
			name:     "INBOX with subfolder",
			mailbox:  "INBOX/Projects",
			expected: "Projects",
		},
		{
			name:     "Skip Sent",
			mailbox:  "Sent",
			expected: "",
		},
		{
			name:     "Skip All Mail",
			mailbox:  "All Mail",
			expected: "",
		},
		{
			name:     "Gmail label",
			mailbox:  "INBOX/Work/Client",
			expected: "Work",
		},
		{
			name:     "Skip Archive with subfolder",
			mailbox:  "Archive/2024",
			expected: "2024",
		},
		{
			name:     "Backslash separator",
			mailbox:  "INBOX\\Projects\\Active",
			expected: "Projects",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMailboxFolderTag(tt.mailbox)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestAutoTaggerTagDocument(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a knowledge item
	item := &store.KnowledgeItem{
		ContentID:      uuid.New().String(),
		SourceType:     "test",
		Title:          "Test Document",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	autoTagger := NewAutoTagger(db)

	t.Run("TagDocument", func(t *testing.T) {
		result, err := autoTagger.TagDocument(ctx, item.ContentID, "/Work/Projects/file.txt")
		if err != nil {
			t.Fatalf("failed to tag document: %v", err)
		}

		if len(result.Tags) == 0 {
			t.Error("expected some tags")
		}

		// Check that tags are applied to the item
		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags: %v", err)
		}

		if len(tags) != 2 { // "Work" and "Projects"
			t.Errorf("expected 2 tags, got %d", len(tags))
		}

		// Verify tag names
		tagNames := make(map[string]bool)
		for _, tag := range tags {
			tagNames[tag.Name] = true
			if !tag.IsAuto {
				t.Error("expected auto tags")
			}
		}
		if !tagNames["Work"] {
			t.Error("expected 'Work' tag")
		}
		if !tagNames["Projects"] {
			t.Error("expected 'Projects' tag")
		}
	})
}

func TestAutoTaggerTagMail(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a knowledge item for email
	item := &store.KnowledgeItem{
		ContentID:      "email:" + uuid.New().String(),
		SourceType:     "email",
		Title:          "Test Email",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	autoTagger := NewAutoTagger(db)

	t.Run("TagMail", func(t *testing.T) {
		result, err := autoTagger.TagMail(ctx, item.ContentID, "contact@acme.com", "INBOX/Projects")
		if err != nil {
			t.Fatalf("failed to tag mail: %v", err)
		}

		if len(result.Tags) == 0 {
			t.Error("expected some tags")
		}

		// Check that tags are applied to the item
		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags: %v", err)
		}

		// Should have domain tag and mailbox tag
		if len(tags) != 2 {
			t.Errorf("expected 2 tags, got %d", len(tags))
		}

		// Verify tag names
		tagNames := make(map[string]bool)
		for _, tag := range tags {
			tagNames[tag.Name] = true
			if !tag.IsAuto {
				t.Error("expected auto tags")
			}
		}
		if !tagNames["mail:acme.com"] {
			t.Error("expected 'mail:acme.com' tag")
		}
		if !tagNames["mail:Projects"] {
			t.Error("expected 'mail:Projects' tag")
		}
	})
}

func TestAutoTaggerApplyManualTags(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a knowledge item
	item := &store.KnowledgeItem{
		ContentID:      uuid.New().String(),
		SourceType:     "test",
		Title:          "Test Document",
		NormalizedText: "Test content",
		VersionID:      "v1",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	err = db.InsertKnowledgeItem(ctx, item)
	if err != nil {
		t.Fatalf("failed to create item: %v", err)
	}

	autoTagger := NewAutoTagger(db)

	t.Run("ApplyManualTags", func(t *testing.T) {
		err := autoTagger.ApplyManualTags(ctx, item.ContentID, []string{"Important", "Review", ""})
		if err != nil {
			t.Fatalf("failed to apply manual tags: %v", err)
		}

		// Check that tags are applied to the item
		tags, err := db.GetTagsForItem(ctx, item.ContentID)
		if err != nil {
			t.Fatalf("failed to get tags: %v", err)
		}

		// Should have 2 tags (empty string ignored)
		if len(tags) != 2 {
			t.Errorf("expected 2 tags, got %d", len(tags))
		}

		// Verify tags are not auto
		for _, tag := range tags {
			if tag.IsAuto {
				t.Error("expected manual tags, not auto")
			}
		}
	})
}

func TestAutoTaggerWithNilStore(t *testing.T) {
	autoTagger := NewAutoTagger(nil)

	ctx := context.Background()

	t.Run("TagDocument with nil store", func(t *testing.T) {
		result, err := autoTagger.TagDocument(ctx, "test-id", "/path/to/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Tags) != 0 {
			t.Error("expected empty result with nil store")
		}
	})

	t.Run("TagMail with nil store", func(t *testing.T) {
		result, err := autoTagger.TagMail(ctx, "test-id", "test@example.com", "INBOX")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Tags) != 0 {
			t.Error("expected empty result with nil store")
		}
	})
}
