package store

import (
	"context"
	"testing"
	"time"
)

func makeDecisionItem(t *testing.T, db *DB, id, statement string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &KnowledgeItem{
		ContentID: id, SourceType: SourceTypeDecision, Title: statement, NormalizedText: "",
		Metadata: map[string]any{}, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert decision item %s: %v", id, err)
	}
}

func TestDecisionAttrsListAndStatus(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	makeDecisionItem(t, db, "decision:1", "Proceed with vendor A")
	makeDecisionItem(t, db, "decision:2", "Standing one")
	makeDecisionItem(t, db, "decision:3", "Old superseded one")
	if err := db.UpsertDecisionAttrs(ctx, "decision:1", "proposed", "2026-06-10T00:00:00Z", []string{"mail:42"}, "k1"); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "decision:2", "standing", "2026-06-09T00:00:00Z", nil, ""); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "decision:3", "superseded", "2026-05-01T00:00:00Z", nil, ""); err != nil {
		t.Fatalf("upsert 3: %v", err)
	}

	all, err := db.ListDecisions(ctx, "", "")
	if err != nil || len(all) != 3 {
		t.Fatalf("ListDecisions = %d (err %v)", len(all), err)
	}
	// Proposed sort first, superseded last.
	if all[0].ID != "decision:1" || all[0].Status != "proposed" {
		t.Errorf("proposed should sort first, got %+v", all[0])
	}
	if all[2].ID != "decision:3" {
		t.Errorf("superseded should sort last, got %+v", all[2])
	}
	// Source refs round-trip as a slice.
	if len(all[0].SourceRefs) != 1 || all[0].SourceRefs[0] != "mail:42" {
		t.Errorf("source refs off: %+v", all[0].SourceRefs)
	}

	// Status filter.
	proposed, _ := db.ListDecisions(ctx, "", "proposed")
	if len(proposed) != 1 || proposed[0].ID != "decision:1" {
		t.Errorf("status filter = %+v", proposed)
	}

	// Confirm flips proposed → standing.
	if err := db.SetDecisionStatus(ctx, "decision:1", "standing"); err != nil {
		t.Fatalf("SetDecisionStatus: %v", err)
	}
	got, _ := db.GetDecision(ctx, "decision:1")
	if got == nil || got.Status != "standing" {
		t.Errorf("confirm did not flip status: %+v", got)
	}
	if missing, _ := db.GetDecision(ctx, "decision:nope"); missing != nil {
		t.Errorf("want nil for missing decision")
	}
}

func TestDecisionDedup(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	key := DecisionDedupKey("mail:7", "Sign the lease by Friday")
	// Same source + statement (whitespace/case-insensitive) must yield the same key.
	if key != DecisionDedupKey("mail:7", "sign the   lease  by friday") {
		t.Error("dedup key should be whitespace/case-insensitive")
	}
	if key == DecisionDedupKey("mail:8", "Sign the lease by Friday") {
		t.Error("dedup key should depend on the source ref")
	}

	if exists, _ := db.DecisionDedupExists(ctx, key); exists {
		t.Error("dedup should not exist before insert")
	}
	makeDecisionItem(t, db, "decision:d", "Sign the lease by Friday")
	if err := db.UpsertDecisionAttrs(ctx, "decision:d", "proposed", "", []string{"mail:7"}, key); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if exists, _ := db.DecisionDedupExists(ctx, key); !exists {
		t.Error("dedup should exist after insert")
	}
	// An empty key is never considered a duplicate (manual decisions).
	if exists, _ := db.DecisionDedupExists(ctx, ""); exists {
		t.Error("empty dedup key must not match")
	}
}

// makeSourceItem inserts an ingested source item carrying a content_hash.
func makeSourceItem(t *testing.T, db *DB, id, hash string) {
	t.Helper()
	now := time.Now()
	if err := db.InsertKnowledgeItem(context.Background(), &KnowledgeItem{
		ContentID: id, SourceType: SourceTypeFile, Title: "src", NormalizedText: "body",
		Metadata: map[string]any{"content_hash": hash}, VersionID: "v1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert source item %s: %v", id, err)
	}
}

