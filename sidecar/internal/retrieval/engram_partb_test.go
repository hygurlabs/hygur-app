package retrieval

import "testing"

// Part B network shaping: a named entity is preferred over a claim of comparable
// association, but not over a much stronger one.
func TestEngramNeighborPartB(t *testing.T) {
	named := EngramNeighbor{Norm: "x", Weight: 0.30, Type: "org"}
	claimSame := EngramNeighbor{Norm: "y", Weight: 0.30}
	claimStrong := EngramNeighbor{Norm: "z", Weight: 0.90}
	if neighborRank(named) <= neighborRank(claimSame) {
		t.Error("a named neighbor should lead a claim of equal association")
	}
	if neighborRank(named) >= neighborRank(claimStrong) {
		t.Error("a much stronger claim association should still outrank a weak named entity")
	}
}
