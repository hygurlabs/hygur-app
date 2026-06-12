package store

import (
	"context"
	"testing"
)

func TestDecisionStatuses(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	makeDecisionItem(t, db, "decision:a", "Standing decision")
	makeDecisionItem(t, db, "decision:b", "Superseded decision")
	if err := db.UpsertDecisionAttrs(ctx, "decision:a", DecisionStanding, "", nil, ""); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "decision:b", DecisionSuperseded, "", nil, ""); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	// Mix decision ids with a non-decision capture id that has no attrs row.
	got, err := db.DecisionStatuses(ctx, []string{"decision:a", "decision:b", "mail:99"})
	if err != nil {
		t.Fatalf("DecisionStatuses: %v", err)
	}
	if got["decision:a"] != DecisionStanding {
		t.Errorf("decision:a status = %q, want %q", got["decision:a"], DecisionStanding)
	}
	if got["decision:b"] != DecisionSuperseded {
		t.Errorf("decision:b status = %q, want %q", got["decision:b"], DecisionSuperseded)
	}
	if _, ok := got["mail:99"]; ok {
		t.Errorf("non-decision id mail:99 must be absent from the map (got %q)", got["mail:99"])
	}

	// Empty input → empty map, no error.
	empty, err := db.DecisionStatuses(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("DecisionStatuses(nil) = %v (err %v); want empty map", empty, err)
	}
}
