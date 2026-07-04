package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/tools"
	"github.com/rs/zerolog"
)

// TestPlanVoieA_Routing encodes the pre-match routing for the plan's fixtures: a VAT NUMBER
// question → identifier lane; a VAT AMOUNT (montant … à payer) → figure lane; a bare DUNS →
// identifier; an exploratory query → no lane (voie B). DATA-driven, no per-type router.
func TestPlanVoieA_Routing(t *testing.T) {
	// subjectFn stub: only "acme" is a known named subject.
	subjectFn := func(q string) string {
		if strings.Contains(strings.ToLower(q), "acme") {
			return "acme"
		}
		return ""
	}
	cases := []struct {
		name      string
		query     string
		wantOK    bool
		wantLane  string
		wantEnt   string
		wantLabel string // identifier lane only
	}{
		{"vat number → identifier", "quel est mon numéro de TVA ?", true, laneIdentifier, "moi", "VAT number"},
		{"vat amount → figure", "montant de la dernière TVA à payer", true, laneFigure, "moi", ""},
		{"duns → identifier", "mon DUNS", true, laneIdentifier, "moi", "DUNS"},
		{"named subject figure", "le montant de TVA à payer de Acme", true, laneFigure, "acme", ""},
		{"dosage → figure", "what's my Amoxicillin dose?", true, laneFigure, "moi", ""},
		{"meeting with party → meeting", "what time is my meeting with Acme?", true, laneMeeting, "Acme", ""},
		{"meeting no party → voie B", "when is my meeting?", false, "", "", ""},
		{"exploratory → voie B", "résume ma semaine", false, "", "", ""},
		{"bare tax word → voie B", "explique-moi comment fonctionne la TVA", false, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := planVoieA(tc.query, subjectFn)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (plan %+v)", ok, tc.wantOK, plan)
			}
			if !ok {
				return
			}
			if plan.lane != tc.wantLane {
				t.Errorf("lane = %q, want %q", plan.lane, tc.wantLane)
			}
			if plan.entity != tc.wantEnt {
				t.Errorf("entity = %q, want %q", plan.entity, tc.wantEnt)
			}
			if tc.wantLane == laneIdentifier && plan.label != tc.wantLabel {
				t.Errorf("label = %q, want %q", plan.label, tc.wantLabel)
			}
		})
	}
}

// TestComposeVoieAAnswer_TemplatedByEngine proves the engine composes a clean English sentence from
// the value + context + source, and — critically — invents NO value on a decline.
func TestComposeVoieAAnswer_TemplatedByEngine(t *testing.T) {
	t.Run("identifier high", func(t *testing.T) {
		got := composeVoieAAnswer(&DeterminedAnswerEvent{
			Label: "VAT number", Subject: "you", Value: "BE 1021.234.567", Confidence: "high",
			Sources: []DeterminedAnswerSource{{ContentID: "c1", Title: "VAT certificate"}},
		})
		if !strings.Contains(got, "BE 1021.234.567") || !strings.HasPrefix(got, "Your VAT number is ") {
			t.Errorf("unexpected: %q", got)
		}
		if !strings.Contains(got, "VAT certificate") {
			t.Errorf("missing source: %q", got)
		}
	})
	t.Run("figure high — period folded into prose", func(t *testing.T) {
		got := composeVoieAAnswer(&DeterminedAnswerEvent{
			Label: "VAT to pay · Q3 2026", Subject: "you", Value: "7 421,85 €", Confidence: "high",
		})
		if !strings.Contains(got, "7 421,85 €") || !strings.Contains(got, "VAT to pay for Q3 2026") {
			t.Errorf("unexpected: %q", got)
		}
	})
	t.Run("dosage high — value + frequency + supersession note", func(t *testing.T) {
		got := composeVoieAAnswer(&DeterminedAnswerEvent{
			Label: "Warfarin dose", Subject: "you", Value: "10 mg, 1×/day", Confidence: "high",
			Note: "Previously 5 mg — updated.",
		})
		if !strings.Contains(got, "Your Warfarin dose is 10 mg, 1×/day") {
			t.Errorf("dose sentence wrong: %q", got)
		}
		if !strings.Contains(got, "Previously 5 mg — updated.") {
			t.Errorf("supersession note missing: %q", got)
		}
	})
	t.Run("decline invents no value", func(t *testing.T) {
		got := composeVoieAAnswer(&DeterminedAnswerEvent{
			Confidence: "none", Message: "No verified value — I don't have a confirmed one on record for you.",
		})
		if strings.ContainsAny(got, "0123456789") {
			t.Errorf("decline must contain no number: %q", got)
		}
	})
}

// stubValueTool is a canned engine resolver for the handler-level Voie A test.
type stubValueTool struct{ resp any }

func (s stubValueTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(s.resp)
}

