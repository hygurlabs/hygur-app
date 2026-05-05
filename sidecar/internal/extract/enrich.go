package extract

// EnrichMetadataWithTier1 runs Tier 1 extraction on `text` and writes the
// resulting structured entities into `metadata` under the conventional keys
// `extracted_*` consumed by retrieval/entity_search.go. Mutates metadata in
// place. Empty result lists are not written, so callers can rely on key
// presence to test whether an entity type was found.
//
// This function is body-only and source-agnostic: it is safe to call on any
// document type (markdown, txt, pdf, docx, email body, note). Source-specific
// enrichment (e.g. accounting-domain detection from the email sender) is the
// caller's responsibility.
func EnrichMetadataWithTier1(metadata map[string]any, text string) Tier1Entities {
	tier1 := ExtractTier1(text)
	MergeTier1IntoMetadata(metadata, tier1)
	return tier1
}

// MergeTier1IntoMetadata writes a precomputed Tier1Entities into the metadata
// map under the conventional `extracted_*` keys. Useful when callers already
// have a Tier1Entities (e.g. mail indexer pre-computed it for downstream use)
// and want to avoid re-running ExtractTier1.
func MergeTier1IntoMetadata(metadata map[string]any, tier1 Tier1Entities) {
	if len(tier1.IBANs) > 0 {
		metadata["extracted_iban"] = tier1.IBANs
	}
	if len(tier1.Amounts) > 0 {
		amounts := make([]string, len(tier1.Amounts))
		for i, a := range tier1.Amounts {
			amounts[i] = a.Value + " " + a.Currency
		}
		metadata["extracted_amounts"] = amounts
	}
	if len(tier1.StructuredCommunications) > 0 {
		metadata["extracted_structured_comm"] = tier1.StructuredCommunications
	}
	if len(tier1.VATNumbers) > 0 {
		metadata["extracted_vat_numbers"] = tier1.VATNumbers
	}
	if len(tier1.PhoneNumbers) > 0 {
		metadata["extracted_phones"] = tier1.PhoneNumbers
	}
	if len(tier1.URLs) > 0 {
		metadata["extracted_urls"] = tier1.URLs
	}
	if len(tier1.DueDates) > 0 {
		metadata["extracted_due_dates"] = tier1.DueDates
	}
}
