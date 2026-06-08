package ingest

import (
	"context"
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func itemTagNames(t *testing.T, db *store.DB, contentID string) []string {
	t.Helper()
	tags, err := db.GetTagsForItem(context.Background(), contentID)
	if err != nil {
		t.Fatalf("GetTagsForItem: %v", err)
	}
	names := make([]string, 0, len(tags))
	for _, tg := range tags {
		names = append(names, tg.Name)
	}
	return names
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// IngestText is how edge/cloud mail reaches the central KB. It must auto-tag mail
// with its mailbox folder. (Topic tags need the Tier-2 LLM client, absent here,
// so only the folder tag is asserted; sender-domain tags were dropped.)
func TestIngestText_AutoTagsMailFolder(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	ctx := context.Background()

	res, err := i.IngestText(ctx, IngestTextInput{
		Title:      "Facture janvier",
		Text:       "Subject: Facture janvier\nFrom: EDF <noreply@edf.fr>\n\nVotre facture est disponible.",
		SourceType: "mail",
		SourceRef:  "proton:msg-1",
		Author:     "EDF <noreply@edf.fr>",
		Metadata:   map[string]any{"from": "EDF <noreply@edf.fr>", "mailbox": "INBOX/Factures"},
	})
	if err != nil {
		t.Fatalf("IngestText: %v", err)
	}

	names := itemTagNames(t, db, res.ContentID)
	if !contains(names, "mail:Factures") {
		t.Errorf("expected mailbox-folder tag mail:Factures, got %v", names)
	}
	if contains(names, "mail:edf.fr") {
		t.Errorf("sender-domain tags should be dropped, got %v", names)
	}
}

// Non-mail text ingest must not pick up mail tags.
func TestIngestText_NonMailNotTagged(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	ctx := context.Background()

	res, err := i.IngestText(ctx, IngestTextInput{
		Text:       "just a note",
		SourceType: "note",
		SourceRef:  "note:1",
	})
	if err != nil {
		t.Fatalf("IngestText: %v", err)
	}
	if names := itemTagNames(t, db, res.ContentID); len(names) != 0 {
		t.Errorf("expected no tags for a note, got %v", names)
	}
}

// RetagMail purges stale auto-tags and re-applies the folder tag. (Topics need
// the LLM client, absent here.)
func TestRetagMail_PurgesAndRefoldersMail(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	ctx := context.Background()

	// A mail item under a Proton custom folder, with NO tags yet.
	item := &store.KnowledgeItem{
		ContentID:      "c-1",
		SourceType:     "mail",
		Title:          "Invoice",
		NormalizedText: "body",
		Metadata:       map[string]any{"from": "billing@acme.io", "mailbox": "Folders/Factures"},
		VersionID:      "v1",
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}

	// A stale auto domain-tag from the old scheme, attached to the item.
	stale, err := db.GetOrCreateTag(ctx, "mail:acme.io", true, "mail:from:@acme.io")
	if err != nil {
		t.Fatalf("GetOrCreateTag: %v", err)
	}
	if err := db.AddTagToItem(ctx, "c-1", stale.ID); err != nil {
		t.Fatalf("AddTagToItem: %v", err)
	}

	n, err := i.RetagMail(ctx)
	if err != nil {
		t.Fatalf("RetagMail: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 item processed, got %d", n)
	}

	names := itemTagNames(t, db, "c-1")
	if contains(names, "mail:acme.io") {
		t.Errorf("stale auto domain-tag should be purged, got %v", names)
	}
	if !contains(names, "mail:Factures") {
		t.Errorf("expected mail:Factures after retag, got %v", names)
	}
}
