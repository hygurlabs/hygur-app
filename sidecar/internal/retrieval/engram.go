package retrieval

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// The Engram dossier — a subject's consolidated memory, assembled deterministically
// (no LLM) from the entity index, the Hebbian graph, and the consolidation signals.
// It is the central read artifact: who/what the subject is (network of neighbors), a
// timeline ordered by retention strength (recency × salience) with von Restorff spikes
// for surprising items, and the live/dead compartment (standing vs superseded
// decisions, open contradictions). Faculties read this; the LLM only narrates it.

const (
	engramFirstCap        = 60   // direct (1st-order) items considered
	engramNetworkMax      = 12   // Hebbian neighbors kept as the subject's network
	engramSpikeWeight     = 0.30 // von Restorff: surprise lift added to retention strength
	engramNeighbors2nd    = 6    // top neighbors expanded for the 2nd-order timeline
	engramPerNeighbor2nd  = 12   // items pulled per expanded neighbor
	engramSecondCap       = 15   // hard cap on 2nd-order items (noise control)
	engramNeutralSalience = 0.30 // salience assumed when an item was never scored by the dream
	engramNamedBonus      = 0.10 // NPMI-equivalent lift for a named (ner_) neighbor over a claim

	// FSRS/DSR forgetting curve for the dossier timeline (see docs/PSYCHE_GROUNDING_PLAN.md,
	// A2). R(t,S) = (1 + FACTOR·t/S)^DECAY is a POWER law: unlike the exponential
	// Ebbinghaus curve (ComputeStrength, right for eviction), its tail stays meaningful,
	// so a months-old item keeps a real retrievability and the subject's history stays
	// visible recent→old instead of collapsing to ~0. Stability (days) is derived from
	// salience — a salient memory decays slower.
	fsrsFactor           = 19.0 / 81.0 // ≈0.2346 → R = 0.9 exactly at age = stability
	fsrsDecay            = -0.5
	engramStabilityBase  = 30.0 // days — base stability at zero salience
	engramStabilityBoost = 4.0  // salience 0→1 stretches stability ×1→×5
)

// engramRetrievability is the FSRS power-law retention R(t,S) used to order a dossier
// timeline. Stability S = base·(1 + boost·salience); higher salience ⇒ slower decay.
func engramRetrievability(ageDays, salience float64) float64 {
	if ageDays < 0 {
		ageDays = 0
	}
	s := engramStabilityBase * (1 + salClamp(salience)*engramStabilityBoost)
	if s <= 0 {
		return 1
	}
	return math.Pow(1+fsrsFactor*ageDays/s, fsrsDecay)
}

// neighborRank orders network neighbors: NPMI weight, plus a small bonus for a named
// (ner_) entity so it leads a claim-only neighbor of comparable association.
func neighborRank(n EngramNeighbor) float64 {
	if n.Type != "" {
		return n.Weight + engramNamedBonus
	}
	return n.Weight
}

// engramArticles are leading determiners that mark a generic reference ("the author",
// "le contrat") rather than a named entity. Normalized (lowercase, accents stripped).
var engramArticles = map[string]bool{
	"the": true, "le": true, "la": true, "les": true, "l": true,
	"un": true, "une": true, "des": true,
}

// hasLeadingArticle reports whether a normalized norm starts with a generic determiner.
func hasLeadingArticle(norm string) bool {
	first, _, ok := strings.Cut(strings.TrimSpace(norm), " ")
	if !ok {
		return false
	}
	return engramArticles[first]
}

// Engram is a subject's consolidated dossier.
type Engram struct {
	Subject        EngramSubject    `json:"subject"`
	Network        []EngramNeighbor `json:"network"`
	Timeline       []EngramItem     `json:"timeline"`
	Decisions      []EngramItem     `json:"decisions"`      // standing/superseded decisions in the set
	Contradictions []EngramItem     `json:"contradictions"` // items carrying an open contradiction
}

// EngramSubject identifies the dossier's subject and its dominant kind.
type EngramSubject struct {
	Norm string `json:"norm"`
	Type string `json:"type"` // person|org|project|topic|claim
}

// EngramNeighbor is one node of the subject's network: its NPMI association weight and
// its kind (person/org/project/topic, or "" for a claim-only entity).
type EngramNeighbor struct {
	Norm   string  `json:"norm"`
	Weight float64 `json:"weight"`
	Type   string  `json:"type,omitempty"`
}

