package retrieval

import (
	"testing"
	"time"
)

// Memory-phenomena validation — the salience/forgetting engine as an *executable
// specification* of human-memory science. Each test fixes a controlled scenario in
// which a named result predicts an ordering, and asserts the deterministic engine
// reproduces it. We assert DIRECTION (the law), never magnitude (the uncalibrated
// weights), so these pin qualitative behavior without freezing constants. We can't
// mine a behavioral ground truth from a passive corpus, so this is how we keep the
// engine honest: it must obey the mechanisms it claims to implement. A failure here
// is a finding — a mechanism that stopped behaving like the phenomenon it models.
//
// Most assertions are weight-robust (hold for any positive weights). The two marked
// DESIGN-INTENT encode a deliberate choice in how the axes trade off, not a universal
// law, and document what we *want* the engine to do.

func phBase(now time.Time) SalienceSignals {
	return SalienceSignals{IngestedAt: now.AddDate(0, 0, -30), Now: now}
}

// von Restorff (isolation effect): a distinctive/surprising item is consolidated
// better than an otherwise-identical bland one. Maps to the `surprise` term.
func TestPhenomenon_VonRestorff(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	bland := phBase(now)
	distinctive := phBase(now)
	distinctive.Surprise = 0.8
	if ComputeSalience(distinctive) <= ComputeSalience(bland) {
		t.Errorf("von Restorff: distinctive item must outscore bland (%.4f ≤ %.4f)",
			ComputeSalience(distinctive), ComputeSalience(bland))
	}
}

// Frequency / repetition effect: repeated retrieval raises salience, with
// diminishing returns (log saturation). Maps to fFreq(hit_count).
func TestPhenomenon_Frequency(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	mk := func(hits int) float64 {
		s := phBase(now)
		s.HitCount = hits
		s.LastAccessed = now.AddDate(0, 0, -1) // anchor access recency equal across the three
		return ComputeSalience(s)
	}
	if !(mk(10) > mk(3) && mk(3) > mk(1)) {
		t.Errorf("frequency: salience must rise with hit_count (1=%.4f 3=%.4f 10=%.4f)", mk(1), mk(3), mk(10))
	}
	// Diminishing returns: the 1→3 gain exceeds the 8→10 gain (log curvature).
	if (mk(3) - mk(1)) <= (mk(10) - mk(8)) {
		t.Errorf("frequency must show diminishing returns: Δ(1→3)=%.4f should exceed Δ(8→10)=%.4f",
			mk(3)-mk(1), mk(10)-mk(8))
	}
}

// Ebbinghaus forgetting curve: retention decays monotonically with time since access.
func TestPhenomenon_ForgettingCurve(t *testing.T) {
	d1, d30, d180 := ComputeStrength(0.3, 1), ComputeStrength(0.3, 30), ComputeStrength(0.3, 180)
	if !(d1 > d30 && d30 > d180) {
		t.Errorf("forgetting: strength must decay with age (1d=%.4f 30d=%.4f 180d=%.4f)", d1, d30, d180)
	}
}

// Depth-of-processing: important (high-salience) memories fade slower — for a fixed
// age, higher salience retains more strength (the half-life is salience-stretched).
func TestPhenomenon_ImportanceSlowsForgetting(t *testing.T) {
	const age = 60.0
	if ComputeStrength(0.9, age) <= ComputeStrength(0.1, age) {
		t.Errorf("important memory must retain more at %.0fd (%.4f vs %.4f)",
			age, ComputeStrength(0.9, age), ComputeStrength(0.1, age))
	}
}

// Fan effect / spreading activation: an item embedded in a denser associative network
// (more graph links) is more central, hence more salient. Maps to fLink(link_count) —
// the mechanism the Hebbian entity graph feeds.
func TestPhenomenon_ConnectivityMonotonic(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	mk := func(links int) float64 {
		s := phBase(now)
		s.LinkCount = links
		return ComputeSalience(s)
	}
	if !(mk(8) > mk(2) && mk(2) > mk(0)) {
		t.Errorf("connectivity: salience must rise with link density (0=%.4f 2=%.4f 8=%.4f)", mk(0), mk(2), mk(8))
	}
}

// Centrality vs recency. (a) weight-robust: an old *central* item outscores an old
// *isolated* one — connectivity is importance independent of age. (b) DESIGN-INTENT:
// a maximally-central item outscores a recent-but-isolated one — structural centrality
// must be able to beat pure recency, which is the whole reason the Hebbian graph feeds
// salience. (b) holds because we weight links (0.20) above canonical recency (0.10).
func TestPhenomenon_CentralityVsRecency(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	oldCentral := phBase(now)
	oldCentral.LinkCount = 8
	oldCentral.CanonicalDate = now.AddDate(0, 0, -365)
	oldIsolated := phBase(now)
	oldIsolated.CanonicalDate = now.AddDate(0, 0, -365)
	if ComputeSalience(oldCentral) <= ComputeSalience(oldIsolated) {
		t.Errorf("(a) old central must beat old isolated (%.4f ≤ %.4f)",
			ComputeSalience(oldCentral), ComputeSalience(oldIsolated))
	}
	recentIsolated := phBase(now)
	recentIsolated.CanonicalDate = now // maximally recent
	if ComputeSalience(oldCentral) <= ComputeSalience(recentIsolated) {
		t.Errorf("(b) centrality must be able to outweigh pure recency (central old %.4f ≤ recent isolated %.4f)",
			ComputeSalience(oldCentral), ComputeSalience(recentIsolated))
	}
}

// Protection of commitments: a deliberately flagged item (standing decision / open
// contradiction / pin / active project) carries a salience floor — it is shielded
// from fading to nothing even with no access, links, or recency.
func TestPhenomenon_FlagProtection(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	flagged := phBase(now)
	flagged.Flag = true
	if got := ComputeSalience(flagged); got < salWFlag-1e-9 {
		t.Errorf("flag protection: a flagged item must score ≥ %.2f (got %.4f)", salWFlag, got)
	}
	if ComputeSalience(flagged) <= ComputeSalience(phBase(now)) {
		t.Error("flag must lift salience strictly above an identical unflagged item")
	}
}
