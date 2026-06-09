package contradict

import "testing"

const claimsSource = "We confirmed the contract is signed. Kickoff is scheduled for May 4. " +
	"I will send the invoice on Friday."

func TestParseClaims_GateDropsHallucinations(t *testing.T) {
	raw := `[
	  {"entity":"contract","attribute":"status","value":"signed","polarity":"affirm","quote":"the contract is signed"},
	  {"entity":"refund","attribute":"status","value":"approved","polarity":"affirm","quote":"the refund was approved"}
	]`
	got := parseClaims(raw, claimsSource)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1 (hallucinated quote must be dropped): %+v", len(got), got)
	}
	if got[0].Entity != "contract" || got[0].Polarity != "affirm" {
		t.Errorf("unexpected surviving claim: %+v", got[0])
	}
}

func TestParseClaims_FencedAndWhitespaceTolerant(t *testing.T) {
	// Markdown-fenced + surrounding prose, and a quote with different spacing/case
	// than the source — must still match (genuinely present), and parse cleanly.
	raw := "Sure! Here you go:\n```json\n" +
		`[{"entity":"invoice","attribute":"send date","value":"Friday","polarity":"affirm","quote":"I  will   SEND the invoice on friday"}]` +
		"\n```\n"
	got := parseClaims(raw, claimsSource)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (fenced + ws/case-tolerant): %+v", len(got), got)
	}
	if got[0].Attribute != "send date" {
		t.Errorf("attribute = %q", got[0].Attribute)
	}
}

func TestParseClaims_PolarityAndRequiredFields(t *testing.T) {
	raw := `[
	  {"entity":"kickoff","attribute":"date","value":"cancelled","polarity":"negate","quote":"Kickoff is scheduled for May 4"},
	  {"entity":"","attribute":"x","value":"y","quote":"the contract is signed"},
	  {"entity":"e","attribute":"","value":"y","quote":"the contract is signed"},
	  {"entity":"e","attribute":"a","value":"y","quote":""}
	]`
	got := parseClaims(raw, claimsSource)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (missing entity/attribute/quote dropped): %+v", len(got), got)
	}
	if got[0].Polarity != "negate" {
		t.Errorf("polarity = %q, want negate", got[0].Polarity)
	}
}

func TestParseClaims_Garbage(t *testing.T) {
	for _, raw := range []string{"", "not json at all", "[]", "[ {broken", "{\"not\":\"array\"}"} {
		if got := parseClaims(raw, claimsSource); len(got) != 0 {
			t.Errorf("parseClaims(%q) = %+v, want none", raw, got)
		}
	}
}

func TestParseClaims_CapsAtMax(t *testing.T) {
	// Build claimsMax+5 valid claims (same verbatim quote) → capped at claimsMax.
	one := `{"entity":"contract","attribute":"status","value":"signed","polarity":"affirm","quote":"the contract is signed"}`
	raw := "["
	for i := 0; i < claimsMax+5; i++ {
		if i > 0 {
			raw += ","
		}
		raw += one
	}
	raw += "]"
	if got := parseClaims(raw, claimsSource); len(got) != claimsMax {
		t.Fatalf("got %d claims, want cap %d", len(got), claimsMax)
	}
}
