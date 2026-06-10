package contradict

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func ccItem(id, title string, claims ...Claim) *store.KnowledgeItem {
	return &store.KnowledgeItem{ContentID: id, Title: title, Metadata: map[string]any{"extracted_claims": claims}}
}

func ccClaim(entity, attr, value string) Claim {
	return Claim{Entity: entity, Attribute: attr, Value: value, Quote: value, Polarity: "affirm"}
}

func TestDetectClaimConflicts_DivergentAcrossSources(t *testing.T) {
	items := []*store.KnowledgeItem{
		ccItem("m1", "Re: Devis projet X", ccClaim("solde à payer", "montant", "1000 €")),
		ccItem("m2", "RE: Devis projet X", ccClaim("Solde à payer", "Montant", "1200 €")),
	}
	got := DetectClaimConflicts(items, "")
	if len(got) != 1 {
		t.Fatalf("want 1 conflict, got %d: %+v", len(got), got)
	}
	if len(got[0].Members) != 2 {
		t.Errorf("want 2 cited members, got %d", len(got[0].Members))
	}
	if got[0].Cluster != "devis projet x" {
		t.Errorf("cluster = %q", got[0].Cluster)
	}
}

func TestDetectClaimConflicts_NoConflictWhenAgree(t *testing.T) {
	items := []*store.KnowledgeItem{
		ccItem("m1", "Facture", ccClaim("montant", "total", "500 €")),
		ccItem("m2", "RE: Facture", ccClaim("Montant", "Total", "500 €")),
	}
	if got := DetectClaimConflicts(items, ""); len(got) != 0 {
		t.Errorf("agreeing claims must not conflict: %+v", got)
	}
}

func TestDetectClaimConflicts_NeedsTwoSources(t *testing.T) {
	// One source asserting two values is an explanation, not a cross-source conflict.
	items := []*store.KnowledgeItem{
		ccItem("m1", "Devis", ccClaim("prix", "valeur", "100 €"), ccClaim("prix", "valeur", "200 €")),
	}
	if got := DetectClaimConflicts(items, ""); len(got) != 0 {
		t.Errorf("single source must not conflict: %+v", got)
	}
}

func TestDetectClaimConflicts_SeparateThreads(t *testing.T) {
	items := []*store.KnowledgeItem{
		ccItem("m1", "Projet A", ccClaim("deadline", "date", "1 mai")),
		ccItem("m2", "Projet B", ccClaim("deadline", "date", "2 mai")),
	}
	if got := DetectClaimConflicts(items, ""); len(got) != 0 {
		t.Errorf("different threads must not cluster: %+v", got)
	}
}

func TestDetectClaimConflicts_RecencyFilter(t *testing.T) {
	old1 := ccClaim("solde", "montant", "1000 €")
	old1.AssertedAt = "2024-09-12T08:00:00Z"
	old2 := ccClaim("solde", "montant", "1200 €")
	old2.AssertedAt = "2024-09-13T08:00:00Z"
	items := []*store.KnowledgeItem{
		ccItem("m1", "Devis X", old1),
		ccItem("m2", "RE: Devis X", old2),
	}
	// No cutoff → the (stale) divergence is still a candidate.
	if got := DetectClaimConflicts(items, ""); len(got) != 1 {
		t.Fatalf("no cutoff: want 1 candidate, got %d", len(got))
	}
	// A 2026 cutoff → both 2024 claims are stale → dropped → no candidate.
	if got := DetectClaimConflicts(items, "2026-01-01T00:00:00Z"); len(got) != 0 {
		t.Fatalf("recency cutoff should drop 2024 claims, got %+v", got)
	}
}

func TestDetectClaimConflicts_CanonicalDateBeatsIngestionStamp(t *testing.T) {
	// Claims carry a 2026 INGESTION stamp, but the messages are really from 2024
	// (re-synced old mail). The recency window must judge by the real date.
	c1 := ccClaim("solde", "montant", "1000 €")
	c1.AssertedAt = "2026-06-01T00:00:00Z"
	c2 := ccClaim("solde", "montant", "1200 €")
	c2.AssertedAt = "2026-06-01T00:00:00Z"
	it1 := ccItem("m1", "Devis X", c1)
	it1.Metadata["canonical_date"] = "2024-09-12T08:00:00Z"
	it2 := ccItem("m2", "RE: Devis X", c2)
	it2.Metadata["canonical_date"] = "2024-09-13T08:00:00Z"
	items := []*store.KnowledgeItem{it1, it2}

	// A 2026 cutoff must drop them by their REAL 2024 date, despite the 2026 stamp.
	if got := DetectClaimConflicts(items, "2026-01-01T00:00:00Z"); len(got) != 0 {
		t.Fatalf("canonical date should drive the cutoff, got %+v", got)
	}
	// No cutoff → still a candidate, and the cited date is the canonical 2024 one.
	got := DetectClaimConflicts(items, "")
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(got))
	}
	for _, m := range got[0].Members {
		if len(m.AssertedAt) < 4 || m.AssertedAt[:4] != "2024" {
			t.Errorf("cited date should be the canonical message date, got %q", m.AssertedAt)
		}
	}
}

func TestClaimsFromMetadata_JSONRoundTrip(t *testing.T) {
	// The shape metadata takes after a store JSON round-trip: []any of map[string]any.
	m := map[string]any{"extracted_claims": []any{
		map[string]any{"entity": "x", "attribute": "a", "value": "1", "quote": "q", "source_id": "s1"},
		map[string]any{"entity": "", "attribute": "a", "value": "2"}, // dropped: no entity
	}}
	got := claimsFromMetadata(m)
	if len(got) != 1 || got[0].Entity != "x" || got[0].SourceID != "s1" {
		t.Fatalf("round-trip parse = %+v", got)
	}
}
