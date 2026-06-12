package retrieval

import (
	"testing"

	"github.com/hygur/sidecar/internal/store"
)

func TestClassifyAuthority(t *testing.T) {
	cases := []struct {
		name         string
		sourceType   string
		status       string
		wantTier     AuthorityTier
		wantValidity Validity
	}{
		{"standing decision → confirmed/current", store.SourceTypeDecision, store.DecisionStanding, TierConfirmed, ValidityCurrent},
		{"superseded decision → confirmed/superseded", store.SourceTypeDecision, store.DecisionSuperseded, TierConfirmed, ValiditySuperseded},
		{"proposed decision → candidate (not yet authoritative)", store.SourceTypeDecision, store.DecisionProposed, TierCandidate, ValidityCurrent},
		{"decision with no attrs row → confirmed/current (defaults standing)", store.SourceTypeDecision, "", TierConfirmed, ValidityCurrent},
		{"mail capture → capture/current", store.SourceTypeMail, "", TierCapture, ValidityCurrent},
		{"email capture → capture/current", store.SourceTypeEmail, "", TierCapture, ValidityCurrent},
		{"file capture → capture/current", store.SourceTypeFile, "", TierCapture, ValidityCurrent},
		{"note ignores a stray status → capture/current", store.SourceTypeNote, store.DecisionStanding, TierCapture, ValidityCurrent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTier, gotValidity := classifyAuthority(tc.sourceType, tc.status)
			if gotTier != tc.wantTier || gotValidity != tc.wantValidity {
				t.Errorf("classifyAuthority(%q, %q) = (%s, %s); want (%s, %s)",
					tc.sourceType, tc.status, gotTier, gotValidity, tc.wantTier, tc.wantValidity)
			}
		})
	}
}
