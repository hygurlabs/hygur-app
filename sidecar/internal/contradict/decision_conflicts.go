package contradict

import (
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// DetectDecisionConflicts is the G4 guardrail: find (entity, attribute) divergences
// between a STANDING decision's own claim and RECENT capture claims across ALL
// threads — i.e. a fresh capture that contradicts a confirmed decision. Unlike
// DetectClaimConflicts (thread-scoped), this clusters cross-thread, anchored on each
// decision. The decision is member[0], dated decidedAt[id]; only captures asserted
// AFTER the decision and within the recency window, carrying a divergent value, join
// it. Output feeds the SAME reconcile pipeline (the caller judges conflict /
// supersedes / none), so a decision conflict surfaces exactly like any other — and a
// "supersedes" verdict means a later capture has overtaken the decision (reopen).
//
// decidedAt maps a decision's content_id → its decided-on date (RFC3339); a decision
// without a date still anchors (every newer capture then qualifies). Decisions must
// already carry extracted_claims in metadata (extracted on the small indexing model).
func DetectDecisionConflicts(decisions []*store.KnowledgeItem, decidedAt map[string]string, items []*store.KnowledgeItem, sinceRFC3339 string) []ClaimConflict {
	// Index capture claims by normalized (entity, attribute) across ALL threads.
	byEA := map[string][]ClaimRef{}
	for _, it := range items {
		if it == nil {
			continue
		}
		at := ""
		if t := store.GetCanonicalDate(it); !t.IsZero() {
			at = t.UTC().Format(time.RFC3339)
		}
		for _, c := range claimsFromMetadata(it.Metadata) {
			a := at
			if a == "" {
				a = c.AssertedAt
			}
			cid := c.SourceID
			if cid == "" {
				cid = it.ContentID
			}
			ea := normKey(c.Entity) + "\x1f" + normKey(c.Attribute)
			byEA[ea] = append(byEA[ea], ClaimRef{SourceID: cid, Value: c.Value, Quote: c.Quote, AssertedAt: a})
		}
	}

	var out []ClaimConflict
	for _, dec := range decisions {
		if dec == nil {
			continue
		}
		decAt := decidedAt[dec.ContentID]
		for _, dc := range claimsFromMetadata(dec.Metadata) {
			ea := normKey(dc.Entity) + "\x1f" + normKey(dc.Attribute)
			caps := byEA[ea]
			if len(caps) == 0 {
				continue
			}
			decVal := normKey(dc.Value)
			members := []ClaimRef{{SourceID: dec.ContentID, Value: dc.Value, Quote: dc.Quote, AssertedAt: decAt}}
			seen := map[string]bool{decVal: true}
			for _, r := range caps {
				if r.SourceID == dec.ContentID {
					continue
				}
				if decAt != "" && r.AssertedAt != "" && r.AssertedAt <= decAt {
					continue // not newer than the decision → not a fresh contradiction
				}
				if sinceRFC3339 != "" && r.AssertedAt != "" && r.AssertedAt < sinceRFC3339 {
					continue // stale, outside the recency window
				}
				rv := normKey(r.Value)
				if rv == decVal || seen[rv] {
					continue // same value, or one representative per distinct value
				}
				seen[rv] = true
				members = append(members, r)
			}
			if len(members) >= 2 { // the decision + ≥1 divergent, newer capture
				out = append(out, ClaimConflict{
					Cluster:   "decision:" + dec.ContentID,
					Entity:    dc.Entity,
					Attribute: dc.Attribute,
					Members:   members,
					Key:       conflictKey("decision:"+dec.ContentID, dc.Entity, dc.Attribute, members),
				})
			}
		}
	}
	return out
}
