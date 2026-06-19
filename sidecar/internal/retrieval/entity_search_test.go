package retrieval

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// insertItem is a small helper for entity_search tests.
func insertItem(t *testing.T, db *store.DB, id, title, body string, metadata map[string]any) {
	t.Helper()
	now := time.Now()
	ki := &store.KnowledgeItem{
		ContentID:      id,
		SourceType:     "markdown",
		Title:          title,
		NormalizedText: body,
		Metadata:       metadata,
		VersionID:      id + "-v1",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.InsertKnowledgeItem(context.Background(), ki); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

func TestEntitySearch_FilterByPerson(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Doc A — Jean appears in extracted_persons (Tier 2 NER): canonical match.
	insertItem(t, db, "doc-elric", "Infos administratives", "Coordonnées de l'équipe.",
		map[string]any{
			"extracted_persons": []string{"Jean Dupont"},
		})

	// Doc B — body mentions Elfcam (different entity), but NOT Jean. Should
	// not match because the SQL filter looks for "Jean" everywhere.
	insertItem(t, db, "doc-elfcam", "Newsletter Elfcam", "Promo Elfcam ce mois-ci.", nil)

	// Doc C — Jean mentioned only in body (no NER list). Should match but
	// score lower than Doc A.
	insertItem(t, db, "doc-elric-body", "Notes diverses", "Réunion avec Jean hier.", nil)

	intent := &QueryIntent{
		Category:  IntentFactualEntity,
		Entity:    "Jean",
		Attribute: "person",
	}
	results, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].ContentID != "doc-elric" {
		t.Errorf("top result = %q, want doc-elric (Tier 2 NER hit should outrank body match)", results[0].ContentID)
	}
	for _, r := range results {
		if r.ContentID == "doc-elfcam" {
			t.Errorf("doc-elfcam should not be returned (no Jean anywhere)")
		}
	}
}

func TestEntitySearch_FilterByProject(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Doc A — Hygur is in extracted_projects.
	insertItem(t, db, "doc-hygur-project", "Brief produit", "Plan du trimestre.",
		map[string]any{
			"extracted_projects": []string{"Hygur"},
		})

	// Doc B — Hygur only in body, no NER list.
	insertItem(t, db, "doc-hygur-body", "Notes", "On a parlé de Hygur en réunion.", nil)

	// Doc C — unrelated.
	insertItem(t, db, "doc-other", "Random", "Lorem ipsum dolor.", nil)

	intent := &QueryIntent{
		Category:  IntentFactualEntity,
		Entity:    "Hygur",
		Attribute: "project",
	}
	results, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 hits, got %d", len(results))
	}
	if results[0].ContentID != "doc-hygur-project" {
		t.Errorf("top result = %q, want doc-hygur-project (project NER list should outrank body)", results[0].ContentID)
	}
	for _, r := range results {
		if r.ContentID == "doc-other" {
			t.Errorf("doc-other should not be returned")
		}
	}
}

func TestEntitySearch_AttributeBoostFromExtractedKey(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Doc A — Stripe in body, has extracted_amounts (the requested attribute).
	insertItem(t, db, "doc-with-amount", "Facture Stripe", "Paiement Stripe reçu.",
		map[string]any{
			"extracted_amounts": []string{"42.00 EUR"},
		})

	// Doc B — Stripe in body, no extracted_amounts.
	insertItem(t, db, "doc-without-amount", "Mention Stripe", "On utilise Stripe.", nil)

	intent := &QueryIntent{
		Category:  IntentFactualEntity,
		Entity:    "Stripe",
		Attribute: "amount",
	}
	results, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(results))
	}
	if results[0].ContentID != "doc-with-amount" {
		t.Errorf("top = %q, want doc-with-amount (×1.3 attribute boost)", results[0].ContentID)
	}
}

func TestEntitySearch_EmptyEntityReturnsNil(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	results, err := EntitySearch(context.Background(), db, &QueryIntent{Entity: "  "}, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty entity, got %d", len(results))
	}
}

