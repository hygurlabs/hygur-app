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