// makeDecisionAt inserts a decision knowledge_item with an explicit created_at so
// the earliest-created survivor is deterministic.
func makeDecisionAt(t *testing.T, db *DB, id, statement string, created time.Time) {
	t.Helper()
	if err := db.InsertKnowledgeItem(context.Background(), &KnowledgeItem{
		ContentID: id, SourceType: SourceTypeDecision, Title: statement, NormalizedText: "",
		Metadata: map[string]any{}, VersionID: "v1", CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		t.Fatalf("insert decision %s: %v", id, err)
	}
}

func TestFindAndDeleteDuplicateDecisions(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Two source items are copies of the same attachment (same content_hash).
	makeSourceItem(t, db, "file:copyA", "HASH-DUP")
	makeSourceItem(t, db, "file:copyB", "HASH-DUP")
	// A third source with a DIFFERENT content_hash but the SAME statement — must
	// NOT be grouped (fail-closed: distinct content = distinct decision).
	makeSourceItem(t, db, "file:other", "HASH-OTHER")

	// Two duplicate decisions grounded in the two copies (earliest = keeper).
	makeDecisionAt(t, db, "decision:keep", "Sign the lease by Friday", base)
	makeDecisionAt(t, db, "decision:dupe", "sign the   lease  by friday", base.Add(time.Hour))
	makeDecisionAt(t, db, "decision:distinct", "Sign the lease by Friday", base.Add(2*time.Hour))
	if err := db.UpsertDecisionAttrs(ctx, "decision:keep", "proposed", "", []string{"file:copyA"}, "kA"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "decision:dupe", "proposed", "", []string{"file:copyB"}, "kB"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "decision:distinct", "proposed", "", []string{"file:other"}, "kC"); err != nil {
		t.Fatal(err)
	}

	groups, err := db.FindDuplicateDecisions(ctx)
	if err != nil {
		t.Fatalf("FindDuplicateDecisions: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("want 1 dup group, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Keep != "decision:keep" {
		t.Errorf("keeper should be earliest-created, got %q", g.Keep)
	}
	if len(g.Delete) != 1 || g.Delete[0] != "decision:dupe" {
		t.Errorf("delete set = %+v, want [decision:dupe]", g.Delete)
	}
	if g.ContentHash != "HASH-DUP" {
		t.Errorf("group content_hash = %q", g.ContentHash)
	}

	n, err := db.DeleteDecisions(ctx, g.Delete)
	if err != nil || n != 1 {
		t.Fatalf("DeleteDecisions = %d (err %v)", n, err)
	}
	all, _ := db.ListDecisions(ctx, "", "")
	if len(all) != 2 {
		t.Fatalf("after cleanup want 2 decisions, got %d", len(all))
	}
	// The cascade removed the decision_attrs row too (not just the knowledge_item).
	if got, _ := db.GetDecision(ctx, "decision:dupe"); got != nil {
		t.Error("deleted decision still resolvable")
	}
	if got, _ := db.GetDecision(ctx, "decision:keep"); got == nil {
		t.Error("survivor was wrongly removed")
	}
	// Re-running finds nothing (idempotent).
	if again, _ := db.FindDuplicateDecisions(ctx); len(again) != 0 {
		t.Errorf("second pass should be clean, got %d groups", len(again))
	}
}

func TestAppSettingRoundTrip(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if v, _ := db.GetAppSetting(ctx, "missing"); v != "" {
		t.Errorf("absent key should be empty, got %q", v)
	}
	if err := db.SetAppSetting(ctx, "decision_scan_watermark", "2026-06-11T00:00:00Z"); err != nil {
		t.Fatalf("SetAppSetting: %v", err)
	}
	if v, _ := db.GetAppSetting(ctx, "decision_scan_watermark"); v != "2026-06-11T00:00:00Z" {
		t.Errorf("round-trip failed, got %q", v)
	}
	// UPSERT overwrites.
	_ = db.SetAppSetting(ctx, "decision_scan_watermark", "2026-06-12T00:00:00Z")
	if v, _ := db.GetAppSetting(ctx, "decision_scan_watermark"); v != "2026-06-12T00:00:00Z" {
		t.Errorf("overwrite failed, got %q", v)
	}
}
