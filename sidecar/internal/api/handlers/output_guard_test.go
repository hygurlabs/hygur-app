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

// noRetrieved is the empty R set — no retrieved excerpts this turn. A value not in D and not in R is
// INVENTÉ (the classic strip case).
func noRetrieved() provenanceValueSet { return newProvenanceValueSet() }

// excerptsR builds the R set from raw untrusted excerpt strings, mirroring what retrievedValueSet
// does over RAGSource excerpts.
func excerptsR(excerpts ...string) provenanceValueSet {
	srcs := make([]RAGSource, 0, len(excerpts))
	for _, e := range excerpts {
		srcs = append(srcs, RAGSource{Excerpt: e})
	}
	return retrievedValueSet(srcs)
}

// TestRAGChatHandler_OutputGuardStripsUnverifiedNumber drives the whole handler: the LLM answers
// (no tool call) with an INVENTED phone number, and the engine has no determined value for it
// (factsDB nil → empty set) and no retrieved excerpts. The guard must fire so the wrong number
// NEVER appears in any emitted SSE delta — the user sees the honest decline instead.
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

// PHONE CASE — the INVENTÉ barrier: the answer's prose carries an identifier-grade value (a phone
// number) the engine never determined and no excerpt contains. It is INVENTÉ → the guard must not
// show it → honest decline, and the wrong number must NOT survive anywhere in the emitted text.
func TestGuardAnswer_StripsInventedPhone(t *testing.T) {
	determined := determinedValueSet(ownerVATFacts()) // holds the VAT, NOT any phone
	answer := "Your phone number is 0470 12 34 56, from the contract."
	out, declined := guardAnswer(answer, determined, noRetrieved())
	if !declined {
		t.Fatal("an invented phone number must trigger the guard")
	}
	if strings.Contains(out, "0470") || strings.Contains(out, "047012") {
		t.Errorf("the wrong number leaked into the guarded answer: %q", out)
	}
	if out != unverifiedIdentifierDecline {
		t.Errorf("guard = %q, want the honest decline", out)
	}
}

// PHONE CASE, empty engine set + no excerpts — "my phone number" with NO determined phone (the live
// gap). Any identifier-grade value in the prose is INVENTÉ.
func TestGuardAnswer_EmptyDeterminedSetStripsInventedNumber(t *testing.T) {
	out, declined := guardAnswer("It is +32 470 123 456.", newProvenanceValueSet(), noRetrieved())
	if !declined {
		t.Fatal("with no determined value and no excerpt, an identifier-grade number must be stripped")
	}
	if strings.ContainsAny(out, "0123456789") {
		t.Errorf("a digit-run leaked through: %q", out)
	}
}

// VAT CASE — the DÉTERMINÉ branch: the verified value IS in the determined set → it is NOT stripped
// and NOT marked (the card and the prose agree; it is asserted as verified fact).
func TestGuardAnswer_KeepsVerifiedValueUnmarked(t *testing.T) {
	determined := determinedValueSet(ownerVATFacts())
	for _, answer := range []string{
		"Your VAT number is BE 1021.456.789 (from the BCE letter).",
		"It is 1021456789.",
	} {
		out, declined := guardAnswer(answer, determined, noRetrieved())
		if declined {
			t.Errorf("verified value wrongly stripped from %q → %q", answer, out)
		}
		if out != answer {
			t.Errorf("verified answer altered (should carry NO marker): %q → %q", answer, out)
		}
	}
}

