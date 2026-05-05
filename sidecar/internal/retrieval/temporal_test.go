package retrieval

import (
	"testing"
	"time"
)

func TestIsCurrentStateQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		// Positive — English
		{"what is the latest VAT?", true},
		{"my current balance", true},
		{"recent invoices", true},
		{"what is due this month?", true},
		{"outstanding amounts", true},
		// Positive — French
		{"quel est le montant à payer ?", true},
		{"mon solde TVA", true},
		{"dernier rappel échéance", true},
		{"je suis redevable de combien ?", true},
		// Negative — historical or non-temporal
		{"how does VAT work in Belgium?", false},
		{"history of my account", false},
		{"comment fonctionne la TVA ?", false},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got := IsCurrentStateQuery(tc.query)
			if got != tc.want {
				t.Errorf("IsCurrentStateQuery(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		date time.Time
		want float64
		eps  float64
	}{
		{"today", now, 1.0, 0.001},
		{"1 day old", now.AddDate(0, 0, -1), 0.5, 0.001},
		{"30 days old", now.AddDate(0, 0, -30), 1.0 / 31.0, 0.001},
		{"future date treated as today", now.AddDate(0, 0, 5), 1.0, 0.001},
		{"zero date", time.Time{}, 0.0, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := recencyScore(tc.date, now)
			if abs(got-tc.want) > tc.eps {
				t.Errorf("got %f, want %f ±%f", got, tc.want, tc.eps)
			}
		})
	}
}

func TestApplyAdditiveScore_TodayBeatsOldHigherCosine(t *testing.T) {
	// Recency curve is steep (1/(1+days)): a doc from today should beat an
	// old high-cosine doc. The 90-day pre-filter handles the longer-tail
	// "5d ago vs 18mo" case at the candidate-pool level, before scoring.
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	// Today's doc, mediocre cosine 0.65
	recent := ApplyAdditiveScore(0.65, now, now, false)

	// 18-month-old doc, strong cosine 0.92 (the "high-relevance old doc"
	// scenario the user wants to fix)
	oldDate := now.AddDate(0, -18, 0)
	old := ApplyAdditiveScore(0.92, oldDate, now, false)

	if recent <= old {
		t.Errorf("expected today's doc (sem=0.65) to beat 18mo doc (sem=0.92): recent=%f old=%f", recent, old)
	}
}

func TestApplyAdditiveScore_WithinRecentWindow_RanksBySemantics(t *testing.T) {
	// Within a tight recency window (e.g. all candidates are <30 days old),
	// the recency component is similar across docs, so the semantic score
	// dominates the ranking. This is the desired behavior post-pre-filter.
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	// Two docs both ~14 days old; one more semantically relevant than the other.
	dateA := now.AddDate(0, 0, -14)
	dateB := now.AddDate(0, 0, -16)
	scoreA := ApplyAdditiveScore(0.85, dateA, now, false)
	scoreB := ApplyAdditiveScore(0.65, dateB, now, false)

	if scoreA <= scoreB {
		t.Errorf("within recent window, higher cosine (0.85, 14d) should beat lower (0.65, 16d): A=%f B=%f", scoreA, scoreB)
	}
}

func TestApplyAdditiveScore_TemporalMarker_AmplifiesRecencyWeight(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	recentDate := now.AddDate(0, 0, -5)
	oldDate := now.AddDate(0, -6, 0)

	// Without marker: 0.3 weight on recency
	recentNoMarker := ApplyAdditiveScore(0.65, recentDate, now, false)
	oldNoMarker := ApplyAdditiveScore(0.85, oldDate, now, false)
	gapNoMarker := recentNoMarker - oldNoMarker

	// With marker: 0.5 weight on recency
	recentWithMarker := ApplyAdditiveScore(0.65, recentDate, now, true)
	oldWithMarker := ApplyAdditiveScore(0.85, oldDate, now, true)
	gapWithMarker := recentWithMarker - oldWithMarker

	if gapWithMarker <= gapNoMarker {
		t.Errorf("temporal marker should widen the gap: noMarker=%f withMarker=%f", gapNoMarker, gapWithMarker)
	}
}

func TestApplyAdditiveScore_ZeroDate_DoesNotDominate(t *testing.T) {
	// A document with no canonical_date (zero time) should not get a recency
	// boost — recencyScore returns 0, so the final blend is sem*(1-Wr).
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	got := ApplyAdditiveScore(0.8, time.Time{}, now, false)
	want := 0.8 * 0.7 // sem*(1-0.3)
	if abs(got-want) > 0.001 {
		t.Errorf("got %f, want %f", got, want)
	}
}

func TestDetectQueryEntityType(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"show me the IBAN", "iban"},
		{"can you show me the account number?", "iban"},
		{"quel est le numéro de compte ?", "iban"},
		{"what is the amount due?", "amount"},
		{"montant à payer", "amount"},
		{"how much do I owe?", "amount"},
		{"combien je dois", "amount"},
		{"give me the communication reference", "communication"},
		{"référence pour le virement", "communication"},
		{"who sent this email?", ""},
		{"what is VAT?", ""},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got := detectQueryEntityType(tc.query)
			if got != tc.want {
				t.Errorf("detectQueryEntityType(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

func TestMetadataHasEntity(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		etype    string
		want     bool
	}{
		{"iban present as []string", map[string]any{"extracted_iban": []string{"BE22..."}}, "iban", true},
		{"iban present as []any (post-JSON)", map[string]any{"extracted_iban": []any{"BE22..."}}, "iban", true},
		{"iban absent", map[string]any{"other": "value"}, "iban", false},
		{"empty list does not count", map[string]any{"extracted_iban": []string{}}, "iban", false},
		{"amount present", map[string]any{"extracted_amounts": []string{"100 EUR"}}, "amount", true},
		{"communication present", map[string]any{"extracted_structured_comm": []any{"+++..."}}, "communication", true},
		{"unknown type returns false", map[string]any{"extracted_iban": []string{"X"}}, "unknown", false},
		{"nil metadata", nil, "iban", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := metadataHasEntity(tc.metadata, tc.etype)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
