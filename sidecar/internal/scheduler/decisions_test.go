package scheduler

import "testing"

func TestParseDecisionsVerbatimGate(t *testing.T) {
	source := "After review we decided to proceed with vendor A and to sign the lease by Friday. We are still weighing the budget."

	raw := "```json\n" + `[
	  {"statement": "Proceed with vendor A", "quote": "decided to proceed with vendor A"},
	  {"statement": "Sign the lease by Friday", "quote": "to sign the lease by Friday"},
	  {"statement": "Hire two engineers next quarter", "quote": "we will hire two engineers next quarter"}
	]` + "\n```"

	got := parseDecisions(raw, source)
	// The third is dropped: its quote is not verbatim in the source (anti-hallucination).
	if len(got) != 2 {
		t.Fatalf("want 2 gated decisions, got %d: %+v", len(got), got)
	}
	if got[0].Statement != "Proceed with vendor A" || got[1].Statement != "Sign the lease by Friday" {
		t.Errorf("unexpected statements: %+v", got)
	}
}

func TestParseDecisionsEmptyAndMalformed(t *testing.T) {
	if got := parseDecisions("no array here", "src"); got != nil {
		t.Errorf("no array → nil, got %+v", got)
	}
	if got := parseDecisions("[]", "src"); len(got) != 0 {
		t.Errorf("empty array → 0, got %+v", got)
	}
	// A candidate missing the quote, or with a blank statement, is dropped.
	raw := `[{"statement": "", "quote": "x"}, {"statement": "ok", "quote": ""}]`
	if got := parseDecisions(raw, "x ok"); len(got) != 0 {
		t.Errorf("incomplete candidates → 0, got %+v", got)
	}
}

func TestParseDecisionsPerItemCap(t *testing.T) {
	source := "a b c d e f g"
	raw := `[
	  {"statement":"s1","quote":"a"},
	  {"statement":"s2","quote":"b"},
	  {"statement":"s3","quote":"c"},
	  {"statement":"s4","quote":"d"},
	  {"statement":"s5","quote":"e"},
	  {"statement":"s6","quote":"f"},
	  {"statement":"s7","quote":"g"}
	]`
	got := parseDecisions(raw, source)
	if len(got) != decisionMaxPerItem {
		t.Fatalf("want cap %d, got %d", decisionMaxPerItem, len(got))
	}
}
