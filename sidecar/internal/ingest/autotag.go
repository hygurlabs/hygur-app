// Package ingest provides document ingestion and auto-tagging for the Hygur knowledge base.
package ingest

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/hygur/sidecar/internal/store"
)

// AutoTagger generates and applies automatic tags to ingested content.
type AutoTagger struct {
	store *store.DB
}

// NewAutoTagger creates a new AutoTagger.
func NewAutoTagger(db *store.DB) *AutoTagger {
	return &AutoTagger{
		store: db,
	}
}

// AutoTagResult contains the result of auto-tagging.
type AutoTagResult struct {
	// Tags contains the tag IDs that were applied.
	Tags []string

	// NewTags contains any newly created tags.
	NewTags []string
}

// TagDocument applies auto-tags to a document based on its folder path.
// It extracts up to 3 folder levels from the path.
func (a *AutoTagger) TagDocument(ctx context.Context, contentID, sourcePath string) (*AutoTagResult, error) {
	if a.store == nil {
		return &AutoTagResult{}, nil
	}

	result := &AutoTagResult{
		Tags:    make([]string, 0),
		NewTags: make([]string, 0),
	}

	// Extract folder tags from path
	folderTags := extractFolderTags(sourcePath)

	for _, tagName := range folderTags {
		autoRule := "folder:" + tagName

		// Get or create the tag
		tag, err := a.store.GetOrCreateTag(ctx, tagName, true, autoRule)
		if err != nil {
			continue // Skip on error
		}

		// Check if this is a new tag
		if tag.ItemCount == 0 {
			result.NewTags = append(result.NewTags, tag.ID)
		}

		// Add tag to item
		if err := a.store.AddTagToItem(ctx, contentID, tag.ID); err != nil {
			continue // Skip on error
		}

		result.Tags = append(result.Tags, tag.ID)
	}

	// Prune auto-tags if needed
	_ = a.store.PruneAutoTags(ctx)

	return result, nil
}

// TagMail applies the mailbox-folder auto-tag to an email. Sender-domain tags
// were dropped in favour of semantic topic tags (see TagTopics) — too many,
// too little "meaning" — so only the folder tag remains here. senderEmail is
// kept so the direct-IMAP indexer's call site is unchanged; it is unused.
func (a *AutoTagger) TagMail(ctx context.Context, contentID string, senderEmail string, mailboxPath string) (*AutoTagResult, error) {
	_ = senderEmail
	if a.store == nil {
		return &AutoTagResult{}, nil
	}
	a.tagMailFolder(ctx, contentID, mailboxPath)
	_ = a.store.PruneAutoTags(ctx)
	return &AutoTagResult{}, nil
}

// TagTopics applies semantic topic tags ("topic:<label>") derived by the Tier-2
// LLM extractor — the "meaning" grouping that classifies mail by subject rather
// than by sender. The label vocabulary is small and reused across documents, so
// the tag set stays compact.
func (a *AutoTagger) TagTopics(ctx context.Context, contentID string, topics []string) (*AutoTagResult, error) {
	if a.store == nil {
		return &AutoTagResult{}, nil
	}
	a.tagTopics(ctx, contentID, topics)
	_ = a.store.PruneAutoTags(ctx)
	return &AutoTagResult{}, nil
}

// tagMailFolder adds the mailbox-folder tag. It does NOT prune — a batch backfill
// prunes once at the end to avoid deleting (and orphaning) tags mid-run; the
// single-item public methods prune themselves.
func (a *AutoTagger) tagMailFolder(ctx context.Context, contentID, mailboxPath string) {
	folderTag := extractMailboxFolderTag(mailboxPath)
	if folderTag == "" {
		return
	}
	tag, err := a.store.GetOrCreateTag(ctx, "mail:"+folderTag, true, "mail:folder:"+folderTag)
	if err != nil {
		return
	}
	_ = a.store.AddTagToItem(ctx, contentID, tag.ID)
}

// tagTopics adds the "topic:<label>" tags. No pruning (see tagMailFolder).
func (a *AutoTagger) tagTopics(ctx context.Context, contentID string, topics []string) {
	for _, topic := range topics {
		topic = strings.ToLower(strings.TrimSpace(topic))
		if topic == "" {
			continue
		}
		tag, err := a.store.GetOrCreateTag(ctx, "topic:"+topic, true, "topic:"+topic)
		if err != nil {
			continue
		}
		_ = a.store.AddTagToItem(ctx, contentID, tag.ID)
	}
}

