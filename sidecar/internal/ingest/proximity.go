package ingest

import (
	"strings"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/recognize"
	"github.com/hygur/sidecar/internal/store"
)

// proxWindow bounds how far (chars) a person mention may be from an identifier to be its
// owner at all.
const proxWindow = 300

// identifierProximityLinks emits a (person ↔ typed identifier) link for a document only when
// the pairing is unambiguous there. It locates each person by their RAREST name token (a
// distinctive first name like "elric", not a shared surname the NER's reconstructed full
// name may not even match verbatim in the OCR), assigns every identifier to its nearest
// person within the window, and emits a link only when a person is the unique claimant of a
// single same-type identifier. This fires for a family member whose number sits on their own
// row and correctly declines when one name is flanked by two numbers of the same type.
// Deterministic.
func identifierProximityLinks(item *store.KnowledgeItem) []store.IdentifierLink {
	if item == nil {
		return nil
	}
	text := item.Title + " " + item.NormalizedText
	typed := recognize.Recognize(text)
	if len(typed) == 0 {
		return nil
	}
	lower := strings.ToLower(text)

	// Locate each person by their rarest (most distinctive) name token.
	type person struct {
		norm string
		pos  []int
	}
	var people []person
	for _, raw := range metaStrings(item.Metadata, "extracted_persons") {
		norm := contradict.NormKey(raw)
		if norm == "" {
			continue
		}
		rare, rareCount := "", 1<<30
		for _, tok := range strings.Fields(strings.ToLower(raw)) {
			if len([]rune(tok)) < 3 {
				continue
			}
			if c := strings.Count(lower, tok); c > 0 && c < rareCount {
				rare, rareCount = tok, c
			}
		}
		if rare == "" {
			continue
		}
		var pos []int
		for i := 0; ; {
			j := strings.Index(lower[i:], rare)
			if j < 0 {
				break
			}
			pos = append(pos, i+j)
			i += j + len(rare)
		}
		if len(pos) > 0 {
			people = append(people, person{norm, pos})
		}
	}
	if len(people) == 0 {
		return nil
	}

	// Assign each identifier to its nearest person (within the window); collect the distinct
	// identifier values claimed per (type, person).
	claims := map[string]map[string]bool{} // "type\x1fperson" -> set of id values
	for _, t := range typed {
		idPos := (t.Start + t.End) / 2
		bestNorm, bestD := "", 1<<30
		for _, p := range people {
			for _, pp := range p.pos {
				if d := abs(pp - idPos); d < bestD {
					bestD, bestNorm = d, p.norm
				}
			}
		}
		if bestNorm == "" || bestD > proxWindow {
			continue
		}
		key := t.Type + "\x1f" + bestNorm
		if claims[key] == nil {
			claims[key] = map[string]bool{}
		}
		claims[key][t.Value] = true
	}

	// Emit only unique claims: a person who is the sole nearest to exactly one same-type value.
	var out []store.IdentifierLink
	for key, vals := range claims {
		if len(vals) != 1 {
			continue // 0 or ≥2 → ambiguous
		}
		typ, pnorm, _ := strings.Cut(key, "\x1f")
		for v := range vals {
			out = append(out, store.IdentifierLink{PersonNorm: pnorm, IDNorm: v, IDType: typ, Prox: 1.0})
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