// llmMustNotBeCalled fails the test if the LLM backend is ever hit — the proof that Voie A skips
// the LLM entirely (P=0 by construction).
func llmMustNotBeCalled(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("LLM was called on a Voie A turn — the engine answer must skip the LLM")
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

// TestServeVoieA_IdentifierCutLLM is the core barrier: « quel est mon numéro de TVA » is answered
// by the ENGINE (value on the wire as a determined_answer card + an engine-composed, simulated-
// streamed sentence) with the LLM NEVER called.
func TestServeVoieA_IdentifierCutLLM(t *testing.T) {
	voieAStreamDelay = 0 // instant stream for the test
	srv := llmMustNotBeCalled(t)
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetVoieATools(
		stubValueTool{resp: tools.LookupResponse{Type: "enterprise_number", Label: "VAT number",
			Subject: "you", Value: "1021", Tier: fact.TierHigh}},
		stubValueTool{resp: tools.FigureResponse{Tier: fact.TierNone}},
	)

	rec := serveChat(t, handler, "quel est mon numéro de TVA ?")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	card, prose := collectDetermined(t, rec.Body.String())
	if card == nil || card.Value != "1021" || card.Confidence != "high" {
		t.Fatalf("engine card missing/wrong: %+v", card)
	}
	if !strings.Contains(prose, "1021") || !strings.Contains(prose, "VAT number") {
		t.Errorf("engine-composed prose missing the value/label: %q", prose)
	}
}

// TestServeVoieA_FigureFixesRAG is the 357 € fix: « montant de la dernière TVA à payer » is answered
// from F1's determined figure (value + period + direction + source), engine-composed — NOT a RAG
// number, and the LLM is never called.
func TestServeVoieA_FigureFixesRAG(t *testing.T) {
	voieAStreamDelay = 0
	srv := llmMustNotBeCalled(t)
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetVoieATools(
		stubValueTool{resp: tools.LookupResponse{Tier: fact.TierNone}},
		stubValueTool{resp: tools.FigureResponse{Label: "VAT to pay · Q3 2026", Subject: "you",
			Value: "7 421,85 €", Unit: "EUR", Period: "2026-Q3", Direction: "payable", Tier: fact.TierHigh,
			Sources: []fact.Source{{ContentID: "vat-q3", Title: "VAT return Q3"}}}},
	)

	rec := serveChat(t, handler, "montant de la dernière TVA à payer")
	card, prose := collectDetermined(t, rec.Body.String())
	if card == nil || card.Value != "7 421,85 €" || card.Confidence != "high" {
		t.Fatalf("engine figure card missing/wrong: %+v", card)
	}
	if !strings.Contains(prose, "7 421,85 €") || !strings.Contains(prose, "VAT to pay for Q3 2026") {
		t.Errorf("engine-composed figure prose wrong: %q", prose)
	}
}

// TestServeVoieA_DeclineNoValue: an identifier the engine could not confirm → honest decline (no
// value), still on Voie A (never leaks to RAG to invent one).
func TestServeVoieA_DeclineNoValue(t *testing.T) {
	voieAStreamDelay = 0
	srv := llmMustNotBeCalled(t)
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetVoieATools(
		stubValueTool{resp: tools.LookupResponse{Type: "iban", Label: "IBAN", Subject: "you",
			Tier: fact.TierNone, Reason: "uncorroborated_candidate"}},
		stubValueTool{resp: tools.FigureResponse{Tier: fact.TierNone}},
	)

	rec := serveChat(t, handler, "quel est mon IBAN ?")
	card, prose := collectDetermined(t, rec.Body.String())
	if card == nil || card.Confidence != "none" || card.Value != "" {
		t.Fatalf("decline card should carry no value: %+v", card)
	}
	if strings.ContainsAny(prose, "0123456789") {
		t.Errorf("decline prose must invent no number: %q", prose)
	}
}

// TestServeVoieA_ExploratoryFallsToVoieB: an exploratory query is NOT pre-matched, so Voie A does
// not fire — the turn reaches the LLM path (here the mock would be called; we assert Voie A stayed
// out by seeing no determined_answer card and the LLM stub being hit).
func TestServeVoieA_ExploratoryFallsToVoieB(t *testing.T) {
	voieAStreamDelay = 0
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"Voici ta semaine.\"},\"finish_reason\":\"stop\"}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	handler := NewRAGChatHandler(createMockLLMClient(srv.URL), nil, nil, DefaultRAGConfig, zerolog.Nop())
	handler.SetVoieATools(
		stubValueTool{resp: tools.LookupResponse{Tier: fact.TierNone}},
		stubValueTool{resp: tools.FigureResponse{Tier: fact.TierNone}},
	)

	rec := serveChat(t, handler, "résume ma semaine")
	if !called {
		t.Error("exploratory query should reach the LLM (voie B), but it did not")
	}
	card, _ := collectDetermined(t, rec.Body.String())
	if card != nil {
		t.Errorf("exploratory query must not emit a determined_answer card: %+v", card)
	}
}

// serveChat runs one chat turn through the handler and returns the recorder.
func serveChat(t *testing.T, handler *RAGChatHandler, query string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": query}},
		"stream":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// collectDetermined parses the SSE stream into (the determined_answer card, the concatenated prose).
func collectDetermined(t *testing.T, body string) (*DeterminedAnswerEvent, string) {
	t.Helper()
	var card *DeterminedAnswerEvent
	var prose strings.Builder
	for _, e := range parseSSEEvents(t, body) {
		if e["type"] == "determined_answer" {
			b, _ := json.Marshal(e)
			var ev DeterminedAnswerEvent
			_ = json.Unmarshal(b, &ev)
			card = &ev
		}
		if d, ok := e["delta"].(string); ok {
			prose.WriteString(d)
		}
	}
	return card, prose.String()
}
