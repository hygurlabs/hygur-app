package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

// seedOwnerIdentifier builds a store where the OWNER (Alex Martin) carries a national number,
// proximity-linked and corroborated so it resolves at tier≥med. Returns the db + owner matcher.
func seedOwnerIdentifier(t *testing.T) (*store.DB, *identity.Matcher, time.Time) {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	now := time.Now().UTC()

	owner := contradict.NormKey("Alex Martin")
	idNorm := "0000000097" // masked fake national number (test fixture, not real PII)
	at := now.Add(-24 * time.Hour).Format(time.RFC3339)

	mk := func(cid string, mentions []store.EntityMention) {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
			Metadata: map[string]any{"canonical_date": at},
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		if err := db.ReplaceEntityMentions(ctx, cid, mentions); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
	}
	per := store.EntityMention{EntityNorm: owner, EntityRaw: "Alex Martin", Attribute: "ner_person", AssertedAt: at}
	idm := store.EntityMention{EntityNorm: idNorm, EntityRaw: idNorm, Attribute: "id_national_number", AssertedAt: at}
	mk("i1", []store.EntityMention{per, idm})
	mk("i2", []store.EntityMention{per, idm})
	for i := 0; i < 5; i++ {
		id := "f" + string(rune('1'+i))
		mk(id, []store.EntityMention{{EntityNorm: "filler" + string(rune('1'+i)), AssertedAt: at}})
	}
	if err := db.UpsertCoOccurrences(ctx, []string{owner, idNorm}, at); err != nil {
		t.Fatalf("co-occ: %v", err)
	}
	if err := db.UpsertCoOccurrences(ctx, []string{owner, idNorm}, at); err != nil {
		t.Fatalf("co-occ: %v", err)
	}
	if err := db.ReplaceIdentifierLinks(ctx, "i1", []store.IdentifierLink{
		{ContentID: "i1", PersonNorm: owner, IDNorm: idNorm, IDType: "national_number", Prox: 1.0},
	}); err != nil {
		t.Fatalf("id links: %v", err)
	}
	return db, identity.NewMatcher([]string{"Alex Martin"}), now
}

// A first-person query ("what is my national number?") names no proper noun, so subject
// detection surfaces nothing — the OWNER must still be resolved as the subject (first-person
// framing) and his determined national number assembled at tier≥med. This is the CORE thesis:
// the value comes from the deterministic resolver, not from reading a document.
func TestAssembleQueryFacts_FirstPersonResolvesOwner(t *testing.T) {
	db, owner, now := seedOwnerIdentifier(t)
	ctx := context.Background()

	facts, err := AssembleQueryFacts(ctx, db, "what is my national number?", now, owner, "Alex Martin")
	if err != nil {
		t.Fatalf("AssembleQueryFacts: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("no facts assembled for a first-person owner query")
	}
	owned := facts[0]
	if !owned.IsOwner {
		t.Errorf("first subject IsOwner = false, want true")
	}
	var found *EngramIdentifier
	for i := range owned.Identity {
		if owned.Identity[i].Type == "national_number" {
			found = &owned.Identity[i]
		}
	}
	if found == nil {
		t.Fatalf("owner determined facts missing national_number: %+v", owned.Identity)
	}
	if found.Tier != "high" && found.Tier != "medium" {
		t.Errorf("national_number tier = %q, want high|medium", found.Tier)
	}
	if found.Value != "0000000097" {
		t.Errorf("national_number value = %q, want the determined fixture value", found.Value)
	}
}

// An unknown subject with no presence yields no facts (fail-closed): AssembleDeterminedFacts
// returns nil so the authoritative layer stays silent rather than fabricating a subject.
func TestAssembleDeterminedFacts_UnknownSubjectNil(t *testing.T) {
	db, owner, now := seedOwnerIdentifier(t)
	ctx := context.Background()
	df, err := AssembleDeterminedFacts(ctx, db, "Nonexistent Person", now, owner)
	if err != nil {
		t.Fatalf("AssembleDeterminedFacts: %v", err)
	}
	if df.HasFacts() {
		t.Errorf("unknown subject reported facts: %+v", df)
	}
}
