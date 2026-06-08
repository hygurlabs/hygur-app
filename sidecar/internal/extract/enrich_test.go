package extract

import "testing"

func TestEnrichMetadataWithTier1_PopulatesAllExtractedKeys(t *testing.T) {
	text := `Cher Client,
Montant : 7 421,85 €
IBAN : BE68 5390 0754 7034
Communication : +++090/9337/55493+++
TVA: BE0123456789
Téléphone : +32 2 123 45 67
À payer avant le 25 avril 2026.
https://example.test`

	metadata := map[string]any{}
	got := EnrichMetadataWithTier1(metadata, text)

	if got.Count() == 0 {
		t.Fatalf("expected entities, got Count=0: %+v", got)
	}

	for _, key := range []string{
		"extracted_iban",
		"extracted_amounts",
		"extracted_structured_comm",
		"extracted_vat_numbers",
		"extracted_due_dates",
		"extracted_urls",
	} {
		if _, ok := metadata[key]; !ok {
			t.Errorf("expected metadata[%q] to be set", key)
		}
	}
}

func TestEnrichMetadataWithTier1_EmptyTextLeavesMetadataUntouched(t *testing.T) {
	metadata := map[string]any{"existing": "value"}
	EnrichMetadataWithTier1(metadata, "Just a plain text with no entities.")

	if metadata["existing"] != "value" {
		t.Errorf("existing key should be preserved, got %v", metadata["existing"])
	}

	for _, key := range []string{
		"extracted_iban",
		"extracted_amounts",
		"extracted_structured_comm",
		"extracted_vat_numbers",
	} {
		if _, ok := metadata[key]; ok {
			t.Errorf("metadata[%q] should not be set for entity-free text", key)
		}
	}
}

func TestEnrichMetadataWithTier1_AmountsFormattedAsValueCurrency(t *testing.T) {
	metadata := map[string]any{}
	EnrichMetadataWithTier1(metadata, "Total : 1234,56 €")

	amounts, ok := metadata["extracted_amounts"].([]string)
	if !ok {
		t.Fatalf("extracted_amounts should be []string, got %T", metadata["extracted_amounts"])
	}
	if len(amounts) != 1 || amounts[0] != "1234.56 EUR" {
		t.Errorf("got amounts %v, want [\"1234.56 EUR\"]", amounts)
	}
}

func TestMergeTier1IntoMetadata_PurgesStaleKeysOnReextract(t *testing.T) {
	// A re-extract that no longer finds an entity must DELETE the stale value
	// (the regression behind the "365138779 EUR" false amount lingering after
	// the regex fix). Here a prior amount disappears while the IBAN persists.
	metadata := map[string]any{
		"extracted_amounts": []string{"365138779 EUR"},
		"extracted_iban":    []string{"BE68539007547034"},
		"some_other_key":    "keep me",
	}
	MergeTier1IntoMetadata(metadata, Tier1Entities{IBANs: []string{"BE68539007547034"}})

	if _, ok := metadata["extracted_amounts"]; ok {
		t.Errorf("stale extracted_amounts should be deleted, got %v", metadata["extracted_amounts"])
	}
	if _, ok := metadata["extracted_iban"]; !ok {
		t.Error("extracted_iban should still be set")
	}
	if metadata["some_other_key"] != "keep me" {
		t.Error("non-extracted keys must be preserved")
	}
}

func TestMergeTier1IntoMetadata_SkipsEmptyFields(t *testing.T) {
	tier1 := Tier1Entities{
		IBANs: []string{"BE68539007547034"},
		// All other fields empty
	}
	metadata := map[string]any{}
	MergeTier1IntoMetadata(metadata, tier1)

	if _, ok := metadata["extracted_iban"]; !ok {
		t.Error("extracted_iban should be set")
	}
	for _, key := range []string{
		"extracted_amounts",
		"extracted_structured_comm",
		"extracted_vat_numbers",
		"extracted_phones",
		"extracted_urls",
		"extracted_due_dates",
	} {
		if _, ok := metadata[key]; ok {
			t.Errorf("metadata[%q] should not be set for empty input", key)
		}
	}
}
