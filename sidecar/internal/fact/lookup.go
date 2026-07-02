// Package fact does the deterministic (entity, identifier-type) → value lookup: it ranks an
// entity's typed-identifier neighbors by a coherence score built from three deterministic
// signals — proximity (unambiguous name↔number pairing), NPMI (Hebbian association), and
// corroboration (how many documents carry the value) — and returns the best value with a
// CONFIDENCE TIER so the voice can affirm, hedge, or decline. No LLM in the ranking.
package fact

import (
	"context"
	"sort"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// Scoring weights (sum ≈ 1) and tier thresholds. Starting values — calibrate on real data
// (the plan flags θ as the one empirical knob). Proximity dominates: an unambiguous
// name↔number pairing is what breaks the family-member tie NPMI alone cannot.
const (
	wProx   = 0.5
	wNPMI   = 0.3
	wCorrob = 0.2

	thetaHigh = 0.7 // ≥ → affirm
	thetaLow  = 0.4 // ≥ → hedge; below → decline
)

// Tier is the confidence band that drives the voice's phrasing.
type Tier string

const (
	TierHigh Tier = "high"   // affirm
	TierMed  Tier = "medium" // hedge ("not certain, but…")
	TierNone Tier = "none"   // decline ("no reliable value")
)

// Reason codes explain WHY a lookup declined, so the voice can phrase the right question
// instead of a generic "I couldn't find it". Empty for confident results.
const (
	ReasonAmbiguousSubject = "ambiguous_subject" // the name matches several distinct people
	ReasonAmbiguousOwner   = "ambiguous_owner"   // the value is claimed by several distinct people
)

// Source is a document that carries the value — surfaced so a human can verify.
type Source struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
}

// Result is the outcome of a lookup.
type Result struct {
	Type       string   `json:"type"`
	Value      string   `json:"value"`      // canonical identifier value ("" if none)
	Raw        string   `json:"raw"`        // as written, for display
	Confidence float64  `json:"confidence"` // [0,1]
	Tier       Tier     `json:"tier"`
	Reason     string   `json:"reason,omitempty"` // why it declined (ambiguity code), when Tier=none
	Sources    []Source `json:"sources"`
	Candidates int      `json:"candidates"`
}

// Store is the slice of *store.DB this package needs (kept narrow for testability).
type Store interface {
	ResolvePersonNorms(ctx context.Context, query string, limit int) ([]string, error)
	HebbianNeighborsWeighted(ctx context.Context, norm string, now time.Time, minWeight float64, max int) ([]store.Neighbor, error)
	EntityDominantTypes(ctx context.Context, norms []string) (map[string]string, error)
	IdentifierLinksForID(ctx context.Context, idNorm string) ([]store.IdentifierLink, error)
	SearchByIdentifier(ctx context.Context, key string, limit int) ([]string, error)
	GetKnowledgeItem(ctx context.Context, contentID string) (*store.KnowledgeItem, error)
}