// extractFolderTags extracts up to 3 folder levels from a file path.
// It excludes the filename and common root folders.
func extractFolderTags(path string) []string {
	if path == "" {
		return nil
	}

	// Get directory path (exclude filename)
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return nil
	}

	// Split path into components
	var components []string
	for dir != "." && dir != "/" && dir != "" {
		base := filepath.Base(dir)
		// Skip common root folders
		if !isCommonRootFolder(base) {
			components = append([]string{base}, components...)
		}
		dir = filepath.Dir(dir)
	}

	// Take last 3 components (closest to the file)
	if len(components) > 3 {
		components = components[len(components)-3:]
	}

	// Filter out empty and hidden folders
	var result []string
	for _, comp := range components {
		comp = strings.TrimSpace(comp)
		if comp != "" && !strings.HasPrefix(comp, ".") {
			result = append(result, comp)
		}
	}

	return result
}

// isCommonRootFolder returns true for common root folders that should be skipped.
func isCommonRootFolder(name string) bool {
	lower := strings.ToLower(name)
	commonRoots := []string{
		"users", "home", "documents", "desktop", "downloads",
		"volumes", "mnt", "media", "var", "tmp", "opt",
		"library", "applications",
	}
	for _, root := range commonRoots {
		if lower == root {
			return true
		}
	}
	return false
}

// extractDomainTag extracts the domain from an email address.
// It returns empty string if the email is invalid or from common providers.
func extractDomainTag(email string) string {
	if email == "" {
		return ""
	}

	// Extract domain part
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}

	// Sanitize: sender addresses often arrive angle-bracketed ("Name <a@edf.fr>"),
	// so parts[1] can be "edf.fr>". Keep only the leading run of valid domain
	// characters (a-z, 0-9, '.', '-'), dropping any trailing '>', space, or junk.
	domain := strings.ToLower(strings.TrimSpace(parts[1]))
	if i := strings.IndexFunc(domain, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-')
	}); i >= 0 {
		domain = domain[:i]
	}
	if domain == "" {
		return ""
	}

	// Skip common email providers (not useful for tagging)
	commonProviders := []string{
		"gmail.com", "googlemail.com",
		"yahoo.com", "yahoo.fr", "yahoo.co.uk",
		"example.com", "outlook.com", "live.com", "msn.com",
		"icloud.com", "me.com", "mac.com",
		"protonmail.com", "proton.me", "pm.me",
		"aol.com",
		"mail.com",
		"zoho.com",
	}

	for _, provider := range commonProviders {
		if domain == provider {
			return ""
		}
	}

	return domain
}

// extractMailboxFolderTag extracts the top folder from a mailbox path.
// For "INBOX/Projects/Active", it returns "Projects".
// For "INBOX", it returns empty string.
func extractMailboxFolderTag(mailboxPath string) string {
	if mailboxPath == "" {
		return ""
	}

	// Normalize separators
	path := strings.ReplaceAll(mailboxPath, "\\", "/")

	// Split into parts
	parts := strings.Split(path, "/")

	// Skip common mailbox names
	skipNames := []string{
		"inbox", "sent", "sent mail", "drafts", "trash", "spam", "junk",
		"archive", "all mail", "starred", "important",
		// Proton Bridge exposes custom folders under "Folders/" and labels under
		// "Labels/" — skip those container prefixes so we tag the real name.
		"folders", "labels",
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		lower := strings.ToLower(part)
		skip := false
		for _, skipName := range skipNames {
			if lower == skipName {
				skip = true
				break
			}
		}

		if !skip {
			return part
		}
	}

	return ""
}

// ApplyManualTags applies user-specified tags to a content item.
func (a *AutoTagger) ApplyManualTags(ctx context.Context, contentID string, tagNames []string) error {
	if a.store == nil {
		return nil
	}

	for _, tagName := range tagNames {
		tagName = strings.TrimSpace(tagName)
		if tagName == "" {
			continue
		}

		// Get or create the tag (manual tags are not auto-generated)
		tag, err := a.store.GetOrCreateTag(ctx, tagName, false, "")
		if err != nil {
			continue // Skip on error
		}

		// Add tag to item
		if err := a.store.AddTagToItem(ctx, contentID, tag.ID); err != nil {
			continue // Skip on error
		}
	}

	return nil
}
