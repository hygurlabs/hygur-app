package figure

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/identity"
	"github.com/hygur/sidecar/internal/store"
)

// Reason codes explain WHY a figure resolution declined, so the voice phrases the right question
// instead of stating a number. Empty on a confident result.
const (
	ReasonUnknownLabel    = "unknown_figure_label" // the label is not a seeded figure label
	ReasonNoFigure        = "no_figure"            // no figure node for the subject+label(+period)
	ReasonAmbiguousDir    = "ambiguous_direction"  // several directions compete, none requested
	ReasonAmbiguousPeriod = "ambiguous_period"     // cannot order by period (ties / missing keys)
	ReasonAmbiguousValue  = "ambiguous_value"      // the selected period/direction holds >1 value
	ReasonAmbiguousMedic  = "ambiguous_medication" // several medications carry a dose, none named
)

// Result is the outcome of a figure resolution — the value NODE plus its resolved context EDGES,
// or a decline with a reason. Reuses fact.Tier / fact.Source so the tool + card treat identifier
// and figure answers uniformly.
type Result struct {
	Label      string
	Value      string // canonical numeric ("7421.85", "500")
	Raw        string // as written ("7 421,85", "500")
	Unit       string // "EUR", "mg", "mcg", ...
	Period     string // resolved period key
	Direction  string // resolved direction (Dir*)
	Medication string // resolved dosage medication (folded) or ""
	Frequency  string // resolved dosage cadence ("N×/day") or ""
	// Prior holds the values this figure SUPERSEDED — an older dose that a newer document replaced,
	// surfaced as a cross-document contradiction ("current 10 mg, was 5 mg"). Empty when none.
	Prior      []PriorValue
	Tier       fact.Tier
	Reason     string
	Sources    []fact.Source
	Candidates int
}

// PriorValue is one superseded figure value (the older reading a newer document replaced).
type PriorValue struct {
	Value string
	Raw   string
	Unit  string
}

// Store is the narrow slice of *store.DB the resolver needs (kept small for testability). It reuses
// the SAME entity-resolution methods the identifier lookup uses — figures live in one graph.
type Store interface {
	ResolvePersonNorms(ctx context.Context, query string, limit int) ([]string, error)
	PersonNormsContainingTokens(ctx context.Context, tokens []string) ([]string, error)
	FigureNodesForEntities(ctx context.Context, norms []string, label string) ([]store.FigureNode, error)
	GetKnowledgeItem(ctx context.Context, contentID string) (*store.KnowledgeItem, error)
}

