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

	// Resolve the name to the graph's person entities (full names). If the query resolves to
	// MORE THAN ONE distinct person, the subject itself is ambiguous (a bare surname or first
	// name shared by several people): we must NOT pool them and hand back one person's number
	// at high confidence. Decline and ask which one — the honest answer to "whose is this?".
	resolved, _ := s.ResolvePersonNorms(ctx, query, 20)
	if len(resolved) > 1 {
		res.Tier = TierNone
		res.Reason = ReasonAmbiguousSubject
		res.Candidates = len(resolved)
		return res, nil
	}
	// Mono (or unresolved) subject: proceed with the resolved person plus the exact query.
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
	if ownerCount(ctx, s, best.norm) > 1 {
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
	return len(owners)
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
