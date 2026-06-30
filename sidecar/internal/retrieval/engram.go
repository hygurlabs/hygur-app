package retrieval

import (
	"context"
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
)

// Engram is a subject's consolidated dossier.
type Engram struct {
	Subject        EngramSubject    `json:"subject"`
	Network        []store.Neighbor `json:"network"`
	Timeline       []EngramItem     `json:"timeline"`
	Decisions      []EngramItem     `json:"decisions"`      // standing/superseded decisions in the set
	Contradictions []EngramItem     `json:"contradictions"` // items carrying an open contradiction
}

// EngramSubject identifies the dossier's subject and its dominant kind.
type EngramSubject struct {
	Norm string `json:"norm"`
	Type string `json:"type"` // person|org|project|topic|claim
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
	Strength       float64 `json:"strength"`               // Ebbinghaus retention (recency × salience)
	Surprise       float64 `json:"surprise,omitempty"`     // von Restorff novelty [0,1]
	Order          int     `json:"order"`                  // 1 = mentions the subject, 2 = via a neighbor
	ViaNeighbor    string  `json:"via_neighbor,omitempty"` // the neighbor norm, for order 2
	DecisionStatus string  `json:"decision_status,omitempty"`
	Contradicted   bool    `json:"contradicted,omitempty"`
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
	network, err := db.HebbianNeighborsWeighted(ctx, norm, now, 0, engramNetworkMax)
	if err != nil {
		return nil, err
	}

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

	// 2nd-order: items mentioning a top neighbor (not already seen), down-weighted by
	// the neighbor's normalized edge weight so weak links can't flood the timeline.
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
		strength := ComputeStrength(sal, ageDays)
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