// Resolve is the deterministic TRAVERSAL that answers "the [latest] <label> [<direction>] [<period>]"
// for a subject (FIGURES_TRUTH_PLAN §3.2, G1–G5). It: (1) resolves the subject to entity norms
// (owner variants pooled when the subject is the owner); (2) gathers the subject's figure NODES of
// that label; (3) filters by DIRECTION (declining when several directions compete and none was
// asked); (4) filters or ORDERS by the PERIOD edge (the named period, else the latest); (5) returns
// the value + context, or DECLINES honestly on any ambiguity. No guessing, no RAG number. rawLabel,
// rawDirection and rawPeriod are the user's words (the tool passes them through); they are
// normalized here with the same generic pattern tables the extractor uses.
func Resolve(ctx context.Context, s Store, query, rawLabel, rawDirection, rawPeriod string, owner *identity.Matcher) (Result, error) {
	label := NormalizeFigureLabel(rawLabel)
	res := Result{Label: label, Tier: fact.TierNone}
	if label == "" {
		res.Reason = ReasonUnknownLabel
		return res, nil
	}

	// Resolve the subject to entity norms; pool the owner's name variants when the subject is the
	// owner, exactly as the identifier lookup does — so a figure attached to ANY owner variant (or to
	// the surname-first spelling on a statement) is reached.
	resolved, _ := s.ResolvePersonNorms(ctx, query, 20)
	queryIsOwner := owner.IsOwnerNorm(query)
	for _, r := range resolved {
		if owner.IsOwnerNorm(r) {
			queryIsOwner = true
			break
		}
	}
	norms := append(resolved, query)
	if queryIsOwner {
		if cands, e := s.PersonNormsContainingTokens(ctx, owner.Tokens()); e == nil {
			for _, c := range cands {
				if owner.IsOwnerNorm(c) {
					norms = append(norms, c)
				}
			}
		}
	}

	nodes, err := s.FigureNodesForEntities(ctx, norms, label)
	if err != nil {
		return res, err
	}
	res.Candidates = len(nodes)
	if len(nodes) == 0 {
		res.Reason = ReasonNoFigure
		return res, nil
	}

	// MEDICATION filter (dosage — C7). The medication is the qualifier a shared "dose" label denotes,
	// filtered exactly like direction: if the query names one of the candidates' medications keep only
	// it (isolation — two meds never cross-mix); if several medications carry a dose and none is named,
	// decline; a single medication needs no naming. Monetary figures carry no medication → no-op.
	if meds := distinctMedications(nodes); len(meds) > 0 {
		reqMed, namedUnknown := matchMedication(rawLabel, meds)
		switch {
		case reqMed != "":
			nodes = filterMedication(nodes, reqMed) // the named medication → isolate it
		case namedUnknown:
			res.Reason = ReasonNoFigure // a DIFFERENT medication was named that we have no dose for
			return res, nil
		case len(meds) > 1:
			res.Reason = ReasonAmbiguousMedic // several meds, none named → decline
			return res, nil
		}
	}

	// DIRECTION filter (G2/G3). If the user named a direction, keep only exact matches. If not, and
	// the candidates carry MORE THAN ONE distinct direction, the label is ambiguous → decline.
	reqDir := NormalizeDirection(rawDirection)
	if reqDir != "" {
		nodes = filterDirection(nodes, reqDir)
		if len(nodes) == 0 {
			res.Reason = ReasonNoFigure
			return res, nil
		}
	} else if dirs := distinctDirections(nodes); len(dirs) > 1 {
		res.Reason = ReasonAmbiguousDir
		return res, nil
	}

	// PERIOD selection (G2/G3). Named period → keep only that period. Otherwise pick the LATEST by
	// the period edge, declining when the ordering is not well-defined (no periods to order by, or a
	// tie at the top between different values).
	reqPeriod, hasReq := findPeriod(rawPeriod)
	if hasReq {
		nodes = filterPeriod(nodes, reqPeriod)
		if len(nodes) == 0 {
			res.Reason = ReasonNoFigure
			return res, nil
		}
	} else {
		var perr string
		nodes, perr = latestPeriod(nodes)
		if perr != "" {
			res.Reason = perr
			return res, nil
		}
	}

	// VALUE resolution with TEMPORAL SUPERSESSION (the reusable cross-doc-contradiction mechanism).
	// The survivors are one fact (same period+direction+medication) across possibly several documents.
	// If they AGREE, that's the value. If they DISAGREE, the LATEST document wins (ordered by doc date)
	// and the older reading is surfaced as a contradiction (res.Prior) — never averaged, never guessed.
	// When no clear latest exists (tie / no dates), we decline.
	pick, prior, reason := ResolveTemporal(nodes)
	if reason != "" {
		res.Reason = reason
		return res, nil
	}
	res.Value, res.Raw, res.Unit = pick.Value, pick.Raw, pick.Unit
	res.Period, res.Direction = pick.Period, pick.Direction
	res.Medication, res.Frequency = pick.Medication, pick.Frequency
	res.Prior = prior
	res.Tier = fact.TierHigh
	// Sources: every document that states the CHOSEN (current) value.
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Value != pick.Value || n.ContentID == "" || seen[n.ContentID] {
			continue
		}
		seen[n.ContentID] = true
		if it, e := s.GetKnowledgeItem(ctx, n.ContentID); e == nil && it != nil {
			res.Sources = append(res.Sources, fact.Source{ContentID: n.ContentID, Title: it.Title})
		}
	}
	return res, nil
}

// ResolveTemporal picks the current value from a set of same-fact nodes, applying temporal
// supersession when they disagree: the node(s) from the LATEST document date win, and the distinct
// older values become Prior (the surfaced contradiction). Declines (reason) when the survivors
// disagree but cannot be ordered (no doc dates, or a tie at the latest date holding several values).
// Exported so the SAME mechanism resolves other latest-assertion-wins facts (e.g. a meeting time
// across email + calendar sources — internal/rendezvous) without a parallel implementation.
func ResolveTemporal(nodes []store.FigureNode) (store.FigureNode, []PriorValue, string) {
	if len(nodes) == 0 {
		return store.FigureNode{}, nil, ReasonNoFigure
	}
	if len(distinctValues(nodes)) == 1 {
		return nodes[0], nil, ""
	}
	// Disagreement → order by document date. Find the latest date present.
	var latest time.Time
	for _, n := range nodes {
		if n.DocDate.After(latest) {
			latest = n.DocDate
		}
	}
	if latest.IsZero() {
		return store.FigureNode{}, nil, ReasonAmbiguousValue // no dates to order by
	}
	var top []store.FigureNode
	for _, n := range nodes {
		if n.DocDate.Equal(latest) {
			top = append(top, n)
		}
	}
	if len(distinctValues(top)) != 1 {
		return store.FigureNode{}, nil, ReasonAmbiguousValue // latest date is itself contradictory
	}
	pick := top[0]
	// Prior = the distinct OLDER values that this one superseded.
	var prior []PriorValue
	seen := map[string]bool{pick.Value: true}
	for _, n := range nodes {
		if seen[n.Value] {
			continue
		}
		seen[n.Value] = true
		prior = append(prior, PriorValue{Value: n.Value, Raw: n.Raw, Unit: n.Unit})
	}
	sort.Slice(prior, func(i, j int) bool { return prior[i].Value < prior[j].Value })
	return pick, prior, ""
}

