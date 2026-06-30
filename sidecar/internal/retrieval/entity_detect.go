package retrieval

import (
	"context"
	"log"
	"strings"
	"unicode"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// querySubjectStopwords — generic FR/EN function words that must never be taken as the
// query's subject entity. The entity index already self-filters (a connective is not
// an entity, so it can't match), so this is only a secondary guard against a common
// word that happens to be indexed as an entity. Domain-agnostic by design: no domain
// terms here, only grammar. Built from a slice so duplicate keys can't break the build.
var querySubjectStopwords = func() map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(`le la les un une des de du au aux a ce cet cette ces et ou
ni mais donc or car que qui quoi dont si ne pas plus moins tres sur sous dans par pour avec sans
chez vers entre mon ma mes ton ta tes son sa ses notre nos votre vos leur leurs je tu il elle nous
vous ils elles on est sont etre ete avoir fait faire sais sait dois doit avais retenir propos sujet
concerne lie liee lies derniere dernier reunion quel quelle quels quelles
the an of to in at by for with and but is are was were what who which that this these those about
my your his her their our do does did know i we they me`) {
		m[w] = true
	}
	return m
}()

// detectQuerySubject finds the single most-anchoring known entity named in the query,
// deterministically: it generates 1–3 word n-grams, normalizes them like the entity
// index (contradict.NormKey), drops pure-stopword/too-short candidates, looks up which
// are real entities, and picks the most specific (longest n-gram) then most central
// (most mentions). Returns "" when no clear subject is named. Pure string + index
// match — NO LLM. The entity index is self-filtering: grammar can't match an entity.
// hasProperSignal reports whether s looks like a proper noun: it carries at least one
// uppercase letter or a digit. All-lowercase common nouns return false.
func hasProperSignal(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func detectQuerySubject(ctx context.Context, db *store.DB, query string) (string, error) {
	if db == nil || strings.TrimSpace(query) == "" {
		return "", nil
	}
	toks := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	best := make(map[string]int) // candidate norm -> max word count of the n-gram that produced it
	for n := 1; n <= 3 && n <= len(toks); n++ {
		for i := 0; i+n <= len(toks); i++ {
			window := toks[i : i+n]
			allStop := true
			for _, w := range window {
				if !querySubjectStopwords[contradict.NormKey(w)] {
					allStop = false
					break
				}
			}
			if allStop {
				continue
			}
			// Subject must be a proper noun (a named thing): keep only windows that
			// carry an uppercase letter or a digit; drop all-lowercase common nouns,
			// which are too generic and would over-trigger consolidation.
			if !hasProperSignal(strings.Join(window, " ")) {
				continue
			}
			norm := contradict.NormKey(strings.Join(window, " "))
			if len([]rune(norm)) < 3 {
				continue
			}
			if w, ok := best[norm]; !ok || n > w {
				best[norm] = n
			}
		}
	}
	if len(best) == 0 {
		return "", nil
	}
	cands := make([]string, 0, len(best))
	for norm := range best {
		cands = append(cands, norm)
	}
	matches, err := db.EntityNormsMatching(ctx, cands)
	if err != nil {
		return "", err
	}
	var chosen string
	var chosenWords, chosenCount int
	for norm, count := range matches {
		w := best[norm]
		if w > chosenWords || (w == chosenWords && count > chosenCount) {
			chosen, chosenWords, chosenCount = norm, w, count
		}
	}
	return chosen, nil
}

// applyEntityConsolidation (brick 2a) unions the subject entity's literal/claim-grounded
// items (EntitySearch — LIKE + entity index) into the semantic fusion results, so the
// FULL connected set surfaces: a query about a subject pulls everything that mentions it,
// not just the cosine-close chunks. Append-only (never drops a semantic hit). Order is a
// later refinement (chronology, brick 2c). Deterministic; no LLM.
func (us *UnifiedSearcher) applyEntityConsolidation(ctx context.Context, results *[]UnifiedResult, entity string) {
	if us.store == nil || strings.TrimSpace(entity) == "" {
		return
	}
	er, err := EntitySearch(ctx, us.store, &QueryIntent{Entity: entity},
		EntitySearchOptions{TopK: 40, UseEntityIndex: us.useEntityIndex})
	if err != nil || len(er) == 0 {
		return
	}
	have := make(map[string]bool, len(*results))
	for i := range *results {
		have[(*results)[i].ContentID] = true
	}
	added := 0
	for _, r := range er {
		if r.ContentID == "" || have[r.ContentID] {
			continue
		}
		have[r.ContentID] = true
		*results = append(*results, r)
		added++
	}
	if added > 0 {
		log.Printf("[entity-consolidation] +%d connected items for subject=%q", added, entity)
	}
}
