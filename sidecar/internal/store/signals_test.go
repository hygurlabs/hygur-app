package store

import (
	"context"
	"testing"
	"time"
)

func TestItemSignalsSummary(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(id, title string) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: id, SourceType: "note", Title: title,
			NormalizedText: "b", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mk("a", "A")
	mk("b", "B")
	mk("c", "C")

	// Mid-bucket values to avoid float-edge histogram flakiness.
	sigs := []ItemSignal{
		{ContentID: "a", Salience: 0.95, Strength: 0.85, Surprise: 0.75, Exempt: true, Tier: "hot", ScoredAt: now},
		{ContentID: "b", Salience: 0.55, Strength: 0.45, Surprise: 0.0, Exempt: false, Tier: "hot", ScoredAt: now},
		{ContentID: "c", Salience: 0.05, Strength: 0.02, Surprise: 0.0, Exempt: false, Tier: "cold", ScoredAt: now},
	}
	if err := db.UpsertItemSignals(ctx, sigs); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	s, err := db.ItemSignalsSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.Scored != 3 || s.Hot != 2 || s.Cold != 1 || s.Exempt != 1 {
		t.Errorf("counts: scored=%d hot=%d cold=%d exempt=%d (want 3/2/1/1)", s.Scored, s.Hot, s.Cold, s.Exempt)
	}
	if s.Surprise.CountNonzero != 1 {
		t.Errorf("surprise nonzero=%d want 1", s.Surprise.CountNonzero)
	}
	if s.Salience.Histogram[9] != 1 || s.Salience.Histogram[5] != 1 || s.Salience.Histogram[0] != 1 {
		t.Errorf("salience histogram = %v (want 1 in buckets 0,5,9)", s.Salience.Histogram)
	}
	if s.Salience.Max < 0.94 || s.Salience.Min > 0.06 {
		t.Errorf("salience min/max = %.3f/%.3f", s.Salience.Min, s.Salience.Max)
	}
	if len(s.TopSalience) != 3 || s.TopSalience[0].ContentID != "a" {
		t.Errorf("top salience order: %+v", s.TopSalience)
	}
	if s.TopSalience[0].SourceType != "note" || s.TopSalience[0].Title != "A" || !s.TopSalience[0].Exempt {
		t.Errorf("top salience join/flags: %+v", s.TopSalience[0])
	}
	if len(s.TopSurprise) != 3 || s.TopSurprise[0].ContentID != "a" {
		t.Errorf("top surprise order: %+v", s.TopSurprise)
	}

	// Empty DB → zero summary, no error.
	db2, _ := NewDB(":memory:")
	defer db2.Close()
	if s2, err := db2.ItemSignalsSummary(ctx); err != nil || s2.Scored != 0 {
		t.Errorf("empty summary: scored=%d err=%v", s2.Scored, err)
	}
}
