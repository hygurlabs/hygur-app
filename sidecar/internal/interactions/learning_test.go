package interactions

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

func TestLearningPillars_Feedback(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	calc := NewLearningCalculator(db)

	base, err := calc.Compute(ctx)
	if err != nil {
		t.Fatalf("compute base: %v", err)
	}
	if len(base.Pillars) != 6 {
		t.Fatalf("want 6 pillars, got %d", len(base.Pillars))
	}
	var sumW float64
	for _, p := range base.Pillars {
		sumW += p.Weight
	}
	if sumW < 0.999 || sumW > 1.001 {
		t.Errorf("pillar weights sum = %.3f, want 1.0", sumW)
	}

	// Two psyché-feedback actions: confirm a decision + resolve a contradiction.
	if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID: "dec:1", SourceType: store.SourceTypeDecision, Title: "D",
		NormalizedText: "x", VersionID: "v1", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insert decision: %v", err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "dec:1", "standing", "", []string{}, ""); err != nil {
		t.Fatalf("decision attrs: %v", err)
	}
	if err := db.DismissContradiction(ctx, "k1"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	after, err := calc.Compute(ctx)
	if err != nil {
		t.Fatalf("compute after: %v", err)
	}
	get := func(key string) LearningPillar {
		for _, p := range after.Pillars {
			if p.Key == key {
				return p
			}
		}
		t.Fatalf("pillar %s missing", key)
		return LearningPillar{}
	}
	if d := get(pillarKeyDecisions); d.Current != 1 {
		t.Errorf("decisions_confirmed = %d, want 1", d.Current)
	}
	if c := get(pillarKeyContradictions); c.Current != 1 {
		t.Errorf("contradictions_resolved = %d, want 1", c.Current)
	}
	if after.Coverage <= base.Coverage {
		t.Errorf("coverage should rise after feedback: base=%.4f after=%.4f", base.Coverage, after.Coverage)
	}
}