// TestEntitySearch_FocusScope_FiltersOut — the AllowedContentIDs filter must
// remove out-of-scope docs from the SQL result set even when they match the
// entity perfectly.
func TestEntitySearch_FocusScope_FiltersOut(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Both docs match the entity ("Stripe"), but only doc-in-scope is in the
	// allow-list passed to EntitySearchOptions.
	insertItem(t, db, "doc-in-scope", "Facture Stripe", "Paiement Stripe reçu.",
		map[string]any{"extracted_orgs": []string{"Stripe"}})
	insertItem(t, db, "doc-out-of-scope", "Autre Stripe", "On utilise Stripe ici aussi.",
		map[string]any{"extracted_orgs": []string{"Stripe"}})

	intent := &QueryIntent{
		Category:  IntentFactualEntity,
		Entity:    "Stripe",
		Attribute: "organization",
	}

	// Sanity: without the filter both should match.
	allHits, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("EntitySearch (no filter): %v", err)
	}
	if len(allHits) != 2 {
		t.Fatalf("expected 2 unfiltered hits, got %d", len(allHits))
	}

	// With the filter, only doc-in-scope survives.
	results, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{
		AllowedContentIDs: []string{"doc-in-scope"},
	})
	if err != nil {
		t.Fatalf("EntitySearch (filtered): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 in-scope hit, got %d", len(results))
	}
	if results[0].ContentID != "doc-in-scope" {
		t.Errorf("got %q, want doc-in-scope", results[0].ContentID)
	}
}

// TestEntitySearch_EntityIndexUnion_RecallsClaimGroundedItem — an item whose
// title/body never mention the entity, but whose cached claims do (entity_mentions),
// is recalled ONLY when the entity lens is on. Proves the associative recall and
// that it's gated by the flag (no regression when off).
func TestEntitySearch_EntityIndexUnion_RecallsClaimGroundedItem(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	// "Acme" appears nowhere in the text — only in the item's claims.
	insertItem(t, db, "doc-claimed", "Facture mensuelle", "Paiement reçu pour la prestation.", nil)
	if err := db.ReplaceEntityMentions(ctx, "doc-claimed", []store.EntityMention{
		{EntityNorm: "acme", EntityRaw: "Acme SARL", Attribute: "vendor", AssertedAt: "2026-06-01T00:00:00Z"},
	}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	intent := &QueryIntent{Category: IntentFactualEntity, Entity: "Acme", Attribute: "organization"}

	off, err := EntitySearch(ctx, db, intent, EntitySearchOptions{})
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	for _, r := range off {
		if r.ContentID == "doc-claimed" {
			t.Fatalf("doc-claimed must NOT surface with the lens off (surface match misses it)")
		}
	}

	on, err := EntitySearch(ctx, db, intent, EntitySearchOptions{UseEntityIndex: true})
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	found := false
	for _, r := range on {
		if r.ContentID == "doc-claimed" {
			found = true
		}
	}
	if !found {
		t.Errorf("doc-claimed must be recalled by the entity index when the lens is on")
	}
}

// TestEntitySearch_NoRegression_VATAmount — the ×1.3 attribute boost still selects
// the amount-bearing doc as top result with the entity lens ON, even when the OTHER
// doc is claim-grounded for the same entity. The union must not displace existing
// factual lookups ("dernière facture TVA avec son montant").
func TestEntitySearch_NoRegression_VATAmount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertItem(t, db, "doc-with-amount", "Facture Stripe", "Paiement Stripe reçu, TVA incluse.",
		map[string]any{"extracted_amounts": []string{"42.00 EUR"}})
	insertItem(t, db, "doc-without-amount", "Mention Stripe", "On utilise Stripe.", nil)

	// Adversarial: the NON-amount doc is the one carrying a claim about Stripe.
	if err := db.ReplaceEntityMentions(ctx, "doc-without-amount", []store.EntityMention{
		{EntityNorm: "stripe", EntityRaw: "Stripe", Attribute: "mention"},
	}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	intent := &QueryIntent{Category: IntentFactualEntity, Entity: "Stripe", Attribute: "amount"}
	results, err := EntitySearch(ctx, db, intent, EntitySearchOptions{UseEntityIndex: true})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(results))
	}
	if results[0].ContentID != "doc-with-amount" {
		t.Errorf("top = %q, want doc-with-amount (amount boost must survive the entity lens)", results[0].ContentID)
	}
}