// LookupIdentifier returns the best typed-identifier value for (query, idType), scored
// deterministically. The query name is resolved to the graph's full-name person entities
// first; candidates are their typed-identifier neighbors of the requested type, pooled.
func LookupIdentifier(ctx context.Context, s Store, query, idType string, now time.Time) (Result, error) {
	res := Result{Type: idType, Tier: TierNone}
	attr := "id_" + idType

	// Resolve the name to the graph's person/org entities (full names). A query may resolve to
	// several norms that are really ONE person under name-variant reconstructions (reversed order,
	// a middle name, an OCR accent split): {petit,denis} and {denis,gérard,petit} are the
	// same person. store.DistinctPeople clusters by token-subset (a norm whose tokens ⊆ another's
	// is that person's variant) and counts the MAXIMAL norms = the number of distinct people. So a
	// person's own variants stay ONE subject (resolve), while a bare surname or first name shared
	// by genuinely distinct people (≥2 maximal, non-subset norms) stays ambiguous.
	resolved, _ := s.ResolvePersonNorms(ctx, query, 20)
	ambiguous := store.DistinctPeople(resolved) > 1
	// Pool the resolved norms plus the exact query.
	norms := append(resolved, query)

	type cinfo struct {
		weight float64
		prox   bool
	}
	cands := map[string]*cinfo{} // id_norm → pooled info across all resolved persons
	maxW := 0.0
	seenNorm := map[string]bool{}
	for _, pn := range norms {
		if pn == "" || seenNorm[pn] {
			continue
		}
		seenNorm[pn] = true
		neighbors, err := s.HebbianNeighborsWeighted(ctx, pn, now, 0, 50)
		if err != nil {
			return res, err
		}
		if len(neighbors) == 0 {
			continue
		}
		nn := make([]string, len(neighbors))
		for i, n := range neighbors {
			nn[i] = n.Norm
		}
		types, err := s.EntityDominantTypes(ctx, nn)
		if err != nil {
			return res, err
		}
		for _, n := range neighbors {
			if types[n.Norm] != attr {
				continue
			}
			ci := cands[n.Norm]
			if ci == nil {
				ci = &cinfo{}
				cands[n.Norm] = ci
			}
			if n.Weight > ci.weight {
				ci.weight = n.Weight
			}
			if n.Weight > maxW {
				maxW = n.Weight
			}
			if !ci.prox {
				if links, e := s.IdentifierLinksForID(ctx, n.Norm); e == nil {
					for _, l := range links {
						if l.PersonNorm == pn {
							ci.prox = true
							break
						}
					}
				}
			}
		}
	}
	res.Candidates = len(cands)

	// Flag 2 — collapse same-entity variants. When the query pooled >1 norm, decide whether they
	// are ONE entity or several. The exception applies ONLY to identifier types that one legal
	// entity can legitimately carry under several name variants — an ORG's enterprise number /
	// VAT (an org appears under many trade names, all sharing its number). A national number
	// identifies exactly one natural person and is NEVER shared, so a person pool is always
	// genuinely ambiguous → decline (this is the uniqueness invariant). For an org type we
	// collapse only when the variants converge on EXACTLY ONE proximity value that is itself
	// SHARED (proximity-linked to ≥2 name variants) — the signature of one entity, not two
	// distinct orgs each with their own number.
	if ambiguous {
		proxVal, proxCount := "", 0
		for norm, ci := range cands {
			if ci.prox {
				proxCount++
				proxVal = norm
			}
		}
		if !idTypeAllowsVariantCollapse(idType) || proxCount != 1 || ownerCount(ctx, s, proxVal) < 2 {
			res.Tier = TierNone
			res.Reason = ReasonAmbiguousSubject
			res.Candidates = len(resolved)
			return res, nil
		}
		// One shared, multi-owner org value → the variants are one entity; fall through and
		// resolve it (the shared value dominates the pooled scoring on its proximity weight).
	}

	if len(cands) == 0 {
		return res, nil
	}

	type scored struct {
		norm  string
		score float64
		prox  bool
		docs  []string
	}
	all := make([]scored, 0, len(cands))
	for idNorm, ci := range cands {
		prox := 0.0
		if ci.prox {
			prox = 1.0
		}
		npmiRel := 0.0
		if maxW > 0 {
			npmiRel = ci.weight / maxW
		}
		docs, _ := s.SearchByIdentifier(ctx, idNorm, 20)
		corrob := float64(len(docs)) / 3.0
		if corrob > 1 {
			corrob = 1
		}
		all = append(all, scored{idNorm, clamp01(wProx*prox + wNPMI*npmiRel + wCorrob*corrob), ci.prox, docs})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	best := all[0]

	// Uniqueness invariant: one value = one owner. If the best value is proximity-linked to
	// MORE THAN ONE distinct person, its ownership is contested — we cannot say it belongs to
	// the queried subject. Decline (fail closed), never assert or even hedge a contested value.
	// Skipped in the collapse case (ambiguous): there the several owners ARE the resolved
	// same-entity variants that share this one value, which we already accepted above.
	if !ambiguous && ownerCount(ctx, s, best.norm) > 1 {
		res.Tier = TierNone
		res.Reason = ReasonAmbiguousOwner
		res.Value, res.Raw, res.Confidence = best.norm, best.norm, best.score
		for _, id := range best.docs {
			if it, e := s.GetKnowledgeItem(ctx, id); e == nil && it != nil {
				res.Sources = append(res.Sources, Source{ContentID: id, Title: it.Title})
			}
		}
		return res, nil
	}

	// Tier. Proximity is a trustworthy name↔number pairing → affirm/hedge on the value.
	// WITHOUT proximity, doc-level NPMI CANNOT tell whose number it is once there are
	// competing candidates: a parent's number co-occurs with a child more than the child's
	// own does (the parent is on all the child's documents). So with multiple candidates and
	// no proximity we DECLINE, not hedge on a confident-sounding wrong guess. A lone
	// candidate (no competition) may still hedge. Sources are always returned for the human.
	switch {
	case best.prox && best.score >= thetaHigh:
		res.Tier = TierHigh
	case best.prox:
		res.Tier = TierMed
	case len(all) == 1 && best.score >= thetaLow:
		res.Tier = TierMed // single candidate, nothing to confuse it with → hedge
	default:
		res.Tier = TierNone // ambiguous (multi-candidate, no proximity) or weak → decline
	}

	res.Value, res.Raw, res.Confidence = best.norm, best.norm, best.score
	for _, id := range best.docs {
		if it, e := s.GetKnowledgeItem(ctx, id); e == nil && it != nil {
			res.Sources = append(res.Sources, Source{ContentID: id, Title: it.Title})
		}
	}
	return res, nil
}

// ownerCount is the number of DISTINCT persons proximity-linked to an identifier value —
// the read-time half of the uniqueness invariant. >1 means the value's ownership is
// contested across documents (the latent O2 case), so it must not be asserted for anyone.
// The owner NORMS are clustered by token-subset (store.DistinctPeople) so a person's own
// name-variant norms count as ONE owner (their number is not "contested" with themselves),
// while genuinely distinct people each with the value still count as ≥2 → contested.
func ownerCount(ctx context.Context, s Store, idNorm string) int {
	links, err := s.IdentifierLinksForID(ctx, idNorm)
	if err != nil {
		return 0
	}
	owners := map[string]bool{}
	for _, l := range links {
		if l.PersonNorm != "" {
			owners[l.PersonNorm] = true
		}
	}
	norms := make([]string, 0, len(owners))
	for n := range owners {
		norms = append(norms, n)
	}
	return store.DistinctPeople(norms)
}

// idTypeAllowsVariantCollapse reports whether an identifier type may legitimately be shared
// across several NAME VARIANTS of one entity. True for organisation identifiers (an enterprise
// number / VAT belongs to a legal entity that appears under many trade names); false for a
// national number, which identifies exactly one natural person and is never shared — so a query
// that pools several people stays ambiguous and declines.
func idTypeAllowsVariantCollapse(idType string) bool {
	switch idType {
	case "enterprise_number", "vat":
		return true
	}
	return false
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
