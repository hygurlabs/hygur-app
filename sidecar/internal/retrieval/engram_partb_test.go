package retrieval

import "testing"

// Part B network shaping: generic article-led claim references are recognised, named
// entities are preferred over claims of comparable association but not over much
// stronger ones.
func TestEngramNeighborPartB(t *testing.T) {
	for _, s := range []string{"the author", "le contrat", "la mission", "les documents", "des factures", "l accord"} {
		if !hasLeadingArticle(s) {
			t.Errorf("%q should be flagged as an article-led generic reference", s)
		}
	}
	for _, s := range []string{"tara gaming ltd", "vincent boiteau", "novaquark", "aaa project", "contractor agreement", ""} {
		if hasLeadingArticle(s) {
			t.Errorf("%q should NOT be flagged as article-led", s)
		}
	}

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
