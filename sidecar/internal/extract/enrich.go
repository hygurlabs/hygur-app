package extract

// EnrichMetadataWithTier1 runs Tier 1 extraction on `text` and writes the
// resulting structured entities into `metadata` under the conventional keys
// `extracted_*` consumed by retrieval/entity_search.go. Mutates metadata in
// place. A key is present iff the entity type was found this run (empty results
// delete any prior value), so re-extraction always reflects the current rules.
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
//
// Authoritative: each key is set when its list is non-empty and DELETED when
// empty, so the metadata always reflects the current extraction (key present
// iff found). This makes re-extraction purge values the extractor no longer
// produces — e.g. a reference number previously mis-parsed as an amount.
func MergeTier1IntoMetadata(metadata map[string]any, tier1 Tier1Entities) {
	setOrDelete := func(key string, nonEmpty bool, value any) {
		if nonEmpty {
			metadata[key] = value
		} else {
			delete(metadata, key)
		}
	}

	amounts := make([]string, len(tier1.Amounts))
	for i, a := range tier1.Amounts {
		amounts[i] = a.Value + " " + a.Currency
	}

	setOrDelete("extracted_iban", len(tier1.IBANs) > 0, tier1.IBANs)
	setOrDelete("extracted_amounts", len(tier1.Amounts) > 0, amounts)
	setOrDelete("extracted_structured_comm", len(tier1.StructuredCommunications) > 0, tier1.StructuredCommunications)
	setOrDelete("extracted_vat_numbers", len(tier1.VATNumbers) > 0, tier1.VATNumbers)
	setOrDelete("extracted_phones", len(tier1.PhoneNumbers) > 0, tier1.PhoneNumbers)
	setOrDelete("extracted_urls", len(tier1.URLs) > 0, tier1.URLs)
	setOrDelete("extracted_due_dates", len(tier1.DueDates) > 0, tier1.DueDates)
}
