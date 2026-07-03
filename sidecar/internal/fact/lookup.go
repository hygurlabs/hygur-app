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

	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/recognize"
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
	// ReasonUncorroborated: the winning value competes with other candidate values of the same
	// type for this subject yet is backed by a single document — the false-positive signature of
	// a coincidental checksum match (a tax rôle, an order/client reference that happens to pass
	// the type's checksum), not the subject's real number. Decline rather than pick it.
	ReasonUncorroborated = "uncorroborated_candidate"
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
	IdentifierValuesForPersonsOfType(ctx context.Context, norms []string, idType string) ([]string, error)
	SearchByIdentifier(ctx context.Context, key string, limit int) ([]string, error)
	GetKnowledgeItem(ctx context.Context, contentID string) (*store.KnowledgeItem, error)
	NationalNumbersByPersons(ctx context.Context, norms []string) (map[string][]string, error)
	PersonNormsContainingTokens(ctx context.Context, tokens []string) ([]string, error)
}

// LookupIdentifier returns the best typed-identifier value for (query, idType), scored
// deterministically. The query name is resolved to the graph's full-name person entities
// first; candidates are their typed-identifier neighbors of the requested type, pooled.
// owner (may be nil) is the first-class owner matcher: it collapses the owner's name variants
// into ONE person for the ambiguity/owner counts, pools ALL his variants so a query to any of
// them finds an identifier linked to a different variant (surname-first vs given-first), and —
// combined with dominant-owner plurality — affirms the owner's OWN reference number even when
// institutions reprint it across his correspondence.
func LookupIdentifier(ctx context.Context, s Store, query, idType string, now time.Time, owner *identity.Matcher) (Result, error) {
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

	// Is the queried subject the owner? (query itself, or any norm it resolved to).
	queryIsOwner := owner.IsOwnerNorm(query)
	for _, r := range resolved {
		if owner.IsOwnerNorm(r) {
			queryIsOwner = true
			break
		}
	}

	// Ambiguity is judged over the RESOLVED set only, with the owner's variants collapsed to one
	// person: a query that resolves to the founder's many spellings ("denis l", "petit denis",
	// "denis gérard petit" — not mutually token-subset) is ONE person, not several.
	ambiguous := distinctPeopleOwnerAware(ctx, s, resolved, owner) > 1

	// Pool the resolved norms plus the exact query.
	norms := append(resolved, query)
	// Owner anchor — pool EVERY owner variant norm present in the graph, so a query to a variant
	// that carries no id link (e.g. given-first "denis petit") still finds his number, linked
	// to another variant (surname-first "petit denis"). Gated to owner queries; the strict
	// matcher keeps family members out of the pool.
	if queryIsOwner {
		if cands, e := s.PersonNormsContainingTokens(ctx, owner.Tokens()); e == nil {
			for _, c := range cands {
				if owner.IsOwnerNorm(c) {
					norms = append(norms, c)
				}
			}
		}
	}

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
						// Proximity to the queried subject: an exact link to the pooled norm, OR —
						// for an owner query — a link to ANY owner name-variant, since the owner is
						// ONE unified subject. This is what lets a value that neighbors one variant
						// (given-first) but is proximity-linked under another (surname-first) still
						// count as the owner's own, proximity-confident pairing.
						if l.PersonNorm == pn || (queryIsOwner && owner.IsOwnerNorm(l.PersonNorm)) {
							ci.prox = true
							break
						}
					}
				}
			}
		}
	}

	// Recall floor. Candidate enumeration above walks the subject's TOP-K Hebbian neighbors, so a
	// rare, single-document identifier (e.g. a DUNS printed once, in one mail) is crowded out of the
	// neighbor list for a highly-connected subject (the owner has hundreds of neighbors) and would
	// never be scored — a silent miss that grows with the subject's centrality. But a proximity link
	// is the STRONGEST ownership signal we have; a value proximity-linked to a pooled subject norm is
	// authoritative and must be a candidate regardless of neighbor rank. Seed those directly (precise,
	// uncapped). This only ADDS candidates that are already proximity-confident — every downstream
	// gate (ambiguity, uniqueness/ownerCount, owner-dominance) is unchanged and still fails closed.
	if seed, e := s.IdentifierValuesForPersonsOfType(ctx, norms, idType); e == nil {
		for _, v := range seed {
			if v == "" {
				continue
			}
			ci := cands[v]
			if ci == nil {
				ci = &cinfo{}
				cands[v] = ci
			}
			ci.prox = true // linked to a pooled subject norm by construction of the query
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
		if !idTypeAllowsVariantCollapse(idType) || proxCount != 1 || ownerCount(ctx, s, proxVal, owner) < 2 {
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
	//
	// OWNER ANCHOR + DOMINANCE exception: the founder's own reference number is reprinted by
	// institutions as their "client ref", so it looks contested (owner + a scatter of
	// institutional contacts). When the queried subject IS the owner AND the owner is the
	// DECISIVE-plurality holder of the value across his own correspondence (≥3 docs and ≥2× the
	// runner-up), resolve to him — his own number is not truly shared. This is NOT an
	// attribute-to-owner-by-default: a non-dominant contested value still declines, and a
	// non-owner query never gets an owner's number.
	if !ambiguous && ownerCount(ctx, s, best.norm, owner) > 1 {
		if !(queryIsOwner && ownerIsDominant(ctx, s, best.norm, owner)) {
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
	}

	// Competing-candidates corroboration guard (fail-closed). When the subject carries MORE THAN
	// ONE distinct candidate value of this single-value identifier type, the winner must be
	// CORROBORATED to be trusted: a value backed by a single document that merely sits near the
	// subject in that one document is the false-positive signature of a coincidental checksum
	// match (a property-tax rôle, an order or client reference that happens to pass the type's
	// checksum), NOT the subject's real number. Require ≥2 corroborating documents — or, for an
	// owner query, owner-dominance across his correspondence — to keep the value; otherwise decline
	// honestly rather than affirm a poorly-supported one. A LONE candidate (nothing to confuse it
	// with) is untouched, so the legitimate single-source case still resolves. This only ADDS a
	// gate — it never affirms anything the prior code declined, and it leaves the checksum, owner
	// and dominance gates intact.
	if len(all) > 1 && len(best.docs) < 2 && !(queryIsOwner && ownerIsDominant(ctx, s, best.norm, owner)) {
		res.Tier = TierNone
		res.Reason = ReasonUncorroborated
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

	// Family split: a FAMILY-B label-derived type (id_duns, id_siret…) has no intrinsic checksum
	// proof — its confidence comes only from the label binding + proximity + corroboration — so it
	// can never be affirmed HIGH, only hedged. Family A (checksum types) is unaffected. idType is
	// already canonical here (callers normalize via labelfact.NormalizeLabel), so VAT synonyms have
	// resolved to enterprise_number and stay high.
	if res.Tier == TierHigh && !recognize.IsChecksumType(idType) {
		res.Tier = TierMed
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
// The owner NORMS are clustered by token-subset (store.DistinctPeople, with the father/son
// NISS guard) so a person's own name-variant norms count as ONE owner, while genuinely
// distinct people each with the value count as ≥2 → contested. The corpus OWNER's many
// variants collapse to ONE owner too, so his number is not "contested with himself".
func ownerCount(ctx context.Context, s Store, idNorm string, owner *identity.Matcher) int {
	links, err := s.IdentifierLinksForID(ctx, idNorm)
	if err != nil {
		return 0
	}
	seen := map[string]bool{}
	norms := make([]string, 0, len(links))
	for _, l := range links {
		if l.PersonNorm != "" && !seen[l.PersonNorm] {
			seen[l.PersonNorm] = true
			norms = append(norms, l.PersonNorm)
		}
	}
	return distinctPeopleOwnerAware(ctx, s, norms, owner)
}

// distinctPeopleOwnerAware counts distinct people among norms, collapsing ALL of the owner's
// variant norms into ONE person and applying the father/son NISS guard to the rest.
func distinctPeopleOwnerAware(ctx context.Context, s Store, norms []string, owner *identity.Matcher) int {
	ownerSeen := false
	other := make([]string, 0, len(norms))
	for _, n := range norms {
		if n == "" {
			continue
		}
		if owner.IsOwnerNorm(n) {
			ownerSeen = true
			continue
		}
		other = append(other, n)
	}
	nat, _ := s.NationalNumbersByPersons(ctx, other)
	c := store.DistinctPeopleGuarded(other, nat)
	if ownerSeen {
		c++
	}
	return c
}

// ownerIsDominant reports whether the OWNER holds a decisive plurality of the proximity-link
// DOCS for a contested value: ≥3 distinct owner documents AND ≥2× the runner-up (the highest
// distinct-doc count among any single non-owner person). Owner variants collapse to one
// bucket; non-owner norms are counted per distinct norm (never merged across norms, so a
// non-owner is never inflated to look dominant). This is the plurality gate that turns the
// owner's own reprinted reference number from "ambiguous_owner" into an affirmed answer.
func ownerIsDominant(ctx context.Context, s Store, idNorm string, owner *identity.Matcher) bool {
	links, err := s.IdentifierLinksForID(ctx, idNorm)
	if err != nil {
		return false
	}
	ownerDocs := map[string]bool{}
	otherDocs := map[string]map[string]bool{}
	for _, l := range links {
		if l.ContentID == "" || l.PersonNorm == "" {
			continue
		}
		if owner.IsOwnerNorm(l.PersonNorm) {
			ownerDocs[l.ContentID] = true
			continue
		}
		if otherDocs[l.PersonNorm] == nil {
			otherDocs[l.PersonNorm] = map[string]bool{}
		}
		otherDocs[l.PersonNorm][l.ContentID] = true
	}
	ownerN := len(ownerDocs)
	runnerUp := 0
	for _, ds := range otherDocs {
		if len(ds) > runnerUp {
			runnerUp = len(ds)
		}
	}
	return ownerN >= 3 && ownerN >= 2*runnerUp
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
