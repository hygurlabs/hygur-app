package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/figure"
	"github.com/hygur/sidecar/internal/labelfact"
	"github.com/hygur/sidecar/internal/session"
)

// Voie A — the slot-filling socle (SLOT_FILLING_PLAN §1, PROVENANCE_FIREWALL_PLAN §3). For a query
// that maps to a DETERMINED fact the engine has, the handler resolves it BEFORE any LLM round and
// COMPOSES the answer itself from a template + the determined value + context + source. The LLM is
// skipped entirely, so P(the LLM writes the value) = 0 BY CONSTRUCTION — there is no probabilistic
// stream to guard. On ambiguity / no determined value the engine declines honestly (no value
// invented). Voie B (RAG) is untouched: a query with no pre-match falls straight through.

// valueLookup is the narrow contract the Voie A pre-match calls — the deterministic engine tools
// (lookup_identifier / lookup_figure) satisfy it. A test seam: stubs stand in without a store.
type valueLookup interface {
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

// voieAStreamDelay is the per-word pause of the SIMULATED stream (§3): the SAFE, engine-composed
// answer is streamed word-by-word so it feels live, WITHOUT the LLM's probabilistic stream. Tests
// may set it to 0 to run instantly.
var voieAStreamDelay = 12 * time.Millisecond

const (
	laneIdentifier = "identifier"
	laneFigure     = "figure"
)

// voieAPlan is the deterministic pre-match verdict: which engine to call and with what args, all
// extracted from the query by the generic label normalizer (DATA, not a per-type router).
type voieAPlan struct {
	lane      string // laneIdentifier | laneFigure
	entity    string // "moi" (owner default / first-person) or a named subject norm
	label     string // display label passed to the tool (it re-normalizes)
	direction string // figure only — the raw query (the tool extracts the direction cue)
	period    string // figure only — the raw query (the tool extracts the period)
}

// firstPersonMarkers are the possessives/pronouns that pin a query to the OWNER even when a proper
// noun is also present ("le DUNS que Jean m'a envoyé" is still about me). Folded keys.
var firstPersonMarkers = map[string]bool{
	"mon": true, "ma": true, "mes": true, "my": true, "mine": true, "je": true, "moi": true,
}

// amountCues mark a FIGURE (a monetary quantity), not an identifier. DATA — a domain-agnostic set
// of "how much" words (EN + FR). A direction cue (à payer / remboursé / acompte) also counts.
var amountCues = map[string]bool{
	"montant": true, "montants": true, "amount": true, "amounts": true,
	"combien": true, "somme": true,
}

// idValueCues mark an IDENTIFIER-VALUE question. Generic cue words ("numéro", "number", "n°"…) PLUS
// the family-B acronym labels that ARE their own cue (a bare "DUNS"/"SIRET"/"IBAN" is a request for
// that identifier). DATA — so a bare tax word ("la TVA") does NOT trigger the identifier lane
// (that stays exploratory → voie B), while "mon numéro de TVA" / "mon DUNS" do.
var idValueCues = map[string]bool{
	"numero": true, "number": true, "no": true, "nr": true,
	"ref": true, "reference": true, "identifiant": true, "id": true,
	// self-cueing identifier terms
	"duns": true, "siret": true, "siren": true, "ein": true, "tin": true,
	"lei": true, "gln": true, "rna": true, "urssaf": true, "iban": true, "niss": true,
}

// idTypeDisplayLabel maps a canonical id_type to a human label for the answer + card. Presentation
// DATA only; the tool re-normalizes it back to the same canonical type. Fallback un-snake-cases.
var idTypeDisplayLabel = map[string]string{
	"enterprise_number": "VAT number",
	"national_number":   "national number",
	"iban":              "IBAN",
	"duns":              "DUNS",
	"siret":             "SIRET",
	"siren":             "SIREN",
	"ein":               "EIN",
	"tin":               "TIN",
	"lei":               "LEI",
	"gln":               "GLN",
	"rna":               "RNA",
	"urssaf":            "URSSAF",
}

func displayLabel(canon string) string {
	if d, ok := idTypeDisplayLabel[canon]; ok {
		return d
	}
	return strings.ReplaceAll(canon, "_", " ")
}

// foldQuery lowercases + strips accents and returns the alphabetic/numeric tokens, for cue matching.
func queryTokens(q string) []string {
	return strings.FieldsFunc(labelfact.Fold(q), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
}

func hasFirstPersonMarker(q string) bool {
	for _, t := range queryTokens(q) {
		if firstPersonMarkers[t] {
			return true
		}
	}
	return false
}

func hasAmountCue(q string) bool {
	for _, t := range queryTokens(q) {
		if amountCues[t] {
			return true
		}
	}
	return false
}

func hasIdentifierCue(q string) bool {
	if strings.Contains(q, "n°") || strings.Contains(q, "#") {
		return true
	}
	for _, t := range queryTokens(q) {
		if idValueCues[t] {
			return true
		}
	}
	return false
}

// planVoieA decides whether the query is a determined factual-value question and how to resolve it.
// subjectFn resolves a named subject deterministically ("" when none). It returns (plan, true) only
// when the query names a determined fact by label; otherwise (_, false) → the turn stays on voie B.
func planVoieA(query string, subjectFn func(string) string) (voieAPlan, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return voieAPlan{}, false
	}

	// Subject: the owner by default / first-person; a named subject only when the query names one
	// and is not first-person (so "mon X" is always me, "le X de Acme" is Acme).
	entity := "moi"
	if !hasFirstPersonMarker(q) {
		if subjectFn != nil {
			if subj := strings.TrimSpace(subjectFn(q)); subj != "" {
				entity = subj
			}
		}
	}

	// FIGURE lane takes precedence: a seeded figure label PLUS an amount or direction cue is
	// unambiguously a "how much" question (this is the 357 € fix — it never reaches RAG).
	if figLabel := figure.NormalizeFigureLabel(q); figLabel != "" {
		if hasAmountCue(q) || figure.NormalizeDirection(q) != "" {
			return voieAPlan{lane: laneFigure, entity: entity, label: q, direction: q, period: q}, true
		}
	}

	// IDENTIFIER lane: exactly ONE labelled identifier named, with an identifier-value cue. Several
	// labels, or a bare tax word with no cue, decline the lane (→ voie B).
	if ids := labelfact.DetectLabels(q); len(ids) == 1 && hasIdentifierCue(q) {
		return voieAPlan{lane: laneIdentifier, entity: entity, label: displayLabel(ids[0])}, true
	}
	return voieAPlan{}, false
}

// serveVoieA resolves the pre-matched fact via the engine, emits the authoritative value card,
// composes the answer itself, and simulated-streams it — the LLM is never called. Returns true when
// it handled the turn (answer OR honest decline). Returns false only on an unexpected tool error, so
// the caller can fall back to voie B.
func (h *RAGChatHandler) serveVoieA(ctx context.Context, plan voieAPlan, writeSSE func(any) error, req RAGChatRequest, query string, sessionCtx *session.SessionContext) bool {
	var tool valueLookup
	var toolName string
	var args map[string]any
	switch plan.lane {
	case laneIdentifier:
		tool, toolName = h.idTool, "lookup_identifier"
		args = map[string]any{"entity": plan.entity, "type": plan.label}
	case laneFigure:
		tool, toolName = h.figTool, "lookup_figure"
		args = map[string]any{"entity": plan.entity, "label": plan.label, "direction": plan.direction, "period": plan.period}
	default:
		return false
	}
	if tool == nil {
		return false
	}

	raw, err := json.Marshal(args)
	if err != nil {
		return false
	}
	result, err := tool.Execute(ctx, raw)
	if err != nil {
		h.logger.Debug().Err(err).Str("lane", plan.lane).Msg("voie A resolution failed — falling back to voie B")
		return false
	}
	evt, ok := determinedAnswerFromToolResult(toolName, result)
	if !ok {
		return false
	}

	// 1) The engine's value card — on the wire BEFORE any prose (cut-LLM-safe render). On a decline
	//    it carries NO value.
	_ = writeSSE(evt)

	// 2) The engine-COMPOSED answer text (the LLM never writes it).
	answer := composeVoieAAnswer(evt)

	// 3) Simulated stream: the SAFE answer, word-by-word.
	h.simulatedStream(ctx, writeSSE, answer)
	_ = writeSSE(map[string]any{"done": true})

	h.logger.Info().Str("lane", plan.lane).Str("confidence", evt.Confidence).
		Bool("declined", evt.Value == "").Msg("voie A: engine-composed factual answer (LLM skipped)")

	// Persistence + signals (minimal mirror of the voie B tail; no LLM, no memory extraction).
	var turnSources []RAGSource
	for _, s := range evt.Sources {
		turnSources = append(turnSources, RAGSource{ContentID: s.ContentID, Title: s.Title})
	}
	h.persistVoieA(req, query, answer, turnSources, sessionCtx)
	return true
}

// simulatedStream emits the composed answer as word deltas with a small pause, so the concatenation
// of the deltas reproduces the answer exactly (wire-compatible with the token stream). Respects
// context cancellation (a client disconnect stops it cleanly).
func (h *RAGChatHandler) simulatedStream(ctx context.Context, writeSSE func(any) error, text string) {
	if text == "" {
		return
	}
	words := strings.Split(text, " ")
	for i, w := range words {
		select {
		case <-ctx.Done():
			return
		default:
		}
		piece := w
		if i < len(words)-1 {
			piece += " "
		}
		if err := writeSSE(map[string]any{"delta": piece, "done": false}); err != nil {
			return
		}
		if voieAStreamDelay > 0 && i < len(words)-1 {
			timer := time.NewTimer(voieAStreamDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

// composeVoieAAnswer builds the clean English answer from the engine verdict — a TEMPLATE filled by
// the determined value + context + source. On a decline (no value) it returns the honest message,
// never a fabricated value.
func composeVoieAAnswer(evt *DeterminedAnswerEvent) string {
	if evt.Value == "" {
		if m := strings.TrimSpace(evt.Message); m != "" {
			return m
		}
		return unverifiedIdentifierDecline
	}
	subj := "Your "
	if s := strings.TrimSpace(evt.Subject); s != "" && !strings.EqualFold(s, "you") && !strings.EqualFold(s, "me") {
		subj = capitalizeFirst(s) + "'s "
	}
	head := strings.ReplaceAll(evt.Label, " · ", " for ")
	var sentence string
	if strings.EqualFold(evt.Confidence, "medium") {
		sentence = "I'm not fully certain, but " + lowerFirst(subj) + head + " is " + evt.Value + " — please double-check against the source."
	} else {
		sentence = subj + head + " is " + evt.Value + "."
	}
	if titles := determinedSourceTitles(evt.Sources); titles != "" {
		sentence += " (source: " + titles + ")"
	}
	return sentence
}

// determinedSourceTitles renders up to two source titles for the composed answer.
func determinedSourceTitles(sources []DeterminedAnswerSource) string {
	const max = 2
	parts := make([]string, 0, max)
	for _, s := range sources {
		if len(parts) >= max {
			break
		}
		t := strings.TrimSpace(s.Title)
		if t == "" {
			t = strings.TrimSpace(s.ContentID)
		}
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "; ")
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToLower(string(r[0])))[0]
	return string(r)
}

// persistVoieA mirrors the minimal post-turn persistence of the voie B tail: the assistant turn +
// its citations to the durable transcript, an access bump on cited items, and the session update
// for follow-ups. No memory extraction (a factual lookup rarely carries a durable memory, and voie
// A avoids the extra LLM call by design).
func (h *RAGChatHandler) persistVoieA(req RAGChatRequest, query, answer string, turnSources []RAGSource, sessionCtx *session.SessionContext) {
	if answer == "" {
		return
	}
	if req.SessionID != "" && h.chatStore != nil {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		h.persistAssistantTurn(pctx, req.SessionID, answer, turnSources)
		cancel()
	}
	if h.chatStore != nil && len(turnSources) > 0 {
		ids := make([]string, 0, len(turnSources))
		for _, s := range turnSources {
			if s.ContentID != "" {
				ids = append(ids, s.ContentID)
			}
		}
		if len(ids) > 0 {
			go func() {
				actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.chatStore.BumpItemAccess(actx, ids); err != nil {
					h.logger.Debug().Err(err).Msg("bump item access (voie A)")
				}
			}()
		}
	}
	if req.SessionID != "" && sessionCtx != nil {
		updateSessionPostSynthesis(sessionCtx, query, answer, req.RecentSourceIDs)
	}
}
