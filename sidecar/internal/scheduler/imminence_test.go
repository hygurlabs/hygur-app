package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// TestImminentContentIDs is the producer E2E for the imminence boost: a monthly
// recurrence whose next occurrence falls inside the window contributes its source
// content_ids; a one-off does not.
func TestImminentContentIDs(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	mkMail := func(cid, title string, daysAgo int) {
		d := now.AddDate(0, 0, -daysAgo)
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeMail, Title: title,
			NormalizedText: "body", VersionID: cid,
			Metadata:  map[string]any{"canonical_date": d.Format(time.RFC3339)},
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", cid, err)
		}
	}
	// Monthly recurrence: gaps ~30d → period 30 → next = (now-20)+30 = now+10 (in window).
	mkMail("email:r1", "Monthly invoice", 80)
	mkMail("email:r2", "Monthly invoice", 50)
	mkMail("email:r3", "Monthly invoice", 20)
	// A one-off that must not be flagged.
	mkMail("email:x", "Welcome aboard", 3)

	d := &DailyBrief{store: db}
	got := d.ImminentContentIDs(ctx, 14)
	for _, id := range []string{"email:r1", "email:r2", "email:r3"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("expected %s in imminent set; got %v", id, got)
		}
	}
	if _, ok := got["email:x"]; ok {
		t.Fatal("non-recurring item must not be imminent")
	}

	if (*DailyBrief)(nil).ImminentContentIDs(ctx, 14) != nil {
		t.Fatal("nil DailyBrief must return nil")
	}
}
