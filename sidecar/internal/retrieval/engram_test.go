package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// AssembleEngram builds a subject dossier deterministically: a Hebbian network, a
// strength-ordered timeline of direct (order 1) and neighbor (order 2) items, and the
// live/dead decisions compartment. This locks that wiring end to end.
func TestAssembleEngram(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	acme := contradict.NormKey("Acme")
	alice := contradict.NormKey("Alice")
	recent := now.Add(-24 * time.Hour).Format(time.RFC3339)
	old := now.Add(-300 * 24 * time.Hour).Format(time.RFC3339)

	mk := func(cid, canonical string, mentions []store.EntityMention) {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
			Metadata: map[string]any{"canonical_date": canonical},
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		if err := db.ReplaceEntityMentions(ctx, cid, mentions); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
	}
	org := func(at string) store.EntityMention {
		return store.EntityMention{EntityNorm: acme, EntityRaw: "Acme", Attribute: "ner_org", AssertedAt: at}
	}
	per := func(at string) store.EntityMention {
		return store.EntityMention{EntityNorm: alice, EntityRaw: "Alice", Attribute: "ner_person", AssertedAt: at}
	}

	// i1 & i2 mention Acme + Alice (they co-occur → a strong NPMI edge), i1 recent + a
	// standing decision, i2 old.
	mk("i1", recent, []store.EntityMention{org(recent), per(recent)})
	mk("i2", old, []store.EntityMention{org(old), per(old)})
	// i3 mentions Alice only, recent → reachable from Acme at 2nd order via Alice.
	mk("i3", recent, []store.EntityMention{per(recent)})
	// Filler items with unrelated entities, so Acme/Alice stay rare relative to the
	// corpus and their co-occurrence beats chance (positive NPMI).
	for i := 0; i < 5; i++ {
		id := "f" + string(rune('1'+i))
		mk(id, recent, []store.EntityMention{{EntityNorm: "filler" + string(rune('1'+i)), AssertedAt: recent}})
	}

	// Acme+Alice co-occur in i1 and i2 → co_count 2.
	db.UpsertCoOccurrences(ctx, []string{acme, alice}, recent)
	if err := db.UpsertCoOccurrences(ctx, []string{acme, alice}, recent); err != nil {
		t.Fatalf("co-occurrences: %v", err)
	}
	if err := db.UpsertItemSignals(ctx, []store.ItemSignal{
		{ContentID: "i1", Salience: 0.6, Surprise: 0.2, ScoredAt: now},
		{ContentID: "i2", Salience: 0.6, ScoredAt: now},
		{ContentID: "i3", Salience: 0.6, ScoredAt: now},
	}); err != nil {
		t.Fatalf("signals: %v", err)
	}
	if err := db.UpsertDecisionAttrs(ctx, "i1", "standing", recent, nil, ""); err != nil {
		t.Fatalf("decision: %v", err)
	}

	eng, err := AssembleEngram(ctx, db, "Acme", now)
	if err != nil {
		t.Fatalf("AssembleEngram: %v", err)
	}
	if eng == nil {
		t.Fatal("nil engram for a known subject")
	}

	if eng.Subject.Norm != acme || eng.Subject.Type != "org" {
		t.Errorf("subject = %+v, want {%s org}", eng.Subject, acme)
	}
	// Network: Alice is a neighbor, typed as a person (Part B).
	var aliceType string
	found := false
	for _, n := range eng.Network {
		if n.Norm == alice {
			found, aliceType = true, n.Type
		}
	}
	if !found {
		t.Errorf("network %v missing neighbor %s", eng.Network, alice)
	}
	if aliceType != "person" {
		t.Errorf("Alice neighbor type = %q, want person", aliceType)
	}
	// Timeline order + tagging: i1/i2 direct (order 1), i3 via Alice (order 2).
	byID := map[string]EngramItem{}
	for _, it := range eng.Timeline {
		byID[it.ContentID] = it
	}
	if len(byID) != 3 {
		t.Fatalf("timeline = %d items, want 3: %+v", len(byID), eng.Timeline)
	}
	if byID["i1"].Order != 1 || byID["i2"].Order != 1 {
		t.Errorf("i1/i2 should be order 1, got %d/%d", byID["i1"].Order, byID["i2"].Order)
	}
	if byID["i3"].Order != 2 || byID["i3"].ViaNeighbor != alice {
		t.Errorf("i3 should be order 2 via %s, got order %d via %q", alice, byID["i3"].Order, byID["i3"].ViaNeighbor)
	}
	// Recent + surprising i1 outranks old i2 (recency × salience + von Restorff spike).
	if byID["i1"].score <= byID["i2"].score {
		t.Errorf("recent i1 (%.3f) should outrank old i2 (%.3f)", byID["i1"].score, byID["i2"].score)
	}
	if byID["i2"].DateMissing || byID["i2"].Date == "" {
		t.Errorf("i2 should carry its canonical date, got %+v", byID["i2"])
	}
	// A2: the FSRS power-law tail keeps a 300-day-old item's strength meaningful
	// (~0.77 here), where the exponential Ebbinghaus curve would have collapsed it to
	// ~0.01 and flattened the whole history.
	if byID["i2"].Strength < 0.5 {
		t.Errorf("old item strength collapsed (%.3f); FSRS power-law tail should keep it meaningful", byID["i2"].Strength)
	}
	// Live/dead compartment: i1 is a standing decision.
	if len(eng.Decisions) != 1 || eng.Decisions[0].ContentID != "i1" || eng.Decisions[0].DecisionStatus != "standing" {
		t.Errorf("decisions compartment = %+v, want [i1 standing]", eng.Decisions)
	}

	// Unknown subject → nil (404 at the handler).
	if e, _ := AssembleEngram(ctx, db, "Nonexistent", now); e != nil {
		t.Errorf("unknown subject should yield nil, got %+v", e)
	}
}
