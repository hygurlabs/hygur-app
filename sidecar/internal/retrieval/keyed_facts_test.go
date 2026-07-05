package retrieval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/keyed"
	"github.com/hygur/sidecar/internal/store"
)

// seedVehicle builds a store where the PLATE GT-139-RR carries a determined "modèle" attribute
// (Tesla Model X 2023) anchored across two dated emails, while a DIFFERENT vehicle (Model Y, company
// order-ref) and a SOLD vehicle (Model 3) carry their own model claims — anchored to their OWN keys /
// no plate, never to GT-139-RR. Fictional fixtures (plate + model are the subject, PII-safe).
func seedVehicle(t *testing.T) (*store.DB, *identity.Matcher, time.Time) {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(cid string, at time.Time, nodes []store.AttrNode) {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: cid, SourceType: store.SourceTypeNote, Title: cid,
			NormalizedText: "x", VersionID: "v1", CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("insert %s: %v", cid, err)
		}
		for i := range nodes {
			nodes[i].ContentID = cid
		}
		if err := db.ReplaceAttrNodes(ctx, cid, nodes); err != nil {
			t.Fatalf("attr nodes %s: %v", cid, err)
		}
	}

	// GT-139-RR = Tesla Model X, stated in two dated emails (the target).
	x := store.AttrNode{KeyNorm: "gt 139 rr", KeyType: "plate", Kind: "vehicle",
		Attribute: "modele", AttrRaw: "modèle", Value: "tesla model x 2023", ValueRaw: "Tesla Model X 2023", Prox: 1.0}
	mk("mailX1", now.Add(-72*time.Hour), []store.AttrNode{x})
	mk("mailX2", now.Add(-48*time.Hour), []store.AttrNode{x})

	// Model Y = a DIFFERENT vehicle, anchored to its own (different) plate — NOT GT-139-RR.
	mk("mailY", now.Add(-24*time.Hour), []store.AttrNode{{
		KeyNorm: "aa 111 bb", KeyType: "plate", Kind: "vehicle",
		Attribute: "modele", AttrRaw: "modèle", Value: "tesla model y", ValueRaw: "Tesla Model Y", Prox: 1.0}})
	// Model 3 = SOLD, a DIFFERENT past vehicle, its own plate.
	mk("mail3", now.Add(-96*time.Hour), []store.AttrNode{{
		KeyNorm: "cc 222 dd", KeyType: "plate", Kind: "vehicle",
		Attribute: "modele", AttrRaw: "modèle", Value: "tesla model 3", ValueRaw: "Tesla Model 3", Prox: 1.0}})

	return db, identity.NewMatcher([]string{"Alex Martin"}), now
}

// The barrier: "le modèle de mon véhicule GT-139-RR" → the determined, plate-anchored Tesla Model X —
// NOT Model 3, NOT Model Y. The value comes from the deterministic keyed resolver, not from RAG.
func TestAssembleQueryFacts_VehicleModelPlateAnchored(t *testing.T) {
	db, owner, now := seedVehicle(t)
	ctx := context.Background()

	facts, err := AssembleQueryFacts(ctx, db, "quel est le modèle de mon véhicule GT-139-RR ?", now, owner, "Alex Martin")
	if err != nil {
		t.Fatalf("AssembleQueryFacts: %v", err)
	}
	var veh *DeterminedFacts
	for i := range facts {
		if facts[i].Subject.Norm == "gt 139 rr" {
			veh = &facts[i]
		}
	}
	if veh == nil {
		t.Fatalf("no determined facts for the plate GT-139-RR: %+v", facts)
	}
	if veh.Subject.Type != "vehicle" {
		t.Errorf("subject type = %q, want vehicle", veh.Subject.Type)
	}
	var model string
	for _, c := range veh.Claims {
		if c.Attribute == "modèle" {
			model = c.Value
		}
	}
	if model != "Tesla Model X 2023" {
		t.Fatalf("determined model = %q, want Tesla Model X 2023", model)
	}
	// Distinct-entity rejection: NEITHER Model Y NOR Model 3 may appear anywhere in the plate's facts.
	for _, c := range veh.Claims {
		if c.Value == "Tesla Model Y" || c.Value == "Tesla Model 3" {
			t.Errorf("distinct vehicle leaked into GT-139-RR: %q", c.Value)
		}
	}
}