// DUNS CASE — the tool-determined regression fixture (feat/dream-phase-ab). The founder's DUNS is
// engine-determined by the lookup_identifier TOOL and rendered as a determined_answer card (tier
// high) via the recall-floor path — but the determined-facts LAYER's type-discovery does NOT surface
// it, so it is ABSENT from the AssembleQueryFacts set. The guard's set must UNION the TOOL's verified
// value → the DUNS voiced in the prose is DÉTERMINÉ (allowed, unmarked).
func TestGuardAnswer_AllowsToolDeterminedValueAbsentFromLayer(t *testing.T) {
	const founderDUNS = "373021488" // 9-digit identifier-grade value, engine-determined by the TOOL

	// The LAYER set holds only the VAT (1021…). The DUNS is NOT in it — the type-discovery gap that
	// caused the regression. Precondition: layer-only + no excerpt → INVENTÉ → decline.
	layer := determinedValueSet(ownerVATFacts())
	answer := "Your DUNS number is 373021488 (from the D&B record)."
	if out, declined := guardAnswer(answer, layer, noRetrieved()); !declined || out != unverifiedIdentifierDecline {
		t.Fatalf("precondition: the layer-only set must NOT contain the tool DUNS "+
			"(declined=%v, out=%q) — the bug this fixture pins", declined, out)
	}

	// THE FIX: union the TOOL's verdict value into D. The DUNS is now DÉTERMINÉ → allowed, unmarked.
	union := addToolDeterminedValue(layer, founderDUNS)
	out, declined := guardAnswer(answer, union, noRetrieved())
	if declined {
		t.Errorf("tool-determined DUNS wrongly declined: %q → %q", answer, out)
	}
	if out != answer {
		t.Errorf("tool-determined answer altered: %q → %q", answer, out)
	}
}

