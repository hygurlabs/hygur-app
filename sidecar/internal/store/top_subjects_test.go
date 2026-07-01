package store

import (
	"context"
	"testing"
	"time"
)

// TopSubjects ranks NAMED subjects by distinct-item mention count and excludes
// claim-only entities.
func TestTopSubjects(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	add := func(cid string, ms ...EntityMention) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: cid, SourceType: SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		if err := db.ReplaceEntityMentions(ctx, cid, ms); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
	}
	// acme (org) in 3 items, bob (person) in 2, "the report" (claim-only) in 3, plus
	// noise that must be excluded: "facturation" (topic, in 3) and "monsieur" (greeting
	// mislabelled person, in 3) — both would outrank bob if not filtered.
	full := []EntityMention{
		{EntityNorm: "acme", Attribute: "ner_org"},
		{EntityNorm: "bob", Attribute: "ner_person"},
		{EntityNorm: "the report", Attribute: "about"},
		{EntityNorm: "facturation", Attribute: "ner_topic"},
		{EntityNorm: "monsieur", Attribute: "ner_person"},
	}
	add("i1", full...)
	add("i2", full...)
	add("i3", EntityMention{EntityNorm: "acme", Attribute: "ner_org"}, EntityMention{EntityNorm: "the report", Attribute: "about"}, EntityMention{EntityNorm: "facturation", Attribute: "ner_topic"}, EntityMention{EntityNorm: "monsieur", Attribute: "ner_person"})

	subs, err := db.TopSubjects(ctx, 10)
	if err != nil {
		t.Fatalf("TopSubjects: %v", err)
	}
	// Topic + greeting + claim-only all excluded; only acme (3) then bob (2) remain.
	if len(subs) != 2 {
		t.Fatalf("want 2 clean subjects, got %d: %+v", len(subs), subs)
	}
	if subs[0].Norm != "acme" || subs[0].Type != "org" || subs[0].Mentions != 3 {
		t.Errorf("top = %+v, want {acme org 3}", subs[0])
	}
	if subs[1].Norm != "bob" || subs[1].Type != "person" || subs[1].Mentions != 2 {
		t.Errorf("second = %+v, want {bob person 2}", subs[1])
	}
}
