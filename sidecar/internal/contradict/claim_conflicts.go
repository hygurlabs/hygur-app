package contradict

import (
	"sort"
	"strings"
	"unicode"

	"github.com/hygur/sidecar/internal/store"
)

// W6 stage 3 (REDUCE) — deterministic foundation. Over the LLM-extracted claims
// cached on each item (extracted_claims), within a thread cluster, find the same
// (entity, attribute) carrying DIVERGENT values across ≥2 distinct sources. These
// are *candidate* semantic contradictions; the next step (LLM reconciliation)
// classifies each as a real conflict vs an evolution (supersedes) and runs the
// adversarial check. No LLM here — bounds the work + keeps every value cited.

// ClaimConflict is a candidate semantic contradiction: claims about the same
// (entity, attribute) in one thread assert divergent values, each cited.
type ClaimConflict struct {
	Cluster   string     `json:"cluster"`   // normalized thread subject
	Entity    string     `json:"entity"`    // representative entity (first seen)
	Attribute string     `json:"attribute"` // representative attribute (first seen)
	Members   []ClaimRef `json:"members"`   // ≥2, one per distinct value, cited
}

// ClaimRef cites one side of a candidate conflict: the asserted value + its source.
type ClaimRef struct {
	SourceID   string `json:"source_id"`
	Value      string `json:"value"`
	Quote      string `json:"quote"`
	AssertedAt string `json:"asserted_at"`
}

// DetectClaimConflicts scans the cached claims of items, clusters by thread, and
// returns candidate conflicts (≥2 distinct values from ≥2 distinct sources for one
// entity+attribute). Deterministic order.
//
// sinceRFC3339 is a recency cutoff: claims asserted before it are dropped, because
// time-relative facts go stale (a 2024 "available this week" means nothing now) and
// surfacing year-old contradictions is noise. Empty = no filter; undated claims are
// always kept (can't prove them stale). Compare is lexicographic, so the cutoff and
// the stored AssertedAt must share the RFC3339/UTC form the indexer already uses.
func DetectClaimConflicts(items []*store.KnowledgeItem, sinceRFC3339 string) []ClaimConflict {
	type sourced struct {
		claim     Claim
		contentID string
	}
	threads := map[string][]sourced{}
	for _, it := range items {
		if it == nil {
			continue
		}
		key := normalizeSubject(it.Title)
		if key == "" {
			continue
		}
		for _, c := range claimsFromMetadata(it.Metadata) {
			if sinceRFC3339 != "" && c.AssertedAt != "" && c.AssertedAt < sinceRFC3339 {
				continue // stale: outside the recency window
			}
			cid := c.SourceID
			if cid == "" {
				cid = it.ContentID
			}
			threads[key] = append(threads[key], sourced{claim: c, contentID: cid})
		}
	}

	keys := make([]string, 0, len(threads))
	for k := range threads {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []ClaimConflict
	for _, k := range keys {
		// Group a thread's claims by (entity, attribute).
		type grp struct {
			entity, attribute string
			refs              []ClaimRef
		}
		groups := map[string]*grp{}
		var order []string
		for _, s := range threads[k] {
			gk := normKey(s.claim.Entity) + "\x1f" + normKey(s.claim.Attribute)
			g := groups[gk]
			if g == nil {
				g = &grp{entity: s.claim.Entity, attribute: s.claim.Attribute}
				groups[gk] = g
				order = append(order, gk)
			}
			g.refs = append(g.refs, ClaimRef{
				SourceID:   s.contentID,
				Value:      s.claim.Value,
				Quote:      s.claim.Quote,
				AssertedAt: s.claim.AssertedAt,
			})
		}
		for _, gk := range order {
			g := groups[gk]
			if members, ok := divergentMembers(g.refs); ok {
				out = append(out, ClaimConflict{
					Cluster:   k,
					Entity:    g.entity,
					Attribute: g.attribute,
					Members:   members,
				})
			}
		}
	}
	return out
}

// divergentMembers returns one representative ref per distinct (normalized) value,
// iff there are ≥2 distinct values carried by ≥2 distinct sources. A single source
// asserting two values is an explanation, not a cross-source contradiction.
func divergentMembers(refs []ClaimRef) ([]ClaimRef, bool) {
	byValue := map[string][]ClaimRef{}
	var valueOrder []string
	sources := map[string]bool{}
	for _, r := range refs {
		v := normKey(r.Value)
		if v == "" {
			continue
		}
		if _, seen := byValue[v]; !seen {
			valueOrder = append(valueOrder, v)
		}
		byValue[v] = append(byValue[v], r)
		sources[r.SourceID] = true
	}
	if len(byValue) < 2 || len(sources) < 2 {
		return nil, false
	}
	sort.Strings(valueOrder)
	chosen := map[string]bool{}
	members := make([]ClaimRef, 0, len(valueOrder))
	for _, v := range valueOrder {
		members = append(members, pickClaimRef(byValue[v], chosen))
		chosen[members[len(members)-1].SourceID] = true
	}
	// Need the citation set to span ≥2 distinct sources.
	if distinctRefSources(members) < 2 {
		return nil, false
	}
	return members, true
}

// pickClaimRef prefers the earliest-asserted ref whose source isn't used yet, so
// the citation set spans distinct sources; falls back to the earliest overall.
func pickClaimRef(refs []ClaimRef, used map[string]bool) ClaimRef {
	var best, bestFresh *ClaimRef
	for i := range refs {
		r := refs[i]
		if best == nil || refEarlier(r, *best) {
			best = &refs[i]
		}
		if !used[r.SourceID] && (bestFresh == nil || refEarlier(r, *bestFresh)) {
			bestFresh = &refs[i]
		}
	}
	if bestFresh != nil {
		return *bestFresh
	}
	return *best
}

func refEarlier(a, b ClaimRef) bool {
	if a.AssertedAt == b.AssertedAt {
		return a.SourceID < b.SourceID
	}
	return a.AssertedAt < b.AssertedAt // RFC3339 sorts lexicographically
}

func distinctRefSources(refs []ClaimRef) int {
	set := map[string]bool{}
	for _, r := range refs {
		set[r.SourceID] = true
	}
	return len(set)
}

// claimsFromMetadata parses cached claims, tolerating the []Claim (in-process) and
// []any-of-map (post-JSON-round-trip) shapes the metadata can hold.
func claimsFromMetadata(m map[string]any) []Claim {
	if m == nil {
		return nil
	}
	switch raw := m["extracted_claims"].(type) {
	case []Claim:
		return raw
	case []any:
		out := make([]Claim, 0, len(raw))
		for _, e := range raw {
			mm, ok := e.(map[string]any)
			if !ok {
				continue
			}
			c := Claim{
				Entity:     mapStr(mm, "entity"),
				Attribute:  mapStr(mm, "attribute"),
				Value:      mapStr(mm, "value"),
				Polarity:   mapStr(mm, "polarity"),
				Quote:      mapStr(mm, "quote"),
				SourceID:   mapStr(mm, "source_id"),
				AssertedAt: mapStr(mm, "asserted_at"),
			}
			if c.Entity != "" && c.Attribute != "" {
				out = append(out, c)
			}
		}
		return out
	default:
		return nil
	}
}

func mapStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// normKey lowercases and reduces to space-separated alphanumeric words — the
// grouping/equality key for entities, attributes, and values.
func normKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
