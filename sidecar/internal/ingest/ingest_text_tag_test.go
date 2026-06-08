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

// IngestText is how edge/cloud mail reaches the central KB; it must auto-tag mail
// (sender domain + mailbox folder) the way the file and direct-IMAP paths do.
func TestIngestText_AutoTagsMail(t *testing.T) {
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
	if !contains(names, "mail:edf.fr") {
		t.Errorf("expected sender-domain tag mail:edf.fr, got %v", names)
	}
	if !contains(names, "mail:Factures") {
		t.Errorf("expected mailbox-folder tag mail:Factures, got %v", names)
	}
}

// Non-mail text ingest must NOT pick up mail tags.
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

// RetagMail backfills tags for mail ingested before tagging existed (simulated
// here by tagging items that start with none).
func TestRetagMail_Backfills(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	i := NewIngestorWithStore(db)
	ctx := context.Background()

	// Insert a mail item directly (no tagging), mimicking the pre-fix state.
	item := &store.KnowledgeItem{
		ContentID:      "c-1",
		SourceType:     "mail",
		Title:          "Invoice",
		NormalizedText: "body",
		Metadata:       map[string]any{"from": "billing@acme.io", "mailbox": "INBOX"},
		VersionID:      "v1",
	}
	if err := db.InsertKnowledgeItem(ctx, item); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}
	if names := itemTagNames(t, db, "c-1"); len(names) != 0 {
		t.Fatalf("precondition: item should start untagged, got %v", names)
	}

	n, err := i.RetagMail(ctx)
	if err != nil {
		t.Fatalf("RetagMail: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 item retagged, got %d", n)
	}
	if names := itemTagNames(t, db, "c-1"); !contains(names, "mail:acme.io") {
		t.Errorf("expected mail:acme.io after backfill, got %v", names)
	}
}
