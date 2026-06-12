package handlers

import (
	"testing"

	"github.com/hygur/sidecar/internal/retrieval"
)

func TestSourceStratum(t *testing.T) {
	cases := []struct {
		name string
		s    RAGSource
		want string
	}{
		{"confirmed/current → your decision", RAGSource{Tier: string(retrieval.TierConfirmed), Validity: string(retrieval.ValidityCurrent)}, "your decision"},
		{"confirmed/superseded → superseded", RAGSource{Tier: string(retrieval.TierConfirmed), Validity: string(retrieval.ValiditySuperseded)}, "superseded"},
		{"conflicted → contested (validity wins over tier)", RAGSource{Tier: string(retrieval.TierConfirmed), Validity: string(retrieval.ValidityConflicted)}, "contested"},
		{"candidate → proposed decision", RAGSource{Tier: string(retrieval.TierCandidate), Validity: string(retrieval.ValidityCurrent)}, "proposed decision"},
		{"external capture → external", RAGSource{Tier: string(retrieval.TierCapture), Validity: string(retrieval.ValidityCurrent), OwnerOrigin: string(retrieval.OriginExternal)}, "external"},
		{"own current capture → baseline (no tag)", RAGSource{Tier: string(retrieval.TierCapture), Validity: string(retrieval.ValidityCurrent), OwnerOrigin: string(retrieval.OriginOwner)}, ""},
		{"unannotated → no tag", RAGSource{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sourceStratum(c.s); got != c.want {
				t.Errorf("sourceStratum(%+v) = %q, want %q", c.s, got, c.want)
			}
		})
	}
}
