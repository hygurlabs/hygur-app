package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// cleanTypeLabel strips the id_* prefix to a human phrase for the network display; other
// types pass through untouched.
func TestCleanTypeLabel(t *testing.T) {
	cases := map[string]string{
		"id_national_number": "national number",
		"id_duns":            "duns",
		"person":             "person",
		"org":                "org",
	}
	for in, want := range cases {
		if got := cleanTypeLabel(in); got != want {
			t.Errorf("cleanTypeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// aggregateClaims groups a subject's direct-item claims by attribute: agreeing sources
// corroborate one value; a divergent value from a separate source is contested. Negations
// and off-subject claims are excluded.
func TestAggregateClaims(t *testing.T) {
	subj := []string{"alice bernard"}
	claims := []contradict.Claim{
		{Entity: "Alice Bernard", Attribute: "role", Value: "manager", Polarity: "affirm", SourceID: "i1"},
		{Entity: "Alice Bernard", Attribute: "role", Value: "manager", Polarity: "affirm", SourceID: "i2"},
		{Entity: "Alice Bernard", Attribute: "city", Value: "Paris", Polarity: "affirm", SourceID: "i1"},
		{Entity: "Alice Bernard", Attribute: "city", Value: "Lyon", Polarity: "affirm", SourceID: "i2"},
		{Entity: "Alice Bernard", Attribute: "status", Value: "left", Polarity: "negate", SourceID: "i3"}, // dropped (negate)
		{Entity: "Other Co", Attribute: "role", Value: "vendor", Polarity: "affirm", SourceID: "i4"},       // dropped (off-subject)
	}
	out := aggregateClaims(claims, subj)
	byAttr := map[string]EngramClaim{}
	for _, c := range out {
		byAttr[c.Attribute] = c
	}
	if len(out) != 2 {
		t.Fatalf("want 2 beliefs (role, city), got %d: %+v", len(out), out)
	}
	if role := byAttr["role"]; role.State != "corroborated" || role.Corroboration != 2 || role.Value != "manager" {
		t.Errorf("role = %+v, want corroborated ×2 manager", role)
	}
	if city := byAttr["city"]; city.State != "contested" {
		t.Errorf("city = %+v, want contested", city)
	}
}

// End-to-end dossier enrichment (WP36.a): the assembled Engram carries the subject's typed
// identifier (tier shown), aggregated beliefs, and a network whose id_* neighbor is exposed
// under a clean label.
func TestAssembleEngram_Enriched(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	alice := contradict.NormKey("Alice Bernard")
	idNorm := "0000000097" // masked fake national number (test fixture, not real PII)
	at := now.Add(-24 * time.Hour).Format(time.RFC3339)

	mk := func(cid string, claims []any, mentions []store.EntityMention) {
		md := map[string]any{"canonical_date": at}
		if claims != nil {
			md["extracted_claims"] = claims
		}
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now, Metadata: md,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		if err := db.ReplaceEntityMentions(ctx, cid, mentions); err != nil {
			t.Fatalf("mentions %s: %v", cid, err)
		}
	}
	per := store.EntityMention{EntityNorm: alice, EntityRaw: "Alice Bernard", Attribute: "ner_person", AssertedAt: at}
	idm := store.EntityMention{EntityNorm: idNorm, EntityRaw: idNorm, Attribute: "id_national_number", AssertedAt: at}
	claim := func(attr, val, src string) any {
		return map[string]any{"entity": "Alice Bernard", "attribute": attr, "value": val, "polarity": "affirm", "source_id": src, "quote": attr + " " + val}
	}

	// Two direct items: Alice + her national number co-occur (a strong NPMI edge + proximity),
	// and both assert role=manager (corroborated ×2).
	mk("i1", []any{claim("role", "manager", "i1")}, []store.EntityMention{per, idm})
	mk("i2", []any{claim("role", "manager", "i2")}, []store.EntityMention{per, idm})
	// Filler so Alice/the number stay rare → positive NPMI.
	for i := 0; i < 5; i++ {
		id := "f" + string(rune('1'+i))
		mk(id, nil, []store.EntityMention{{EntityNorm: "filler" + string(rune('1'+i)), AssertedAt: at}})
	}
	if err := db.UpsertCoOccurrences(ctx, []string{alice, idNorm}, at); err != nil {
		t.Fatalf("co-occ: %v", err)
	}
	if err := db.UpsertCoOccurrences(ctx, []string{alice, idNorm}, at); err != nil {
		t.Fatalf("co-occ: %v", err)
	}
	// Proximity link: the number is unambiguously Alice's (drives tier≥med).
	if err := db.ReplaceIdentifierLinks(ctx, "i1", []store.IdentifierLink{
		{ContentID: "i1", PersonNorm: alice, IDNorm: idNorm, IDType: "national_number", Prox: 1.0},
	}); err != nil {
		t.Fatalf("id links: %v", err)
	}

	eng, err := AssembleEngram(ctx, db, "Alice Bernard", now, nil)
	if err != nil {
		t.Fatalf("AssembleEngram: %v", err)
	}
	if eng == nil {
		t.Fatal("nil engram")
	}

	// Identity: national number resolved at tier≥med, exposed with a clean label.
	var idFound *EngramIdentifier
	for i := range eng.Identity {
		if eng.Identity[i].Type == "national_number" {
			idFound = &eng.Identity[i]
		}
	}
	if idFound == nil {
		t.Fatalf("identity block missing national_number: %+v", eng.Identity)
	}
	if idFound.Tier != "high" && idFound.Tier != "medium" {
		t.Errorf("national_number tier = %q, want high|medium", idFound.Tier)
	}
	if idFound.Label != "national number" {
		t.Errorf("national_number label = %q, want %q", idFound.Label, "national number")
	}

	// Beliefs: role=manager corroborated across the two direct items.
	var roleFound *EngramClaim
	for i := range eng.Claims {
		if eng.Claims[i].Attribute == "role" {
			roleFound = &eng.Claims[i]
		}
	}
	if roleFound == nil || roleFound.Corroboration != 2 || roleFound.State != "corroborated" {
		t.Errorf("beliefs = %+v, want role corroborated ×2", eng.Claims)
	}

	// Network: the id_* neighbor carries a stripped, human label.
	for _, n := range eng.Network {
		if n.Norm == idNorm {
			if n.Label != "national number" {
				t.Errorf("id neighbor label = %q, want %q", n.Label, "national number")
			}
		}
	}
}