// distinctMedications returns the set of non-empty medications present on the nodes.
func distinctMedications(nodes []store.FigureNode) map[string]bool {
	m := map[string]bool{}
	for _, n := range nodes {
		if n.Medication != "" {
			m[n.Medication] = true
		}
	}
	return m
}

// medNameStopwords are the query words that look like a medication name (Capitalized) but are not one
// — sentence openers, pronouns and the dose cue itself — so a query naming an ABSENT medication is
// distinguished from one naming NO medication.
var medNameStopwords = map[string]bool{
	"what": true, "whats": true, "which": true, "my": true, "your": true, "the": true,
	"dose": true, "doses": true, "dosage": true, "posology": true, "posologie": true,
	"is": true, "of": true, "for": true, "current": true, "latest": true, "last": true,
}

// matchMedication resolves the medication the query names against the candidate set. It returns
// (matched, false) when the query names a candidate; ("", true) when the query names a DIFFERENT
// medication (a Capitalized non-stopword word) the candidates don't carry — so resolution can decline
// honestly (barrier: a med that is absent → decline, never the wrong dose); and ("", false) when the
// query names no medication at all (caller falls back to single-candidate / ambiguity handling).
func matchMedication(query string, meds map[string]bool) (string, bool) {
	q := foldText(query)
	for m := range meds {
		if m != "" && strings.Contains(q, m) {
			return m, false // a candidate is named
		}
	}
	for _, tok := range medicationRe.FindAllString(query, -1) {
		if !medNameStopwords[foldText(tok)] {
			return "", true // a medication is named, but it is not one we have a dose for
		}
	}
	return "", false // no medication named
}

func filterMedication(nodes []store.FigureNode, med string) []store.FigureNode {
	var out []store.FigureNode
	for _, n := range nodes {
		if n.Medication == med {
			out = append(out, n)
		}
	}
	return out
}

func filterDirection(nodes []store.FigureNode, dir string) []store.FigureNode {
	var out []store.FigureNode
	for _, n := range nodes {
		if n.Direction == dir {
			out = append(out, n)
		}
	}
	return out
}

func filterPeriod(nodes []store.FigureNode, period string) []store.FigureNode {
	var out []store.FigureNode
	for _, n := range nodes {
		if n.Period == period {
			out = append(out, n)
		}
	}
	return out
}

func distinctDirections(nodes []store.FigureNode) map[string]bool {
	m := map[string]bool{}
	for _, n := range nodes {
		if n.Direction != "" {
			m[n.Direction] = true
		}
	}
	return m
}

func distinctValues(nodes []store.FigureNode) map[string]bool {
	m := map[string]bool{}
	for _, n := range nodes {
		m[n.Value] = true
	}
	return m
}

// latestPeriod returns the nodes of the highest-ranked period, or a decline reason. It declines
// when NO node carries a usable period (nothing to order by) and the candidates disagree on value —
// picking a "latest" with no period edge would be a guess. A single value with no period is fine
// (nothing to confuse it with).
func latestPeriod(nodes []store.FigureNode) ([]store.FigureNode, string) {
	// Rank periods; the max rank wins.
	best := -1
	for _, n := range nodes {
		if r := PeriodRank(n.Period); r > best {
			best = r
		}
	}
	if best <= 0 {
		// No usable period edge on any node (typical for a dosage). Defer to the temporal-supersession
		// value resolution, which orders any disagreement by document date instead of by period.
		return nodes, ""
	}
	var top []store.FigureNode
	for _, n := range nodes {
		if PeriodRank(n.Period) == best {
			top = append(top, n)
		}
	}
	// Deterministic order for stability.
	sort.Slice(top, func(i, j int) bool { return top[i].Value < top[j].Value })
	return top, ""
}
