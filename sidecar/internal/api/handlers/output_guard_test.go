package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/rs/zerolog"
)

// TestRAGChatHandler_OutputGuardStripsUnverifiedNumber drives the whole handler: the LLM answers
// (no tool call) with an INVENTED phone number, and the engine has no determined value for it
// (factsDB nil → empty set). The guard must fire so the wrong number NEVER appears in any emitted
// SSE delta — the user sees the honest decline instead.
func TestRAGChatHandler_OutputGuardStripsUnverifiedNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(s string) { _, _ = w.Write([]byte("data: " + s + "\n\n")); flusher.Flush() }
		write(`{"id":"1","choices":[{"delta":{"content":"Your phone number is 0470 12 34 56."},"finish_reason":"stop"}]}`)
		write("[DONE]")
	}))
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())

	reqBody := `{"messages":[{"role":"user","content":"what is my phone number?"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "0470") || strings.Contains(body, "047012") {
		t.Errorf("the invented number reached the wire:\n%s", body)
	}
	var prose strings.Builder
	for _, e := range parseSSEEvents(t, body) {
		if d, ok := e["delta"].(string); ok {
			prose.WriteString(d)
		}
	}
	if prose.String() != unverifiedIdentifierDecline {
		t.Errorf("emitted prose = %q, want the honest decline", prose.String())
	}
}

// ownerVATFacts is a determined-facts set carrying the owner's VAT/enterprise number "1021…" — the
// verified value that MAY appear in the prose (it is also the card's value).
func ownerVATFacts() []retrieval.DeterminedFacts {
	return []retrieval.DeterminedFacts{{
		Subject: retrieval.EngramSubject{Norm: "owner", Type: "person"},
		IsOwner: true,
		Identity: []retrieval.EngramIdentifier{
			{Type: "enterprise_number", Label: "enterprise number",
				Value: "1021456789", Raw: "BE 1021.456.789", Tier: "high",
				Sources: []fact.Source{{ContentID: "c1", Title: "BCE letter"}}},
		},
	}}
}

// PHONE CASE — the core barrier: the answer's prose carries an identifier-grade value (a phone
// number) the engine never determined. It is unverified → the guard must not show it → honest
// decline, and the wrong number must NOT survive anywhere in the emitted text.
func TestGuardAnswer_StripsUnverifiedPhone(t *testing.T) {
	determined := determinedValueSet(ownerVATFacts()) // holds the VAT, NOT any phone
	answer := "Your phone number is 0470 12 34 56, from the contract."
	out, declined := guardAnswer(answer, determined)
	if !declined {
		t.Fatal("an unverified phone number must trigger the guard")
	}
	if strings.Contains(out, "0470") || strings.Contains(out, "047012") {
		t.Errorf("the wrong number leaked into the guarded answer: %q", out)
	}
	if out != unverifiedIdentifierDecline {
		t.Errorf("guard = %q, want the honest decline", out)
	}
}

// PHONE CASE, empty engine set — "my phone number" with NO determined phone (the live gap). Any
// identifier-grade value in the prose is unverified.
func TestGuardAnswer_EmptyDeterminedSetStripsInventedNumber(t *testing.T) {
	out, declined := guardAnswer("It is +32 470 123 456.", nil)
	if !declined {
		t.Fatal("with no determined value, an identifier-grade number must be stripped")
	}
	if strings.ContainsAny(out, "0123456789") {
		t.Errorf("a digit-run leaked through: %q", out)
	}
}

// VAT CASE — the verified value IS in the determined set → it is NOT stripped from the prose (the
// card and the prose agree).
func TestGuardAnswer_KeepsVerifiedValue(t *testing.T) {
	determined := determinedValueSet(ownerVATFacts())
	for _, answer := range []string{
		"Your VAT number is BE 1021.456.789 (from the BCE letter).",
		"It is 1021456789.",
	} {
		out, declined := guardAnswer(answer, determined)
		if declined {
			t.Errorf("verified value wrongly stripped from %q → %q", answer, out)
		}
		if out != answer {
			t.Errorf("verified answer altered: %q → %q", answer, out)
		}
	}
}

// CALIBRATION — legitimate non-identifier numbers must survive untouched: a monetary amount, a
// date, a count, a percentage, a year. All sit below the value-grade floor (≥9 digits / IBAN /
// code), so the guard leaves them alone even with an empty determined set.
func TestGuardAnswer_KeepsLegitimateNumbers(t *testing.T) {
	cases := []string{
		"The invoice total is 1 234,56 € including VAT.", // monetary amount
		"The meeting is on 2026-07-03 at 14:30.",         // date + time
		"You have 3 invoices and 12 receipts.",           // counts
		"Revenue grew 20% this quarter, up from 15%.",    // percentages
		"The company was founded in 2018.",               // year
		"That is about 4500 euros over 6 months.",        // amount + count
	}
	for _, answer := range cases {
		out, declined := guardAnswer(answer, nil)
		if declined {
			t.Errorf("legit non-identifier number wrongly stripped: %q → %q", answer, out)
		}
		if out != answer {
			t.Errorf("legit answer altered: %q → %q", answer, out)
		}
	}
}

// Non-identifier Q&A with no numbers at all is a pass-through.
func TestGuardAnswer_PlainProseUnaffected(t *testing.T) {
	answer := "Based on your notes, the next step is to review the draft and send it back."
	out, declined := guardAnswer(answer, nil)
	if declined || out != answer {
		t.Errorf("plain prose must pass through untouched: %q → %q (declined=%v)", answer, out, declined)
	}
}

// determinedValueSet must fold identity values, their raw forms, and claim values through
// identifier.Normalize so a cosmetically-formatted value in the prose matches.
func TestDeterminedValueSet_NormalizesAllSources(t *testing.T) {
	subjects := []retrieval.DeterminedFacts{{
		Subject:  retrieval.EngramSubject{Norm: "owner"},
		IsOwner:  true,
		Identity: []retrieval.EngramIdentifier{{Value: "1021456789", Raw: "BE 1021.456.789"}},
		Claims:   []retrieval.EngramClaim{{Attribute: "iban", Value: "BE68 5390 0754 7034"}},
	}}
	set := determinedValueSet(subjects)
	for _, want := range []string{"1021456789", "be1021456789", "be68539007547034"} {
		if !set[want] {
			t.Errorf("determined set missing normalized value %q; got %v", want, set)
		}
	}
}
