package retrieval

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/keyed"
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

// Engram is a subject's consolidated dossier.
type Engram struct {
	Subject        EngramSubject      `json:"subject"`
	Identity       []EngramIdentifier `json:"identity,omitempty"` // typed identifiers, tier≥med (WP36.a)
	Claims         []EngramClaim      `json:"claims,omitempty"`   // active beliefs aggregated by attribute (WP36.a)
	Network        []EngramNeighbor   `json:"network"`
	Timeline       []EngramItem       `json:"timeline"`
	Decisions      []EngramItem       `json:"decisions"`      // standing/superseded decisions in the set
	Contradictions []EngramItem       `json:"contradictions"` // items carrying an open contradiction
}

// EngramIdentifier is one typed identifier the subject carries (national number, VAT,
// DUNS…), scored deterministically via fact.LookupIdentifier and kept only at tier≥med.
// Label is the id_* type stripped to a human phrase ("national number").
type EngramIdentifier struct {
	Type    string        `json:"type"`  // canonical id type (national_number)
	Label   string        `json:"label"` // clean display label ("national number")
	Value   string        `json:"value"`
	Raw     string        `json:"raw,omitempty"`
	Tier    string        `json:"tier"` // high | medium
	Sources []fact.Source `json:"sources,omitempty"`
}

