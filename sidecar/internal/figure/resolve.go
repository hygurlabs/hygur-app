package figure

import (
	"context"
	"sort"

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
)

// Result is the outcome of a figure resolution — the value NODE plus its resolved context EDGES,
// or a decline with a reason. Reuses fact.Tier / fact.Source so the tool + card treat identifier
// and figure answers uniformly.
type Result struct {
	Label      string
	Value      string // canonical numeric ("7421.85")
	Raw        string // as written ("7 421,85")
	Unit       string // "EUR"
	Period     string // resolved period key
	Direction  string // resolved direction (Dir*)
	Tier       fact.Tier
	Reason     string
	Sources    []fact.Source
	Candidates int
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

	// The surviving nodes must agree on ONE value (they are the same period+direction across
	// possibly several source documents). If they disagree, we cannot pick → decline.
	if v := distinctValues(nodes); len(v) != 1 {
		res.Reason = ReasonAmbiguousValue
		return res, nil
	}

	pick := nodes[0]
	res.Value, res.Raw, res.Unit = pick.Value, pick.Raw, pick.Unit
	res.Period, res.Direction = pick.Period, pick.Direction
	res.Tier = fact.TierHigh
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.ContentID == "" || seen[n.ContentID] {
			continue
		}
		seen[n.ContentID] = true
		if it, e := s.GetKnowledgeItem(ctx, n.ContentID); e == nil && it != nil {
			res.Sources = append(res.Sources, fact.Source{ContentID: n.ContentID, Title: it.Title})
		}
	}
	return res, nil
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
		// No usable period edge on any node. If they all agree on one value, return them; else the
		// "latest" is undefined → decline.
		if len(distinctValues(nodes)) == 1 {
			return nodes, ""
		}
		return nil, ReasonAmbiguousPeriod
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