// FOUNDER SCENARIO (voie A): « le modèle ET l'assurance de mon véhicule GT-139-RR ». The plate carries
// a determined MODEL (Model X) but NO determined insurance — the CBC omnium was a QUOTE (declined at
// anchoring) and no policy is anchored to the plate. Voie A must surface the model and stay SILENT on
// insurance (decline), never asserting the company Model Y's CBC/loyer here. This is the authoritative
// layer that keeps the chat from re-deriving « votre assurance » from conflating RAG.
func TestAssembleQueryFacts_VehicleInsuranceDeclines(t *testing.T) {
	db, owner, now := seedVehicle(t)
	ctx := context.Background()

	facts, err := AssembleQueryFacts(ctx, db, "quel est le modèle et l'assurance de mon véhicule GT-139-RR ?", now, owner, "Alex Martin")
	if err != nil {
		t.Fatalf("AssembleQueryFacts: %v", err)
	}
	var veh *DeterminedFacts
	for i := range facts {
		if facts[i].Subject.Norm == "gt 139 rr" {
			veh = &facts[i]
		}
	}
	if veh == nil {
		t.Fatalf("no determined facts for GT-139-RR: %+v", facts)
	}
	var sawModel bool
	for _, c := range veh.Claims {
		if c.Attribute == "modèle" && c.Value == "Tesla Model X 2023" {
			sawModel = true
		}
		// No insurance/loyer may be affirmed for the personal plate — and never a CBC / omnium value.
		lc := strings.ToLower(c.Attribute + " " + c.Value)
		if strings.Contains(lc, "assurance") || strings.Contains(lc, "omnium") ||
			strings.Contains(lc, "cbc") || strings.Contains(lc, "loyer") {
			t.Errorf("GT-139-RR must decline insurance/loyer, but surfaced %q = %q", c.Attribute, c.Value)
		}
	}
	if !sawModel {
		t.Errorf("expected determined model Tesla Model X 2023, claims=%+v", veh.Claims)
	}
}

// A vehicle plate with no determined attribute declines (no facts surfaced) — no guess.
func TestAssembleQueryFacts_UnknownPlateDeclines(t *testing.T) {
	db, owner, now := seedVehicle(t)
	ctx := context.Background()

	facts, err := AssembleQueryFacts(ctx, db, "le modèle de mon véhicule ZZ-999-ZZ ?", now, owner, "Alex Martin")
	if err != nil {
		t.Fatalf("AssembleQueryFacts: %v", err)
	}
	for _, f := range facts {
		if f.Subject.Norm == "zz 999 zz" {
			t.Fatalf("unknown plate surfaced facts (should decline): %+v", f)
		}
	}
}

// A same-plate CONTESTED model that cannot be temporally ordered declines the attribute (fail-closed):
// GT-139-RR never affirms a guessed model when two undated docs disagree.
func TestKeyedResolve_ContestedUnorderableDeclines(t *testing.T) {
	nodes := []store.AttrNode{
		{ContentID: "a", KeyNorm: "gt 139 rr", Attribute: "modele", AttrRaw: "modèle", Value: "tesla model x", ValueRaw: "Tesla Model X"},
		{ContentID: "b", KeyNorm: "gt 139 rr", Attribute: "modele", AttrRaw: "modèle", Value: "tesla model 3", ValueRaw: "Tesla Model 3"},
	}
	if got := keyed.ResolveAttributes(nodes); len(got) != 0 {
		t.Fatalf("contested unorderable model should decline, got %+v", got)
	}
}
