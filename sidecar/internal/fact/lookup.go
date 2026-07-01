// Package fact does the deterministic (entity, identifier-type) → value lookup: it ranks an
// entity's typed-identifier neighbors by a coherence score built from three deterministic
// signals — proximity (unambiguous name↔number pairing), NPMI (Hebbian association), and
// corroboration (how many documents carry the value) — and returns the best value with a
// CONFIDENCE TIER so the voice can affirm, hedge, or decline. No LLM in the ranking.
package fact

import (
	"context"
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
	Sources    []Source `json:"sources"`
	Candidates int      `json:"candidates"`
}

// Store is the slice of *store.DB this package needs (kept narrow for testability).
type Store interface {
	HebbianNeighborsWeighted(ctx context.Context, norm string, now time.Time, minWeight float64, max int) ([]store.Neighbor, error)
	EntityDominantTypes(ctx context.Context, norms []string) (map[string]string, error)
	IdentifierLinksForID(ctx context.Context, idNorm string) ([]store.IdentifierLink, error)
	SearchByIdentifier(ctx context.Context, key string, limit int) ([]string, error)
	GetKnowledgeItem(ctx context.Context, contentID string) (*store.KnowledgeItem, error)
}

// LookupIdentifier returns the best typed-identifier value for (personNorm, idType), scored
// deterministically. attribute is the entity_mentions tag ("id_" + idType).
func LookupIdentifier(ctx context.Context, s Store, personNorm, idType string, now time.Time) (Result, error) {
	res := Result{Type: idType, Tier: TierNone}
	attr := "id_" + idType

	neighbors, err := s.HebbianNeighborsWeighted(ctx, personNorm, now, 0, 50)
	if err != nil {
		return res, err
	}
	if len(neighbors) == 0 {
		return res, nil
	}
	norms := make([]string, len(neighbors))
	for i, n := range neighbors {
		norms[i] = n.Norm
	}
	types, err := s.EntityDominantTypes(ctx, norms)
	if err != nil {
		return res, err
	}

	// Candidates: the neighbors that are typed identifiers of the requested type.
	type cand struct {
		norm   string
		weight float64
	}
	var cands []cand
	maxW := 0.0
	for _, n := range neighbors {
		if types[n.Norm] != attr {
			continue
		}
		cands = append(cands, cand{n.Norm, n.Weight})
		if n.Weight > maxW {
			maxW = n.Weight
		}
	}
	res.Candidates = len(cands)
	if len(cands) == 0 {
		return res, nil
	}

	bestScore, bestNorm, bestDocs := -1.0, "", []string(nil)
	for _, c := range cands {
		// Proximity: is there an unambiguous (person, id) pairing in any document?
		proxCount := 0
		if links, e := s.IdentifierLinksForID(ctx, c.norm); e == nil {
			for _, l := range links {
				if l.PersonNorm == personNorm {
					proxCount++
				}
			}
		}
		prox := 0.0
		if proxCount > 0 {
			prox = 1.0
		}
		npmiRel := 0.0
		if maxW > 0 {
			npmiRel = c.weight / maxW
		}
		docs, _ := s.SearchByIdentifier(ctx, c.norm, 20)
		corrob := float64(len(docs)) / 3.0
		if corrob > 1 {
			corrob = 1
		}
		score := clamp01(wProx*prox + wNPMI*npmiRel + wCorrob*corrob)
		if score > bestScore {
			bestScore, bestNorm, bestDocs = score, c.norm, docs
		}
	}

	res.Value = bestNorm
	res.Raw = bestNorm
	res.Confidence = bestScore
	switch {
	case bestScore >= thetaHigh:
		res.Tier = TierHigh
	case bestScore >= thetaLow:
		res.Tier = TierMed
	default:
		res.Tier = TierNone
	}
	for _, id := range bestDocs {
		if it, e := s.GetKnowledgeItem(ctx, id); e == nil && it != nil {
			res.Sources = append(res.Sources, Source{ContentID: id, Title: it.Title})
		}
	}
	return res, nil
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
