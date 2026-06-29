package retrieval

import (
	"math"
	"testing"
	"time"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestComputeSalience_Golden(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	// A maximally-hot item: frequently + just accessed, well-linked, flagged, fresh.
	// fFreq=ln(11)/ln(21)=0.7877, fAccess=1, fLink=5/8=0.625, fFlag=1, fCanon=1
	// soft = .25*.7877 + .30*1 + .20*.625 + .15*1 + .10*1 = 0.8719
	hot := ComputeSalience(SalienceSignals{
		HitCount: 10, LastAccessed: now, IngestedAt: now, LinkCount: 5, Flag: true,
		CanonicalDate: now, Now: now,
	})
	if !approx(hot, 0.8719, 0.005) {
		t.Errorf("hot salience = %.4f, want ~0.8719", hot)
	}

	// A cold item: never accessed, no links, no flag, an old canonical date.
	// only fCanon contributes: exp(-ln2*400/180)=0.2143 → soft = .10*0.2143 = 0.0214
	cold := ComputeSalience(SalienceSignals{
		HitCount: 0, LinkCount: 0, Flag: false,
		CanonicalDate: now.AddDate(0, 0, -400), IngestedAt: now.AddDate(0, 0, -400), Now: now,
	})
	if !approx(cold, 0.0214, 0.003) {
		t.Errorf("cold salience = %.4f, want ~0.0214", cold)
	}
	if cold >= hot {
		t.Errorf("cold (%.4f) should be far below hot (%.4f)", cold, hot)
	}

	// Salience is bounded to [0,1].
	if hot < 0 || hot > 1 || cold < 0 || cold > 1 {
		t.Errorf("salience out of [0,1]: hot=%.4f cold=%.4f", hot, cold)
	}
}

func TestComputeSalience_Monotonic(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	base := SalienceSignals{LastAccessed: now, IngestedAt: now, Now: now}

	// More citations → not less salient.
	few := base
	few.HitCount = 1
	many := base
	many.HitCount = 20
	if ComputeSalience(many) <= ComputeSalience(few) {
		t.Errorf("more hits should raise salience: few=%.4f many=%.4f",
			ComputeSalience(few), ComputeSalience(many))
	}

	// A more recent access → not less salient.
	recent := base
	recent.HitCount = 5
	recent.LastAccessed = now
	stale := base
	stale.HitCount = 5
	stale.LastAccessed = now.AddDate(0, 0, -60)
	if ComputeSalience(recent) <= ComputeSalience(stale) {
		t.Errorf("more recent access should raise salience: recent=%.4f stale=%.4f",
			ComputeSalience(recent), ComputeSalience(stale))
	}

	// The flag alone contributes its full weight (0.15).
	flagged := SalienceSignals{Flag: true, Now: now}
	if got := ComputeSalience(flagged); !approx(got, salWFlag, 0.0001) {
		t.Errorf("flag-only salience = %.4f, want %.4f", got, salWFlag)
	}
}

func TestComputeStrength(t *testing.T) {
	// Freshly accessed → strength ≈ 1 regardless of salience.
	if s := ComputeStrength(0.9, 0); !approx(s, 1.0, 1e-9) {
		t.Errorf("strength at age 0 = %.6f, want 1.0", s)
	}

	// Higher salience decays slower: at the same age, more-salient retains more.
	lowSal := ComputeStrength(0.0, 30)
	highSal := ComputeStrength(1.0, 30)
	if highSal <= lowSal {
		t.Errorf("higher salience should decay slower: low=%.4f high=%.4f", lowSal, highSal)
	}

	// A low-salience, long-untouched item falls below the eviction floor.
	stale := ComputeStrength(0.05, 365)
	if stale >= EvictionFloor {
		t.Errorf("stale low-salience strength = %.6f, want < floor %.2f", stale, EvictionFloor)
	}

	// salience=0, age = base half-life (14 d) → strength = 0.5 by construction.
	if s := ComputeStrength(0, forgetHL0); !approx(s, 0.5, 0.01) {
		t.Errorf("strength at one base half-life = %.4f, want ~0.5", s)
	}
}

func TestAccessAgeDays(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	// Accessed 10 days ago → ~10.
	got := SalienceSignals{LastAccessed: now.AddDate(0, 0, -10), Now: now}.AccessAgeDays()
	if !approx(got, 10, 0.01) {
		t.Errorf("accessAge (accessed) = %.4f, want 10", got)
	}
	// Never accessed → falls back to ingestion age.
	got = SalienceSignals{IngestedAt: now.AddDate(0, 0, -5), Now: now}.AccessAgeDays()
	if !approx(got, 5, 0.01) {
		t.Errorf("accessAge (never accessed) = %.4f, want 5 (ingestion age)", got)
	}
	// Never accessed, ancient ingestion → capped at 365.
	got = SalienceSignals{IngestedAt: now.AddDate(-3, 0, 0), Now: now}.AccessAgeDays()
	if !approx(got, 365, 0.01) {
		t.Errorf("accessAge (ancient) = %.4f, want capped 365", got)
	}
}
