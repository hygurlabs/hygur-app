package session

import (
	"github.com/hygur/sidecar/internal/extract"
)

// ExtractEntities runs the same Tier 1 regex pipeline used at indexing time
// against arbitrary text (typically a search-result excerpt or an assistant
// answer) and returns Entity records ready to merge into a SessionContext.
//
// Source carries provenance — the content_id when extraction is from a mail
// excerpt, or "" when extraction is from the assistant's natural-language
// answer (where we can't attribute to a single source).
func ExtractEntities(text, source string) []Entity {
	t1 := extract.ExtractTier1(text)
	out := make([]Entity, 0, t1.Count())

	for _, v := range t1.IBANs {
		out = append(out, Entity{Type: EntityIBAN, Value: v, Source: source})
	}
	for _, a := range t1.Amounts {
		out = append(out, Entity{Type: EntityAmount, Value: a.Value + " " + a.Currency, Source: source})
	}
	for _, c := range t1.StructuredCommunications {
		out = append(out, Entity{Type: EntityStructuredCom, Value: c, Source: source})
	}
	for _, v := range t1.VATNumbers {
		out = append(out, Entity{Type: EntityVATNumber, Value: v, Source: source})
	}
	return out
}
