package figure

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/store"
)

// BARRIER 1 — "Amoxicillin 500 mg, 3×/day" → a determined dose figure with unit AND frequency.
func TestExtract_dosageWithFrequency(t *testing.T) {
	text := "Prescription for Denis.\nAmoxicillin 500 mg, 3×/day for 7 days.\n"
	figs := Extract(text)
	var d *Figure
	for i := range figs {
		if figs[i].Label == "dose" {
			d = &figs[i]
		}
	}
	if d == nil {
		t.Fatalf("no dose figure extracted: %+v", figs)
	}
	if d.Medication != "amoxicillin" || d.Value != "500" || d.Unit != "mg" {
		t.Errorf("dose wrong: %+v", d)
	}
	if d.Frequency != "3×/day" {
		t.Errorf("frequency = %q; want 3×/day", d.Frequency)
	}
}

// BARRIER 2 — "Levothyroxine 50 mcg" → unit-aware (mcg, NOT mg), no frequency.
func TestExtract_dosageMicrograms(t *testing.T) {
	text := "Levothyroxine 50 mcg once daily."
	figs := Extract(text)
	if len(figs) != 1 || figs[0].Label != "dose" {
		t.Fatalf("expected 1 dose figure, got %+v", figs)
	}
	f := figs[0]
	if f.Unit != "mcg" || f.Value != "50" || f.Medication != "levothyroxine" {
		t.Errorf("dose wrong: %+v", f)
	}
	if f.Frequency != "1×/day" {
		t.Errorf("frequency = %q; want 1×/day (once daily)", f.Frequency)
	}
}

// BARRIER 5 — false friends: a weight ("72 kg"), a blood pressure ("120/80") and a heart rate
// ("HR 68") are NOT doses (no dosage unit) → nothing extracted.
func TestExtract_falseFriends(t *testing.T) {
	for _, text := range []string{
		"Weight 72 kg, stable.",
		"Blood pressure 120/80 mmHg.",
		"HR 68 bpm at rest.",
		"Patient is 180 cm tall.",
	} {
		if figs := Extract(text); len(figs) != 0 {
			t.Errorf("false friend %q extracted a figure: %+v", text, figs)
		}
	}
}

// BARRIER 6 — two meds, two doses in one document → two isolated dose nodes (no cross-mix).
func TestExtract_twoMedications(t *testing.T) {
	text := "Amoxicillin 500 mg three times a day.\nLevothyroxine 50 mcg once daily.\n"
	figs := Extract(text)
	byMed := map[string]Figure{}
	for _, f := range figs {
		if f.Label == "dose" {
			byMed[f.Medication] = f
		}
	}
	if len(byMed) != 2 {
		t.Fatalf("expected 2 distinct dose meds, got %+v", figs)
	}
	if byMed["amoxicillin"].Unit != "mg" || byMed["amoxicillin"].Value != "500" {
		t.Errorf("amoxicillin wrong: %+v", byMed["amoxicillin"])
	}
	if byMed["levothyroxine"].Unit != "mcg" || byMed["levothyroxine"].Value != "50" {
		t.Errorf("levothyroxine wrong: %+v", byMed["levothyroxine"])
	}
}

// ---- resolution (voie A traversal) ----

func doseStore(nodes []store.FigureNode) *fakeStore {
	return &fakeStore{nodes: nodes, persons: map[string][]string{"denis petit": {"denis petit"}}}
}

func doseNode(cid, med, value, unit, freq string, date time.Time) store.FigureNode {
	return store.FigureNode{ContentID: cid, EntityNorm: "denis petit", Label: "dose",
		Value: value, Raw: value, Unit: unit, Medication: med, Frequency: freq, Prox: 1, DocDate: date}
}

// BARRIER 1/3 (resolve) — a single dose resolves with value+unit+frequency, P=0.
func TestResolve_doseHighConfidence(t *testing.T) {
	fs := doseStore([]store.FigureNode{doseNode("p1", "amoxicillin", "500", "mg", "3×/day", time.Now())})
	res, err := Resolve(context.Background(), fs, "denis petit", "my Amoxicillin dose", "", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh || res.Value != "500" || res.Unit != "mg" || res.Frequency != "3×/day" {
		t.Fatalf("got %+v", res)
	}
	if res.Medication != "amoxicillin" {
		t.Errorf("medication = %q", res.Medication)
	}
}

// BARRIER 3 — a dose CHANGED across documents (5 mg → 10 mg by doc date): latest wins AND the old
// value is surfaced as a contradiction (supersession).
func TestResolve_doseSupersession(t *testing.T) {
	older := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	fs := doseStore([]store.FigureNode{
		doseNode("old", "warfarin", "5", "mg", "1×/day", older),
		doseNode("new", "warfarin", "10", "mg", "1×/day", newer),
	})
	res, err := Resolve(context.Background(), fs, "denis petit", "my Warfarin dose", "", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh || res.Value != "10" {
		t.Fatalf("latest should win with 10 mg, got %+v", res)
	}
	if len(res.Prior) != 1 || res.Prior[0].Value != "5" {
		t.Fatalf("prior (superseded) 5 mg not surfaced: %+v", res.Prior)
	}
	// the current value's source is the newer document
	if len(res.Sources) != 1 || res.Sources[0].ContentID != "new" {
		t.Errorf("source should be the newer doc: %+v", res.Sources)
	}
}

// BARRIER 4 — a medication that is absent → honest decline (no figure), never a guessed dose.
func TestResolve_doseAbsentDeclines(t *testing.T) {
	fs := doseStore([]store.FigureNode{doseNode("p1", "amoxicillin", "500", "mg", "3×/day", time.Now())})
	res, _ := Resolve(context.Background(), fs, "denis petit", "my Ibuprofen dose", "", "", ownerMatcher())
	// ibuprofen is not among the candidates; the single present med does not match the named one.
	// The query names a medication that isn't there → the amoxicillin dose must NOT be returned.
	if res.Tier == fact.TierHigh && res.Medication == "amoxicillin" {
		t.Fatalf("named Ibuprofen but returned the Amoxicillin dose: %+v", res)
	}
}

// BARRIER 6 (resolve) — two meds present, the query names ONE → only that one, no cross-mix.
func TestResolve_doseTwoMedsIsolation(t *testing.T) {
	fs := doseStore([]store.FigureNode{
		doseNode("a", "amoxicillin", "500", "mg", "3×/day", time.Now()),
		doseNode("l", "levothyroxine", "50", "mcg", "1×/day", time.Now()),
	})
	res, err := Resolve(context.Background(), fs, "denis petit", "my Levothyroxine dose", "", "", ownerMatcher())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != fact.TierHigh || res.Value != "50" || res.Unit != "mcg" {
		t.Fatalf("levothyroxine isolation failed: %+v", res)
	}
}

// Two meds present, the query names NONE → decline (ambiguous medication), never a guess.
func TestResolve_doseAmbiguousMedication(t *testing.T) {
	fs := doseStore([]store.FigureNode{
		doseNode("a", "amoxicillin", "500", "mg", "3×/day", time.Now()),
		doseNode("l", "levothyroxine", "50", "mcg", "1×/day", time.Now()),
	})
	res, _ := Resolve(context.Background(), fs, "denis petit", "what is my dose", "", "", ownerMatcher())
	if res.Tier != fact.TierNone || res.Reason != ReasonAmbiguousMedic {
		t.Errorf("got tier=%q reason=%q; want none / ambiguous_medication", res.Tier, res.Reason)
	}
}
