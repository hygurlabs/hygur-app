package retrieval

import (
	"math"
	"time"
)

// Memory-consolidation scoring — the deterministic salience + forgetting-curve
// engine of "Quand Hygur rêve" (DREAM_PLAN Phase 1, docs/DREAM_PLAN_ADDENDUM.md).
// Pure functions over primitive signals: no DB, no LLM (mirrors attention.go). The
// nightly consolidation pass (scheduler/consolidation.go) feeds them measured
// signals and persists the result to item_signals. Every weight/constant is a v1
// default, tunable after the shadow measurement on the home canary (addendum §7).

const (
	// Salience — weighted blend of access frequency, access recency, structural
	// links, the hard-flag, and (weakly) canonical recency. The soft weights sum to 1.
	salWFreq   = 0.25
	salWAccess = 0.30
	salWLink   = 0.20
	salWFlag   = 0.15
	salWCanon  = 0.10

	salHitSat   = 20.0  // hit_count at which the frequency term saturates
	salHLAccess = 14.0  // days — access-recency half-life
	salLinkSat  = 8.0   // link_count at which the links term saturates
	salHLCanon  = 180.0 // days — canonical-date half-life (tiebreaker only)
	salSurprise = 0.15  // surprise bonus weight (Phase C; surprise is 0 until then)

	// Forgetting curve (Ebbinghaus, salience-modulated): a memory's strength decays
	// from its last access; salience stretches the half-life so important memories
	// fade slower. Strength is the eviction "floor" used under budget (addendum §1.5).
	forgetHL0      = 14.0 // days — base half-life of the working (vector) tier
	forgetSalBoost = 4.0  // salience 0→1 stretches the half-life ×1→×5
)

// EvictionFloor is the strength below which an under-budget item may fade to cold
// (Phase E). Exposed for the consolidation pass.
const EvictionFloor = 0.10

// SalienceSignals are the measured, deterministic inputs to ComputeSalience. All
// ages are derived from Now. Zero times mean "unknown / never".
type SalienceSignals struct {
	HitCount      int
	LastAccessed  time.Time // zero = never cited
	IngestedAt    time.Time // created_at; the age basis when never accessed
	LinkCount     int
	Flag          bool // standing decision ∨ open contradiction ∨ pinned ∨ active project
	CanonicalDate time.Time
	Surprise      float64 // [0,1], 0 until Phase C
	Now           time.Time
}

func salClamp(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	default:
		return x
	}
}

func salDays(from, to time.Time) float64 {
	d := to.Sub(from).Hours() / 24.0
	if d < 0 {
		return 0
	}
	return d
}

// salExpDecay returns exp(-ln2 · ageDays / halfLife) ∈ (0,1], =1 at age 0.
func salExpDecay(ageDays, halfLife float64) float64 {
	if ageDays <= 0 {
		return 1
	}
	if halfLife <= 0 {
		return 0
	}
	return math.Exp(-math.Ln2 * ageDays / halfLife)
}

// AccessAgeDays is the age basis for the forgetting curve: days since the item was
// last cited, or since ingestion if it never was (capped at 365 so an ancient,
// never-touched item is old but not infinitely so).
func (s SalienceSignals) AccessAgeDays() float64 {
	if !s.LastAccessed.IsZero() {
		return salDays(s.LastAccessed, s.Now)
	}
	if s.IngestedAt.IsZero() {
		return 365
	}
	if d := salDays(s.IngestedAt, s.Now); d < 365 {
		return d
	}
	return 365
}

// ComputeSalience returns the composite importance score in [0,1] (addendum §1.2).
// Pure and deterministic given Now.
func ComputeSalience(s SalienceSignals) float64 {
	var fFreq float64
	if s.HitCount > 0 {
		fFreq = salClamp(math.Log1p(float64(s.HitCount)) / math.Log1p(salHitSat))
	}
	var fAccess float64
	if !s.LastAccessed.IsZero() {
		fAccess = salExpDecay(salDays(s.LastAccessed, s.Now), salHLAccess)
	}
	fLink := salClamp(float64(s.LinkCount) / salLinkSat)
	var fFlag float64
	if s.Flag {
		fFlag = 1
	}
	var fCanon float64
	if !s.CanonicalDate.IsZero() {
		fCanon = salExpDecay(salDays(s.CanonicalDate, s.Now), salHLCanon)
	}
	soft := salWFreq*fFreq + salWAccess*fAccess + salWLink*fLink + salWFlag*fFlag + salWCanon*fCanon
	return salClamp(soft + salSurprise*salClamp(s.Surprise))
}

// ComputeStrength returns the Ebbinghaus retention strength in (0,1]: the half-life
// is stretched by salience so important memories fade slower (addendum §1.5). Below
// EvictionFloor an under-budget item becomes eviction-eligible.
func ComputeStrength(salience, accessAgeDays float64) float64 {
	hlEff := forgetHL0 * (1 + salClamp(salience)*forgetSalBoost)
	tau := hlEff / math.Ln2
	if tau <= 0 {
		return 0
	}
	if accessAgeDays < 0 {
		accessAgeDays = 0
	}
	return math.Exp(-accessAgeDays / tau)
}