// TJM 850 CASE — the live confident-wrong fixture. « mon TJM TARA » → the LLM asserts "850 €". 850
// IS in a document ("My daily rate is 850 EUR/day", a NON-TARA rate) but is NOT determined for TARA.
// It must be RETROUVÉ → kept but MARKED unverified/from-a-document — NEVER asserted as the verified
// "your TARA TJM is 850". Confident-wrong neutralized.
func TestGuardAnswer_RetrievedTJMMarkedUnverified(t *testing.T) {
	determined := determinedValueSet(ownerVATFacts()) // no 850 determined for TARA
	retrieved := excerptsR("My daily rate is 850 EUR/day for the standard engagement.")
	answer := "Your TARA daily rate (TJM) is 850 €."
	out, declined := guardAnswer(answer, determined, retrieved)
	if declined {
		t.Fatalf("a retrieved amount must be KEPT (not declined): %q", out)
	}
	if !strings.Contains(out, "850") {
		t.Errorf("the retrieved value must survive in the answer: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "not engine-verified") || !strings.Contains(strings.ToLower(out), "documents") {
		t.Errorf("the answer must carry the unverified/from-a-document marker: %q", out)
	}
}

// CAR-CHARGE LIST CASE — a legit retrieval the founder WANTS: several amounts pulled from ChargePoint
// receipt emails. None is engine-determined, all are in the excerpts → RETROUVÉ → KEPT but marked
// "from your documents" — NOT stripped.
func TestGuardAnswer_RetrievedCarChargeListKeptAndMarked(t *testing.T) {
	determined := newProvenanceValueSet()
	retrieved := retrievedValueSet([]RAGSource{
		{MailSubject: "ChargePoint receipt: 12.50 EUR", Excerpt: "Session on 2026-06-30 — amount charged 12,50 €."},
		{Excerpt: "ChargePoint session — total 8.30 EUR."},
		{Excerpt: "Home charging top-up: 21,40 € billed."},
	})
	answer := "Your recent car charges are 12,50 €, 8,30 €, and 21,40 €."
	out, declined := guardAnswer(answer, determined, retrieved)
	if declined {
		t.Fatalf("a legit retrieved car-charge list must be KEPT, not stripped: %q", out)
	}
	for _, amt := range []string{"12,50", "8,30", "21,40"} {
		if !strings.Contains(out, amt) {
			t.Errorf("retrieved amount %q dropped from the answer: %q", amt, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "documents") {
		t.Errorf("the car-charge list must be marked as from-your-documents: %q", out)
	}
}

// DETERMINED FIGURE CASE — a determined VAT amount (voie A / injected as a claim) → DÉTERMINÉ →
// verified, NO marker even though it is a monetary figure.
func TestGuardAnswer_DeterminedFigureUnmarked(t *testing.T) {
	facts := []retrieval.DeterminedFacts{{
		Subject: retrieval.EngramSubject{Norm: "owner", Type: "person"},
		IsOwner: true,
		Claims:  []retrieval.EngramClaim{{Attribute: "vat_payable", Value: "7421.85", State: "corroborated"}},
	}}
	determined := determinedValueSet(facts)
	// Even with the SAME amount present in an excerpt, DÉTERMINÉ wins → no marker.
	retrieved := excerptsR("VAT payable 7 421,85 € for Q1.")
	answer := "Your VAT payable for Q1 is 7 421,85 €."
	out, declined := guardAnswer(answer, determined, retrieved)
	if declined {
		t.Fatalf("a determined figure must not be declined: %q", out)
	}
	if out != answer {
		t.Errorf("a determined figure must be unmarked/unaltered: %q → %q", answer, out)
	}
}

// CONFABULATED FIGURE CASE — a monetary amount in NO source (not determined, not retrieved) is
// INVENTÉ → stripped/declined, exactly like an invented identifier.
func TestGuardAnswer_ConfabulatedFigureStripped(t *testing.T) {
	out, declined := guardAnswer("Your last VAT was 357 €.", newProvenanceValueSet(), noRetrieved())
	if !declined {
		t.Fatal("a confabulated amount in no source must be stripped")
	}
	if strings.Contains(out, "357") {
		t.Errorf("the confabulated amount leaked: %q", out)
	}
}

// CALIBRATION — legitimate non-answer numbers must survive untouched AND unmarked, even with empty D
// and R: a monetary-looking count, a date, counts, percentages, a year. Amounts here carry NO
// currency unit where they are prose counts, so the figure detector ignores them; identifier-grade
// values are absent. (A true currency amount would be classified — that is the point.)
func TestGuardAnswer_KeepsLegitimateProseNumbers(t *testing.T) {
	cases := []string{
		"The meeting is on 2026-07-03 at 14:30.",      // date + time
		"You have 3 invoices and 12 receipts.",        // counts
		"Revenue grew 20% this quarter, up from 15%.", // percentages
		"The company was founded in 2018.",            // year
		"That is about 6 sessions over 3 months.",     // counts
	}
	for _, answer := range cases {
		out, declined := guardAnswer(answer, newProvenanceValueSet(), noRetrieved())
		if declined {
			t.Errorf("legit prose number wrongly stripped: %q → %q", answer, out)
		}
		if out != answer {
			t.Errorf("legit answer altered/marked: %q → %q", answer, out)
		}
	}
}

// Non-identifier Q&A with no numbers at all is a pass-through.
func TestGuardAnswer_PlainProseUnaffected(t *testing.T) {
	answer := "Based on your notes, the next step is to review the draft and send it back."
	out, declined := guardAnswer(answer, newProvenanceValueSet(), noRetrieved())
	if declined || out != answer {
		t.Errorf("plain prose must pass through untouched: %q → %q (declined=%v)", answer, out, declined)
	}
}

// determinedValueSet must fold identity values, their raw forms, and claim values through the
// identifier normalizer (and, for amounts, the figure normalizer) so a cosmetically-formatted value
// in the prose matches.
func TestDeterminedValueSet_NormalizesAllSources(t *testing.T) {
	subjects := []retrieval.DeterminedFacts{{
		Subject:  retrieval.EngramSubject{Norm: "owner"},
		IsOwner:  true,
		Identity: []retrieval.EngramIdentifier{{Value: "1021456789", Raw: "BE 1021.456.789"}},
		Claims:   []retrieval.EngramClaim{{Attribute: "iban", Value: "BE68 5390 0754 7034"}},
	}}
	set := determinedValueSet(subjects)
	for _, want := range []string{"1021456789", "be1021456789", "be68539007547034"} {
		if !set.id[want] {
			t.Errorf("determined id set missing normalized value %q; got %v", want, set.id)
		}
	}
}
