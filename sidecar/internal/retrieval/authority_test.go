package retrieval

import (
	"testing"

	"github.com/hygur/sidecar/internal/contradict"
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

func member(id, asserted string) contradict.ClaimRef {
	return contradict.ClaimRef{SourceID: id, AssertedAt: asserted}
}

func rc(kind string, dismissed bool, members ...contradict.ClaimRef) contradict.ReconciledConflict {
	return contradict.ReconciledConflict{
		ClaimConflict: contradict.ClaimConflict{Members: members},
		Verdict:       contradict.Verdict{Kind: kind},
		Dismissed:     dismissed,
	}
}

func TestConflictValidity(t *testing.T) {
	conflicts := []contradict.ReconciledConflict{
		// supersedes: the later member (B) stays current; the earlier (A) is superseded.
		rc("supersedes", false, member("mail:A", "2025-01-01"), member("mail:B", "2025-03-01")),
		// conflict: both members are conflicted.
		rc("conflict", false, member("mail:C", "2025-02-01"), member("mail:D", "2025-02-02")),
		// none: contributes nothing.
		rc("none", false, member("mail:E", "2025-01-01"), member("mail:F", "2025-01-02")),
		// dismissed: contributes nothing even though it's a real conflict.
		rc("conflict", true, member("mail:G", "2025-01-01"), member("mail:H", "2025-01-02")),
		// conflicted must dominate superseded when an id appears in both.
		rc("supersedes", false, member("mail:C", "2024-01-01"), member("mail:Z", "2025-09-01")),
	}
	got := conflictValidity(conflicts)

	want := map[string]Validity{
		"mail:A": ValiditySuperseded, // earlier side of a supersedes
		"mail:C": ValidityConflicted, // in a conflict (dominates the supersedes it also loses)
		"mail:D": ValidityConflicted,
	}
	// B (current side), E/F (none), G/H (dismissed), Z (current side) must be absent.
	for _, absent := range []string{"mail:B", "mail:E", "mail:F", "mail:G", "mail:H", "mail:Z"} {
		if v, ok := got[absent]; ok {
			t.Errorf("%s should be absent (current/none/dismissed), got %q", absent, v)
		}
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("conflictValidity[%s] = %q, want %q", id, got[id], w)
		}
	}
}