// TestEntitySearch_NoRegression_NationalNumber — same guard for the
// national_number attribute (mapped to extracted_phones): the doc carrying the
// number stays on top with the entity lens on.
func TestEntitySearch_NoRegression_NationalNumber(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertItem(t, db, "doc-with-number", "Dossier Jean", "Coordonnées de Jean.",
		map[string]any{"extracted_phones": []string{"85.07.30-123.45"}})
	insertItem(t, db, "doc-without-number", "Note Jean", "Réunion avec Jean.", nil)

	intent := &QueryIntent{Category: IntentFactualEntity, Entity: "Jean", Attribute: "national_number"}
	results, err := EntitySearch(ctx, db, intent, EntitySearchOptions{UseEntityIndex: true})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(results))
	}
	if results[0].ContentID != "doc-with-number" {
		t.Errorf("top = %q, want doc-with-number (national_number boost must survive the lens)", results[0].ContentID)
	}
}

// TestEntitySearch_SynonymyExpansion_RecallsViaProvidedNorms — brick 2 plumbing:
// an item whose claims mention a SYNONYM of the queried entity (never the entity
// itself, in text or claims) is recalled only when that synonym norm is folded in
// via EntityNorms (as the embedding expansion does). The cosine→norms step itself
// is unit-tested in store; here we prove the lookup honors the expanded set.
func TestEntitySearch_SynonymyExpansion_RecallsViaProvidedNorms(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	ctx := context.Background()

	insertItem(t, db, "doc-prestataire", "Note interne", "Le prestataire a livré le matériel.", nil)
	if err := db.ReplaceEntityMentions(ctx, "doc-prestataire", []store.EntityMention{
		{EntityNorm: "prestataire", EntityRaw: "le prestataire", Attribute: "vendor"},
	}); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	intent := &QueryIntent{Category: IntentFactualEntity, Entity: "Acme", Attribute: "organization"}

	// Exact-norm lookup ("acme") misses it — neither text nor claims say "Acme".
	base, err := EntitySearch(ctx, db, intent, EntitySearchOptions{UseEntityIndex: true})
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	for _, r := range base {
		if r.ContentID == "doc-prestataire" {
			t.Fatalf("doc-prestataire must NOT surface without the synonym norm")
		}
	}

	// With the synonym norm injected (as brick 2 would after cosine expansion).
	withSyn, err := EntitySearch(ctx, db, intent, EntitySearchOptions{
		UseEntityIndex: true,
		EntityNorms:    []string{"prestataire"},
	})
	if err != nil {
		t.Fatalf("withSyn: %v", err)
	}
	found := false
	for _, r := range withSyn {
		if r.ContentID == "doc-prestataire" {
			found = true
		}
	}
	if !found {
		t.Errorf("the synonym norm should recall doc-prestataire")
	}
}

// TestEntitySearch_FocusScope_EmptyAllowListReturnsNil — an explicit empty
// allow-list (scope set but resolved zero docs) is a hard abstention, not a
// missing filter.
func TestEntitySearch_FocusScope_EmptyAllowListReturnsNil(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-stripe", "Facture Stripe", "Paiement Stripe reçu.", nil)

	intent := &QueryIntent{
		Category:  IntentFactualEntity,
		Entity:    "Stripe",
		Attribute: "organization",
	}
	results, err := EntitySearch(context.Background(), db, intent, EntitySearchOptions{
		AllowedContentIDs: []string{}, // explicit empty
	})
	if err != nil {
		t.Fatalf("EntitySearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results with empty allow-list, got %d", len(results))
	}
}