// EngramItem is one memory in a subject's timeline, annotated with what makes it
// rank (strength, surprise), how it connects (order 1 = direct, 2 = via a neighbor),
// and its live/dead status.
type EngramItem struct {
	ContentID      string  `json:"content_id"`
	Title          string  `json:"title"`
	SourceType     string  `json:"source_type"`
	Date           string  `json:"date,omitempty"`         // canonical content date (RFC3339)
	DateMissing    bool    `json:"date_missing,omitempty"` // ingestion gap — no canonical date
	Strength       float64 `json:"strength"`               // FSRS power-law retention (recency × salience)
	Surprise       float64 `json:"surprise,omitempty"`     // von Restorff novelty [0,1]
	Order          int     `json:"order"`                  // 1 = mentions the subject, 2 = via a neighbor
	ViaNeighbor    string  `json:"via_neighbor,omitempty"` // the neighbor norm, for order 2
	DecisionStatus string  `json:"decision_status,omitempty"`
	Contradicted   bool    `json:"contradicted,omitempty"`
	Closed         bool    `json:"closed,omitempty"` // latest status claim is terminal-negative
	score          float64 // internal rank score (not serialized)
}

// AssembleEngram builds the dossier for a subject deterministically. The subject is
// normalized server-side, so a raw name ("Acme") or a stored norm both work. Returns
// nil when the subject has no presence at all (no mentions and no graph edges).
func AssembleEngram(ctx context.Context, db *store.DB, subject string, now time.Time) (*Engram, error) {
	norm := contradict.NormKey(strings.TrimSpace(subject))
	if db == nil || norm == "" {
		return nil, nil
	}

	// Network: the subject's Hebbian neighbors with weights (the ramifications).
	rawNetwork, err := db.HebbianNeighborsWeighted(ctx, norm, now, 0, engramNetworkMax)
	if err != nil {
		return nil, err
	}
	// Part B: prefer named entities. Type each neighbor (dominant ner_ tag), drop generic
	// claim references (article-prefixed, e.g. "the author"), and give named entities a
	// small bonus so a real person/org leads when NPMI associations are comparable — a
	// much-stronger claim association still wins.
	netNorms := make([]string, len(rawNetwork))
	for i, n := range rawNetwork {
		netNorms[i] = n.Norm
	}
	nTypes, err := db.EntityDominantTypes(ctx, netNorms)
	if err != nil {
		return nil, err
	}
	network := make([]EngramNeighbor, 0, len(rawNetwork))
	for _, n := range rawNetwork {
		t := nTypes[n.Norm]
		if t == "" && hasLeadingArticle(n.Norm) {
			continue // generic claim reference, not a named entity
		}
		network = append(network, EngramNeighbor{Norm: n.Norm, Weight: n.Weight, Type: t})
	}
	sort.SliceStable(network, func(i, j int) bool {
		return neighborRank(network[i]) > neighborRank(network[j])
	})

	// 1st-order: items that mention the subject directly.
	directIDs, err := db.EntityMentionContentIDs(ctx, []string{norm}, engramFirstCap)
	if err != nil {
		return nil, err
	}
	if len(directIDs) == 0 && len(network) == 0 {
		return nil, nil // unknown subject
	}

	type pend struct {
		id    string
		order int
		via   string
		viaW  float64 // normalized edge weight (1 for direct items)
	}
	seen := make(map[string]bool, len(directIDs))
	var pending []pend
	for _, id := range directIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		pending = append(pending, pend{id: id, order: 1, viaW: 1})
	}

	// 2nd-order: items mentioning a neighbor (not already seen), down-weighted by the
	// neighbor's NPMI edge weight. The network is already NPMI-ranked, so a
	// non-discriminative hub (e.g. the corpus owner) is naturally demoted — no separate
	// hub penalty needed.
	var maxW float64
	for _, n := range network {
		if n.Weight > maxW {
			maxW = n.Weight
		}
	}
	var second []pend
	for i, n := range network {
		if i >= engramNeighbors2nd {
			break
		}
		ids, err := db.EntityMentionContentIDs(ctx, []string{n.Norm}, engramPerNeighbor2nd)
		if err != nil {
			return nil, err
		}
		wn := 1.0
		if maxW > 0 {
			wn = n.Weight / maxW
		}
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			second = append(second, pend{id: id, order: 2, via: n.Norm, viaW: wn})
		}
	}

	// Batch the cross-cutting signals over every candidate id once.
	allIDs := make([]string, 0, len(pending)+len(second))
	for _, p := range pending {
		allIDs = append(allIDs, p.id)
	}
	for _, p := range second {
		allIDs = append(allIDs, p.id)
	}
	sigs, err := db.ItemSignalsByIDs(ctx, allIDs)
	if err != nil {
		return nil, err
	}
	decStatus, err := db.DecisionStatuses(ctx, allIDs)
	if err != nil {
		return nil, err
	}
	contra, err := db.OpenContradictionContentIDs(ctx)
	if err != nil {
		return nil, err
	}

	build := func(p pend) *EngramItem {
		it, gerr := db.GetKnowledgeItem(ctx, p.id)
		if gerr != nil || it == nil {
			return nil
		}
		// Canonical content date is the honest timeline axis; a missing one is an
		// ingestion gap we surface rather than paper over with the ingestion time.
		var dateStr string
		var ageDays float64
		dateMissing := true
		if d := store.GetCanonicalDate(it); !d.IsZero() {
			dateStr = d.UTC().Format(time.RFC3339)
			ageDays = now.Sub(d).Hours() / 24.0
			dateMissing = false
		} else if !it.CreatedAt.IsZero() {
			ageDays = now.Sub(it.CreatedAt).Hours() / 24.0 // age basis only; never displayed
		}
		sal := engramNeutralSalience
		var surprise float64
		if s, ok := sigs[p.id]; ok {
			if s.Salience > 0 {
				sal = s.Salience
			}
			surprise = s.Surprise
		}
		strength := engramRetrievability(ageDays, sal)
		ei := &EngramItem{
			ContentID:      p.id,
			Title:          it.Title,
			SourceType:     it.SourceType,
			Date:           dateStr,
			DateMissing:    dateMissing,
			Strength:       strength,
			Surprise:       surprise,
			Order:          p.order,
			ViaNeighbor:    p.via,
			DecisionStatus: decStatus[p.id],
			score:          (strength + engramSpikeWeight*surprise) * p.viaW,
		}
		if _, ok := contra[p.id]; ok {
			ei.Contradicted = true
		}
		// A3: mark items whose latest status claim is a terminal-negative outcome
		// (the live/dead compartment).
		if closed, _ := contradict.ClosedNegative(contradict.ClaimsFromMetadata(it.Metadata)); closed {
			ei.Closed = true
		}
		return ei
	}

	var firstItems, secondItems []EngramItem
	for _, p := range pending {
		if ei := build(p); ei != nil {
			firstItems = append(firstItems, *ei)
		}
	}
	for _, p := range second {
		if ei := build(p); ei != nil {
			secondItems = append(secondItems, *ei)
		}
	}
	// Cap 2nd-order by score (noise control) before merging the timeline.
	sort.Slice(secondItems, func(i, j int) bool { return secondItems[i].score > secondItems[j].score })
	if len(secondItems) > engramSecondCap {
		secondItems = secondItems[:engramSecondCap]
	}
	timeline := append(firstItems, secondItems...)
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].score > timeline[j].score })

	// The live/dead compartment, derived from the annotated timeline.
	var decisions, contradictions []EngramItem
	for _, it := range timeline {
		if it.DecisionStatus != "" {
			decisions = append(decisions, it)
		}
		if it.Contradicted {
			contradictions = append(contradictions, it)
		}
	}

	typ, err := subjectType(ctx, db, norm)
	if err != nil {
		return nil, err
	}
	return &Engram{
		Subject:        EngramSubject{Norm: norm, Type: typ},
		Network:        network,
		Timeline:       timeline,
		Decisions:      decisions,
		Contradictions: contradictions,
	}, nil
}

// subjectType labels a subject by its dominant NER attribute (most-mentioned of
// person/org/project/topic); a subject seen only through claims is "claim".
func subjectType(ctx context.Context, db *store.DB, norm string) (string, error) {
	counts, err := db.EntityAttributeCounts(ctx, norm)
	if err != nil {
		return "", err
	}
	best, bestN := "", 0
	for attr, n := range counts {
		if !strings.HasPrefix(attr, "ner_") {
			continue
		}
		if n > bestN {
			best, bestN = attr, n
		}
	}
	if best == "" {
		return "claim", nil
	}
	return strings.TrimPrefix(best, "ner_"), nil
}
