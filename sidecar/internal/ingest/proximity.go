package ingest

import (
	"strings"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/recognize"
	"github.com/hygur/sidecar/internal/store"
)

const (
	// proxWindow bounds how far (chars) a person mention may be from an identifier to count
	// as proximate at all.
	proxWindow = 300
	// proxMarginRatio is the "clear winner" margin: the runner-up same-type identifier near
	// the person must be at least this many times farther than the winner.
	proxMarginRatio = 2
)

// identifierProximityLinks emits a (person ↔ typed identifier) link for a document ONLY when
// the pairing is unambiguous there: the identifier is the same-type nearest to that person,
// the person is the identifier's nearest, and the runner-up same-type identifier is clearly
// farther. Otherwise nothing (the lookup falls back to NPMI). Deterministic — the guard that
// makes proximity trustworthy for the multi-person case (each family member's number sits on
// their own row).
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

	// Person occurrences: each extracted_persons name located in the text.
	type occ struct {
		norm string
		pos  int
	}
	var people []occ
	for _, raw := range metaStrings(item.Metadata, "extracted_persons") {
		norm := contradict.NormKey(raw)
		needle := strings.ToLower(strings.TrimSpace(raw))
		if norm == "" || len([]rune(needle)) < 3 {
			continue
		}
		for i := 0; ; {
			j := strings.Index(lower[i:], needle)
			if j < 0 {
				break
			}
			people = append(people, occ{norm, i + j})
			i += j + len(needle)
		}
	}
	if len(people) == 0 {
		return nil
	}

	mid := func(t recognize.Typed) int { return (t.Start + t.End) / 2 }
	var out []store.IdentifierLink
	seen := map[string]bool{}
	for _, t := range typed {
		idPos := mid(t)
		// The person occurrence nearest to this identifier.
		pNorm, pPos, pd := "", 0, 1<<30
		for _, p := range people {
			if d := abs(p.pos - idPos); d < pd {
				pd, pNorm, pPos = d, p.norm, p.pos
			}
		}
		if pNorm == "" || pd > proxWindow {
			continue
		}
		// Among same-type identifiers, the nearest + runner-up to that person occurrence.
		n1, n2 := 1<<30, 1<<30
		nearestVal := ""
		for _, u := range typed {
			if u.Type != t.Type {
				continue
			}
			d := abs(mid(u) - pPos)
			if d < n1 {
				n2, n1, nearestVal = n1, d, u.Value
			} else if d < n2 {
				n2 = d
			}
		}
		// Guard: this identifier is the same-type nearest, and any runner-up is clearly farther.
		if nearestVal != t.Value || n2 < proxMarginRatio*max(n1, 1) {
			continue
		}
		if key := pNorm + "\x1f" + t.Value; !seen[key] {
			seen[key] = true
			out = append(out, store.IdentifierLink{PersonNorm: pNorm, IDNorm: t.Value, IDType: t.Type, Prox: 1.0})
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