// EngramClaim is an active belief about the subject, aggregated across its direct items
// by attribute: the dominant value, how many sources corroborate it, and whether other
// sources assert a divergent value (contested).
type EngramClaim struct {
	Attribute     string   `json:"attribute"`     // display attribute (as first written)
	Value         string   `json:"value"`         // dominant value
	State         string   `json:"state"`         // corroborated | contested
	Corroboration int      `json:"corroboration"` // distinct sources for the dominant value
	Sources       []string `json:"sources"`       // content_ids carrying the dominant value
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
	Label  string  `json:"label,omitempty"` // Type as a clean phrase — id_* stripped ("national number")
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
//
// owner (may be nil) unifies the OWNER's dossier: when the subject is the owner, ALL his
// name-variant norms are treated as ONE subject, so his mentions and network merge instead
// of fragmenting across spellings. This is READ-TIME and REVERSIBLE (nothing is written or
// merged in the store); a non-owner subject (a child, the father) is untouched and keeps its
// own separate dossier.
func AssembleEngram(ctx context.Context, db *store.DB, subject string, now time.Time, owner *identity.Matcher) (*Engram, error) {
	norm := contradict.NormKey(strings.TrimSpace(subject))
	if db == nil || norm == "" {
		return nil, nil
	}

	// Owner unification: gather every owner name-variant present in the graph and treat them as
	// one subject. Bounded by the owner's discriminative tokens + the strict matcher filter.
	subjectNorms := []string{norm}
	if owner.IsOwnerNorm(norm) {
		if cands, e := db.PersonNormsContainingTokens(ctx, owner.Tokens()); e == nil {
			seen := map[string]bool{norm: true}
			for _, c := range cands {
				if !seen[c] && owner.IsOwnerNorm(c) {
					seen[c] = true
					subjectNorms = append(subjectNorms, c)
				}
			}
		}
	}

	// Network: the subject's Hebbian neighbors with weights (the ramifications), unioned across
	// the owner's variant norms (identical to a single-norm query for a non-owner subject).
	rawNetwork, err := unionNeighbors(ctx, db, subjectNorms, now, engramNetworkMax)
	if err != nil {
		return nil, err
	}
	// Part B: named entities only. Type each neighbor (dominant ner_ tag) and keep only
	// real named entities (person/org/project/topic) — dropping junk (dates, email
	// fragments, function words) and claim-only generics ("montant", "invoice payment"),
	// so the network shows who/what the subject connects to, not stray claim phrases.
	netNorms := make([]string, len(rawNetwork))
	for i, n := range rawNetwork {
		netNorms[i] = n.Norm
	}
	nTypes, err := db.EntityDominantTypes(ctx, netNorms)
	if err != nil {
		return nil, err
	}
	// The subject's identifier types are its id_* graph neighbors. Collected from the
	// dominant types of ALL raw neighbors — a numeric identifier value reads as "junk"
	// to the subject filter below, so this must run before that filter drops it.
	idTypeSet := map[string]bool{}
	for _, t := range nTypes {
		if strings.HasPrefix(t, "id_") {
			idTypeSet[strings.TrimPrefix(t, "id_")] = true
		}
	}
	network := make([]EngramNeighbor, 0, len(rawNetwork))
	for _, n := range rawNetwork {
		t := nTypes[n.Norm]
		if t == "" || store.IsJunkSubjectNorm(n.Norm) {
			continue // claim-only generic, or junk (date/email/function word)
		}
		network = append(network, EngramNeighbor{Norm: n.Norm, Weight: n.Weight, Type: t, Label: cleanTypeLabel(t)})
	}
	sort.SliceStable(network, func(i, j int) bool {
		return neighborRank(network[i]) > neighborRank(network[j])
	})

	// 1st-order: items that mention the subject directly (all owner variants, merged).
	directIDs, err := db.EntityMentionContentIDs(ctx, subjectNorms, engramFirstCap)
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

	// Direct-item claims accumulate here for the aggregated-belief block (WP36.a).
	var directClaims []contradict.Claim
	build := func(p pend) *EngramItem {
		it, gerr := db.GetKnowledgeItem(ctx, p.id)
		if gerr != nil || it == nil {
			return nil
		}
		if p.order == 1 {
			for _, c := range contradict.ClaimsFromMetadata(it.Metadata) {
				if c.SourceID == "" {
					c.SourceID = p.id
				}
				directClaims = append(directClaims, c)
			}
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

	// Identity: the subject's typed identifiers, scored deterministically and kept only
	// at tier≥med (WP36.a). Assembly, not new computation — id types come from the subject's
	// proximity-linked identifiers (precise) unioned with its id_* graph neighbors, and each
	// value is resolved by fact.LookupIdentifier's existing scorer.
	linkTypes, err := db.IdentifierTypesForPersons(ctx, subjectNorms)
	if err != nil {
		return nil, err
	}
	for _, t := range linkTypes {
		idTypeSet[t] = true
	}
	identifiers, err := assembleIdentifiers(ctx, db, norm, idTypeSet, now, owner)
	if err != nil {
		return nil, err
	}

	// Beliefs: active claims about the subject, aggregated by attribute with corroboration/state.
	claims := aggregateClaims(directClaims, subjectNorms)

	return &Engram{
		Subject:        EngramSubject{Norm: norm, Type: typ},
		Identity:       identifiers,
		Claims:         claims,
		Network:        network,
		Timeline:       timeline,
		Decisions:      decisions,
		Contradictions: contradictions,
	}, nil
}

// DeterminedFacts is the authoritative fact layer for ONE subject in a chat turn: its
// determined typed identifiers (tier≥med) and active aggregated claims, assembled
// deterministically from the WP36.a dossier bricks. The chat pipeline injects these as the
// VERIFIED layer so factual identifier VALUES are voiced from here — never lifted from raw
// (untrusted) document excerpts. No LLM; skips the timeline/network expansion the full
// dossier builds, since the authoritative layer needs only the determined values.
type DeterminedFacts struct {
	Subject  EngramSubject      `json:"subject"`
	IsOwner  bool               `json:"is_owner,omitempty"`
	Identity []EngramIdentifier `json:"identity,omitempty"`
	Claims   []EngramClaim      `json:"claims,omitempty"`
	Figures  []EngramFigure     `json:"figures,omitempty"` // labelled monetary figures (F1 — pilier 1)
}

// EngramFigure is one determined labelled MONETARY figure the subject carries — a figure NODE
// (value+unit) with its resolved context EDGES (period, direction) and source (FIGURES_TRUTH_PLAN
// F1). Assembled deterministically from store.figure_nodes so a subject's figures are ALWAYS in
// context (pilier 1), closing the hole where the chat answered a figure from RAG (the 357 € bug).
type EngramFigure struct {
	Label      string        `json:"label"` // normalized figure label ("vat", "dose")
	Value      string        `json:"value"` // canonical numeric ("7421.85", "500")
	Raw        string        `json:"raw,omitempty"`
	Unit       string        `json:"unit,omitempty"`
	Period     string        `json:"period,omitempty"`
	Direction  string        `json:"direction,omitempty"`
	Medication string        `json:"medication,omitempty"` // dosage qualifier (C7)
	Frequency  string        `json:"frequency,omitempty"`  // dosage cadence ("N×/day")
	Sources    []fact.Source `json:"sources,omitempty"`
}

// HasFacts reports whether the subject carries any determined identifier, claim or figure.
func (d *DeterminedFacts) HasFacts() bool {
	return d != nil && (len(d.Identity) > 0 || len(d.Claims) > 0 || len(d.Figures) > 0)
}

// AssembleDeterminedFacts builds the authoritative facts (typed identifiers + active claims)
// for a subject, reusing the SAME bricks as AssembleEngram (owner unification, id-type
// gathering, assembleIdentifiers, aggregateClaims) but WITHOUT the timeline/2nd-order
// expansion — the chat authoritative layer needs the determined values, not the narrative.
// Returns a non-nil dossier even when empty (HasFacts reports it), or nil for an unknown
// subject. owner (may be nil) unifies the owner's name-variant norms into one subject.
func AssembleDeterminedFacts(ctx context.Context, db *store.DB, subject string, now time.Time, owner *identity.Matcher) (*DeterminedFacts, error) {
	norm := contradict.NormKey(strings.TrimSpace(subject))
	if db == nil || norm == "" {
		return nil, nil
	}

	// Owner unification (identical to AssembleEngram): the owner's name variants are ONE subject.
	subjectNorms := []string{norm}
	if owner.IsOwnerNorm(norm) {
		if cands, e := db.PersonNormsContainingTokens(ctx, owner.Tokens()); e == nil {
			seen := map[string]bool{norm: true}
			for _, c := range cands {
				if !seen[c] && owner.IsOwnerNorm(c) {
					seen[c] = true
					subjectNorms = append(subjectNorms, c)
				}
			}
		}
	}

	// Identifier types: id_* graph neighbors unioned with proximity-linked id types — the
	// exact same union AssembleEngram computes, so identifier recall is unchanged.
	idTypeSet := map[string]bool{}
	rawNetwork, err := unionNeighbors(ctx, db, subjectNorms, now, engramNetworkMax)
	if err != nil {
		return nil, err
	}
	if len(rawNetwork) > 0 {
		netNorms := make([]string, len(rawNetwork))
		for i, n := range rawNetwork {
			netNorms[i] = n.Norm
		}
		nTypes, err := db.EntityDominantTypes(ctx, netNorms)
		if err != nil {
			return nil, err
		}
		for _, t := range nTypes {
			if strings.HasPrefix(t, "id_") {
				idTypeSet[strings.TrimPrefix(t, "id_")] = true
			}
		}
	}
	linkTypes, err := db.IdentifierTypesForPersons(ctx, subjectNorms)
	if err != nil {
		return nil, err
	}
	for _, t := range linkTypes {
		idTypeSet[t] = true
	}
	identifiers, err := assembleIdentifiers(ctx, db, norm, idTypeSet, now, owner)
	if err != nil {
		return nil, err
	}

	// Active claims: the subject's direct-item claims aggregated by attribute (dominant value,
	// corroboration, contested state). Direct (1st-order) items only — same source as the dossier.
	directIDs, err := db.EntityMentionContentIDs(ctx, subjectNorms, engramFirstCap)
	if err != nil {
		return nil, err
	}
	var directClaims []contradict.Claim
	seenID := make(map[string]bool, len(directIDs))
	for _, id := range directIDs {
		if id == "" || seenID[id] {
			continue
		}
		seenID[id] = true
		it, gerr := db.GetKnowledgeItem(ctx, id)
		if gerr != nil || it == nil {
			continue
		}
		for _, c := range contradict.ClaimsFromMetadata(it.Metadata) {
			if c.SourceID == "" {
				c.SourceID = id
			}
			directClaims = append(directClaims, c)
		}
	}
	claims := aggregateClaims(directClaims, subjectNorms)

	// Figures (pilier 1): the subject's labelled monetary figure nodes, grouped by
	// (label, direction, period) and kept only where the group agrees on ONE value — so a
	// contested figure is dropped (fail-closed), never averaged or guessed.
	figNodes, err := db.AllFigureNodesForEntities(ctx, subjectNorms)
	if err != nil {
		return nil, err
	}
	figures := groupDeterminedFigures(ctx, db, figNodes)

	if len(identifiers) == 0 && len(claims) == 0 && len(figures) == 0 && len(directIDs) == 0 && len(rawNetwork) == 0 {
		return nil, nil // unknown subject — no presence at all
	}
	typ, err := subjectType(ctx, db, norm)
	if err != nil {
		return nil, err
	}
	return &DeterminedFacts{
		Subject:  EngramSubject{Norm: norm, Type: typ},
		Identity: identifiers,
		Claims:   claims,
		Figures:  figures,
	}, nil
}

// groupDeterminedFigures collapses raw figure nodes into determined figures: one per
// (label, direction, period) group, kept ONLY when every node in the group carries the same
// value (deterministic; a value conflict drops the group — never a guessed figure). Sources are
// the distinct documents backing the value. Deterministic order: label, then period desc.
func groupDeterminedFigures(ctx context.Context, db *store.DB, nodes []store.FigureNode) []EngramFigure {
	if len(nodes) == 0 {
		return nil
	}
	type group struct {
		label, dir, period, unit, medication, frequency string
		values                                          map[string]store.FigureNode // value → a representative node
		sources                                         map[string]bool
	}
	groups := map[string]*group{}
	for _, n := range nodes {
		if n.Value == "" || n.Label == "" {
			continue
		}
		// The medication is part of the group key so two different dosages ("Amoxicillin" vs
		// "Levothyroxine") never collapse into one contested group and get dropped.
		k := n.Label + "|" + n.Direction + "|" + n.Period + "|" + n.Medication
		g := groups[k]
		if g == nil {
			g = &group{label: n.Label, dir: n.Direction, period: n.Period, unit: n.Unit,
				medication: n.Medication, frequency: n.Frequency,
				values: map[string]store.FigureNode{}, sources: map[string]bool{}}
			groups[k] = g
		}
		g.values[n.Value] = n
		if n.ContentID != "" {
			g.sources[n.ContentID] = true
		}
	}
	out := make([]EngramFigure, 0, len(groups))
	for _, g := range groups {
		if len(g.values) != 1 {
			continue // conflicting values for the same (label,direction,period) → fail-closed
		}
		var rep store.FigureNode
		for _, n := range g.values {
			rep = n
		}
		var sources []fact.Source
		for cid := range g.sources {
			title := cid
			if it, e := db.GetKnowledgeItem(ctx, cid); e == nil && it != nil && it.Title != "" {
				title = it.Title
			}
			sources = append(sources, fact.Source{ContentID: cid, Title: title})
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i].ContentID < sources[j].ContentID })
		out = append(out, EngramFigure{
			Label: g.label, Value: rep.Value, Raw: rep.Raw, Unit: g.unit,
			Period: g.period, Direction: g.dir, Medication: g.medication, Frequency: g.frequency,
			Sources: sources,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].Period > out[j].Period
	})
	return out
}

// AssembleQueryFacts resolves a chat query's authoritative subjects DETERMINISTICALLY and
// assembles each one's determined facts — the CORE anti-hallucination layer. It reuses the
// EXISTING resolution: the owner (first-person framing — the app user IS the owner) plus the
// single named subject the query mentions (detectQuerySubject: pure string↔entity-index match,
// NO LLM, NO keyword list, NO per-type router). Only subjects that actually carry determined
// facts are returned. The owner is emitted first and de-duplicates a query subject that IS the
// owner. Best-effort per subject: an assembly error on one never drops the others.
func AssembleQueryFacts(ctx context.Context, db *store.DB, query string, now time.Time, owner *identity.Matcher, ownerSubject string) ([]DeterminedFacts, error) {
	if db == nil {
		return nil, nil
	}
	var out []DeterminedFacts
	covered := map[string]bool{}

	// The owner — always a subject of a first-person turn ("my VAT", "mon numéro…"), which
	// names no proper noun so detectQuerySubject can't surface it. ownerSubject is a configured
	// owner name; empty (owner unconfigured) simply skips this.
	if ownerSubject != "" {
		if df, err := AssembleDeterminedFacts(ctx, db, ownerSubject, now, owner); err == nil && df.HasFacts() {
			df.IsOwner = true
			out = append(out, *df)
			covered[df.Subject.Norm] = true
		}
	}

	// The query's single named subject (deterministic; "" when the query names none). Skip when
	// it IS the owner (already covered) or the same norm was already emitted.
	subj, err := detectQuerySubject(ctx, db, query)
	if err != nil {
		return out, err
	}
	if subj != "" && !owner.IsOwnerNorm(subj) && !covered[contradict.NormKey(subj)] {
		if df, err := AssembleDeterminedFacts(ctx, db, subj, now, owner); err == nil && df.HasFacts() {
			out = append(out, *df)
			covered[df.Subject.Norm] = true
		}
	}

	// Keyed entities NAMED in the query (GENERALIZATION_PLAN — the universal entity-anchor). A vehicle
	// by its PLATE (generically any keyed entity) resolves straight to its key-anchored determined
	// attributes — "the model of my vehicle GT-139-RR" → the plate's Model X. Distinct-entity rejection
	// is intrinsic: only claims anchored to THIS key can fill it, so a Model Y (order-ref) / Model 3
	// (sold) claim never surfaces here. Appended after the person subjects and de-duplicated by norm.
	for _, k := range keyed.KeysInQuery(query) {
		if covered[k.Norm] {
			continue
		}
		if df := assembleKeyedFacts(ctx, db, k); df != nil && df.HasFacts() {
			out = append(out, *df)
			covered[k.Norm] = true
		}
	}
	return out, nil
}

// assembleKeyedFacts builds the authoritative DETERMINED facts for one keyed entity (a vehicle by its
// plate) — its key-anchored attributes, resolved deterministically (agreement / latest-wins /
// decline) by keyed.ResolveAttributes. Returns nil when the key carries no determined attribute, so
// the authoritative layer stays silent rather than guessing (a vehicle with no determined model
// declines). The resolved attributes are surfaced as EngramClaims so the chat injection renders them
// with the existing "Verified facts" path — no change to the prompt builder.
func assembleKeyedFacts(ctx context.Context, db *store.DB, k keyed.Key) *DeterminedFacts {
	if db == nil {
		return nil
	}
	nodes, err := db.AttrNodesForKeys(ctx, []string{k.Norm})
	if err != nil || len(nodes) == 0 {
		return nil
	}
	attrs := keyed.ResolveAttributes(nodes)
	if len(attrs) == 0 {
		return nil
	}
	claims := make([]EngramClaim, 0, len(attrs))
	for _, a := range attrs {
		claims = append(claims, EngramClaim{
			Attribute:     a.Attribute,
			Value:         a.Value,
			State:         a.State,
			Corroboration: a.Corroboration,
			Sources:       a.Sources,
		})
	}
	return &DeterminedFacts{
		Subject: EngramSubject{Norm: k.Norm, Type: k.Kind},
		Claims:  claims,
	}
}

// cleanTypeLabel turns an id_* dominant type into a human phrase for display
// ("id_national_number" → "national number"); non-id types pass through unchanged.
func cleanTypeLabel(t string) string {
	if strings.HasPrefix(t, "id_") {
		return strings.ReplaceAll(strings.TrimPrefix(t, "id_"), "_", " ")
	}
	return t
}

// assembleIdentifiers enumerates the subject's typed-identifier attributes (id_* in the
// entity index, unioned across the owner's variant norms) and scores each one via the
// existing fact.LookupIdentifier. Only confident results (tier high/medium) are kept, so
// the dossier never affirms an ambiguous or declined value. Read-only assembly.
func assembleIdentifiers(ctx context.Context, db *store.DB, norm string, idTypes map[string]bool, now time.Time, owner *identity.Matcher) ([]EngramIdentifier, error) {
	if len(idTypes) == 0 {
		return nil, nil
	}
	ordered := make([]string, 0, len(idTypes))
	for t := range idTypes {
		ordered = append(ordered, t)
	}
	sort.Strings(ordered) // deterministic order
	out := make([]EngramIdentifier, 0, len(ordered))
	for _, idType := range ordered {
		res, err := fact.LookupIdentifier(ctx, db, norm, idType, now, owner)
		if err != nil {
			return nil, err
		}
		if res.Tier != fact.TierHigh && res.Tier != fact.TierMed {
			continue // decline: never surface an unreliable identifier
		}
		out = append(out, EngramIdentifier{
			Type:    idType,
			Label:   cleanTypeLabel("id_" + idType),
			Value:   res.Value,
			Raw:     res.Raw,
			Tier:    string(res.Tier),
			Sources: res.Sources,
		})
	}
	return out, nil
}

// aggregateClaims groups the subject's direct-item claims by attribute into active beliefs.
// Within an attribute, values are grouped by norm; the value backed by the most distinct
// sources wins (dominant). A belief is "contested" when a different value is asserted by a
// separate source, else "corroborated". Only affirmed claims about one of the subject's own
// norms count. Deterministic ordering: corroboration desc, then attribute.
func aggregateClaims(claims []contradict.Claim, subjectNorms []string) []EngramClaim {
	if len(claims) == 0 {
		return nil
	}
	subj := map[string]bool{}
	for _, n := range subjectNorms {
		subj[n] = true
	}
	type valGroup struct {
		display string
		sources map[string]bool
	}
	type attrGroup struct {
		display string
		values  map[string]*valGroup // value-norm → sources
	}
	byAttr := map[string]*attrGroup{}
	for _, c := range claims {
		if c.Polarity == "negate" || c.Attribute == "" || c.Value == "" {
			continue
		}
		if !subj[contradict.NormKey(c.Entity)] {
			continue
		}
		ak := contradict.NormKey(c.Attribute)
		ag := byAttr[ak]
		if ag == nil {
			ag = &attrGroup{display: strings.TrimSpace(c.Attribute), values: map[string]*valGroup{}}
			byAttr[ak] = ag
		}
		vk := contradict.NormKey(c.Value)
		vg := ag.values[vk]
		if vg == nil {
			vg = &valGroup{display: strings.TrimSpace(c.Value), sources: map[string]bool{}}
			ag.values[vk] = vg
		}
		src := c.SourceID
		if src == "" {
			src = c.Quote // last-resort distinct key; never empty in practice
		}
		vg.sources[src] = true
	}
	out := make([]EngramClaim, 0, len(byAttr))
	for _, ag := range byAttr {
		var best *valGroup
		contested := false
		distinctValues := 0
		for _, vg := range ag.values {
			if len(vg.sources) > 0 {
				distinctValues++
			}
			if best == nil || len(vg.sources) > len(best.sources) {
				best = vg
			}
		}
		if best == nil {
			continue
		}
		if distinctValues > 1 {
			contested = true
		}
		srcs := make([]string, 0, len(best.sources))
		for s := range best.sources {
			srcs = append(srcs, s)
		}
		sort.Strings(srcs)
		state := "corroborated"
		if contested {
			state = "contested"
		}
		out = append(out, EngramClaim{
			Attribute:     ag.display,
			Value:         best.display,
			State:         state,
			Corroboration: len(best.sources),
			Sources:       srcs,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Corroboration != out[j].Corroboration {
			return out[i].Corroboration > out[j].Corroboration
		}
		return out[i].Attribute < out[j].Attribute
	})
	return out
}

// unionNeighbors returns the Hebbian neighbors of one or more subject norms. For a single
// norm it is exactly HebbianNeighborsWeighted (behavior-preserving for non-owner subjects).
// For several (the owner's variants) it merges neighbors by MAX weight, drops the subject
// norms themselves (a variant is not its own neighbor), and keeps the top `max` by weight.
func unionNeighbors(ctx context.Context, db *store.DB, norms []string, now time.Time, max int) ([]store.Neighbor, error) {
	if len(norms) <= 1 {
		if len(norms) == 0 {
			return nil, nil
		}
		return db.HebbianNeighborsWeighted(ctx, norms[0], now, 0, max)
	}
	self := make(map[string]bool, len(norms))
	for _, n := range norms {
		self[n] = true
	}
	best := map[string]float64{}
	for _, n := range norms {
		ns, err := db.HebbianNeighborsWeighted(ctx, n, now, 0, max)
		if err != nil {
			return nil, err
		}
		for _, x := range ns {
			if self[x.Norm] {
				continue
			}
			if x.Weight > best[x.Norm] {
				best[x.Norm] = x.Weight
			}
		}
	}
	out := make([]store.Neighbor, 0, len(best))
	for nm, w := range best {
		out = append(out, store.Neighbor{Norm: nm, Weight: w})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Norm < out[j].Norm
	})
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
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
