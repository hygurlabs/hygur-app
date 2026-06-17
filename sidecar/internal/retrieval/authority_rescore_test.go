package retrieval

import "testing"

func mkRes(id string, tier AuthorityTier, v Validity, score float64) UnifiedResult {
	return UnifiedResult{ContentID: id, Tier: tier, Validity: v, Score: score}
}

func idOrder(results []UnifiedResult) []string {
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ContentID
	}
	return ids
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAuthorityMultiplier(t *testing.T) {
	w := DefaultAuthorityWeights()
	cases := []struct {
		tier AuthorityTier
		v    Validity
		want float64
	}{
		{TierConfirmed, ValidityCurrent, 1.6},
		{TierConfirmed, ValiditySuperseded, 0.3}, // a superseded decision still demotes
		{TierCandidate, ValidityCurrent, 1.0},
		{TierCapture, ValidityCurrent, 1.0},
		{TierCapture, ValiditySuperseded, 0.3},
		{TierCapture, ValidityConflicted, 1.15},
		{TierConfirmed, ValidityConflicted, 1.15}, // conflict surfaces regardless of tier
	}
	for _, c := range cases {
		if got := w.multiplier(c.tier, c.v); got != c.want {
			t.Errorf("multiplier(%s,%s) = %v, want %v", c.tier, c.v, got, c.want)
		}
	}
}

func TestApplyAuthorityRescore(t *testing.T) {
	us := &UnifiedSearcher{}
	us.SetAuthorityRerank(true)

	t.Run("no-op when all capture/current (no regression)", func(t *testing.T) {
		r := []UnifiedResult{
			mkRes("a", TierCapture, ValidityCurrent, 3),
			mkRes("b", TierCapture, ValidityCurrent, 2),
			mkRes("c", TierCapture, ValidityCurrent, 1),
		}
		us.applyAuthorityRescore(r)
		if got := idOrder(r); !eqStrs(got, []string{"a", "b", "c"}) {
			t.Errorf("order = %v, want [a b c] (no reorder when no authority)", got)
		}
	})

	t.Run("confirmed promoted above higher-relevance capture", func(t *testing.T) {
		r := []UnifiedResult{
			mkRes("cap", TierCapture, ValidityCurrent, 1.0),
			mkRes("dec", TierConfirmed, ValidityCurrent, 0.7),
		}
		us.applyAuthorityRescore(r)
		if idOrder(r)[0] != "dec" {
			t.Errorf("want confirmed 'dec' promoted to #1, got %v", idOrder(r))
		}
	})

	t.Run("superseded sinks below confirmed", func(t *testing.T) {
		r := []UnifiedResult{
			mkRes("old", TierCapture, ValiditySuperseded, 1.0),
			mkRes("cur", TierConfirmed, ValidityCurrent, 0.9),
		}
		us.applyAuthorityRescore(r)
		if !eqStrs(idOrder(r), []string{"cur", "old"}) {
			t.Errorf("want [cur old] (superseded demoted), got %v", idOrder(r))
		}
	})

	t.Run("conflicted surfaces above equal-relevance current (guardrail)", func(t *testing.T) {
		r := []UnifiedResult{
			mkRes("plain", TierCapture, ValidityCurrent, 0.6),
			mkRes("cf", TierCapture, ValidityConflicted, 0.6),
		}
		us.applyAuthorityRescore(r)
		if idOrder(r)[0] != "cf" {
			t.Errorf("want conflicted 'cf' surfaced to #1, got %v", idOrder(r))
		}
	})

	t.Run("disabled → no-op", func(t *testing.T) {
		off := &UnifiedSearcher{} // rerank not enabled
		r := []UnifiedResult{
			mkRes("x", TierConfirmed, ValidityCurrent, 0.1),
			mkRes("y", TierCapture, ValidityCurrent, 0.9),
		}
		off.applyAuthorityRescore(r)
		if !eqStrs(idOrder(r), []string{"x", "y"}) {
			t.Errorf("disabled rerank must not reorder, got %v", idOrder(r))
		}
	})
}
