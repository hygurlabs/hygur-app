package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/figure"
	"github.com/hygur/sidecar/internal/identity"
)

// LookupFigureTool exposes the deterministic (subject, figure-label, direction, period) → monetary
// value resolution to the chat agent (FIGURES_TRUTH_PLAN F1). The VALUE + its CONTEXT (unit, period,
// direction, source) come from the figure engram graph via a deterministic traversal, never the
// model's memory; the model only voices what the engine determined, or an honest decline. This is
// how "the amount of my last VAT to pay" gets a grounded, sourced answer instead of a RAG guess.
type LookupFigureTool struct {
	NoSideEffect
	store        figure.Store
	owner        *identity.Matcher
	ownerSubject string // the anchor a FIRST-PERSON subject resolves to (same as lookup_identifier)
}

// NewLookupFigureTool builds the tool over the store, owner matcher and representative owner name.
func NewLookupFigureTool(s figure.Store, owner *identity.Matcher, ownerSubject string) *LookupFigureTool {
	return &LookupFigureTool{store: s, owner: owner, ownerSubject: strings.TrimSpace(ownerSubject)}
}

func (t *LookupFigureTool) Name() string { return "lookup_figure" }

func (t *LookupFigureTool) Description() string {
	return "Get an exact labeled figure with its context — a MONETARY amount (a VAT amount to pay/refunded for a period) OR a medical DOSAGE (a medication's dose with its unit and frequency). Use for 'the amount of my [last] VAT to pay', 'my VAT for Q1 2026', or 'my Amoxicillin dose'. Returns the deterministic value with its context (period+direction, or unit+frequency for a dose) and surfaces any cross-document change; or an honest decline. Never a guessed number."
}

func (t *LookupFigureTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entity": map[string]any{
				"type":        "string",
				"description": "Whose figure it is (a name). Use 'me'/'moi' for the user themselves.",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "The figure's label as the user names it — e.g. 'VAT', 'TVA', or a medication + 'dose' (e.g. 'Amoxicillin dose'). Pass the raw phrase; the engine normalizes it and matches the medication.",
			},
			"direction": map[string]any{
				"type":        "string",
				"description": "WHICH figure the label denotes, if the user said so: 'to pay' / 'à payer', 'refunded' / 'remboursée' / 'à récupérer', 'advance' / 'acompte'. Omit if unspecified.",
			},
			"period": map[string]any{
				"type":        "string",
				"description": "The period the user named, if any — e.g. 'Q1 2026', 'T1 2026', '2026'. Omit for 'the last / latest' (the engine picks the latest period).",
			},
		},
		"required": []string{"entity", "label"},
	}
}

type figureArgs struct {
	Entity    string `json:"entity"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	Period    string `json:"period"`
}

// FigureResponse is what the model receives — and, verbatim, what the chat handler turns into the
// authoritative `determined_answer` render. The engine PRODUCES the value + context; the model only
// voices it at the reported confidence. Value/Label are pre-composed for display so the existing
// card renders a figure (amount + unit, with period + direction in the label) with no UI change.
type FigureResponse struct {
	Label     string `json:"label,omitempty"`   // composed heading, e.g. "VAT to pay · Q1 2026" / "Amoxicillin dose"
	Subject   string `json:"subject,omitempty"` // whose figure ("you" for the owner)
	Value     string `json:"value,omitempty"`   // composed display value, e.g. "7 421,85 €" / "500 mg, 3×/day"
	Unit      string `json:"unit,omitempty"`
	Period    string `json:"period,omitempty"`
	Direction string `json:"direction,omitempty"`
	Frequency string `json:"frequency,omitempty"`
	// Note surfaces a cross-document supersession contradiction ("Previously 5 mg — updated.").
	Note     string        `json:"note,omitempty"`
	Tier     fact.Tier     `json:"confidence"`
	Reason   string        `json:"reason,omitempty"`
	Guidance string        `json:"guidance"`
	Sources  []fact.Source `json:"sources,omitempty"`
}

func (t *LookupFigureTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a figureArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	a.Entity = strings.TrimSpace(a.Entity)
	a.Label = strings.TrimSpace(a.Label)
	if a.Entity == "" || a.Label == "" {
		return nil, fmt.Errorf("entity and label are required")
	}

	// First-person subject → the configured owner anchor (same as lookup_identifier).
	resolveQuery := a.Entity
	subjectLabel := a.Entity
	if isFirstPersonSubject(a.Entity) {
		subjectLabel = "you"
		if t.ownerSubject == "" {
			return json.Marshal(FigureResponse{Label: a.Label, Subject: subjectLabel, Tier: fact.TierNone,
				Guidance: "Do NOT state a value — the owner is not configured, so a first-person figure cannot be resolved. Say you don't have a verified value."})
		}
		resolveQuery = t.ownerSubject
	}

	res, err := figure.Resolve(ctx, t.store, contradict.NormKey(resolveQuery), a.Label, a.Direction, a.Period, t.owner)
	if err != nil {
		return nil, err
	}

	out := FigureResponse{
		Subject: subjectLabel,
		Tier:    res.Tier,
		Reason:  res.Reason,
		Sources: res.Sources,
	}
	if res.Tier == fact.TierHigh || res.Tier == fact.TierMed {
		out.Label = composeFigureHeading(res.Label, res.Medication, res.Direction, res.Period)
		out.Value = composeFigureDisplay(res.Raw, res.Value, res.Unit, res.Frequency)
		out.Unit, out.Period, out.Direction, out.Frequency = res.Unit, res.Period, res.Direction, res.Frequency
		out.Note = composeSupersessionNote(res.Prior)
		out.Guidance = "State this value plainly as the answer, with its unit and any frequency/period/direction, and cite the source document(s). Do NOT alter the number."
		if out.Note != "" {
			out.Guidance += " A newer document changed this value — state the current value AND mention the prior one exactly as given in the note."
		}
		return json.Marshal(out)
	}

	out.Label = composeFigureLabel(res.Label, "", "")
	switch res.Reason {
	case figure.ReasonAmbiguousMedic:
		out.Guidance = "Several medications carry a dose and the user named none. Do NOT state a value — ask WHICH medication they mean."
	case figure.ReasonAmbiguousDir:
		out.Guidance = "Several figures of this label compete (e.g. to pay vs refunded). Do NOT state a value — ask the user WHICH one they mean."
	case figure.ReasonAmbiguousPeriod:
		out.Guidance = "Several periods carry this figure and no clear 'latest' can be ordered. Do NOT state a value — ask the user which period."
	case figure.ReasonAmbiguousValue:
		out.Guidance = "The selected period/direction holds conflicting values. Do NOT state a value — say the record is inconsistent and offer the source(s)."
	case figure.ReasonUnknownLabel:
		out.Guidance = "This figure label is not one the engine tracks deterministically. Do NOT state a value — say you don't have a verified one."
	default: // ReasonNoFigure / none
		out.Guidance = "Do NOT state a value — you have no verified figure of this label for that subject/period. Say so honestly."
	}
	return json.Marshal(out)
}

// directionDisplay renders a canonical direction as an English phrase for the card heading.
func directionDisplay(dir string) string {
	switch dir {
	case figure.DirPayable:
		return "to pay"
	case figure.DirRefund:
		return "refunded"
	case figure.DirAdvance:
		return "advance"
	case figure.DirDue:
		return "due"
	}
	return ""
}

// labelDisplay renders a canonical figure label for the card heading.
func labelDisplay(label string) string {
	switch label {
	case "vat":
		return "VAT"
	}
	return strings.ToUpper(label)
}

// periodDisplay renders a normalized period key for humans ("2026-Q1" → "Q1 2026").
func periodDisplay(period string) string {
	if i := strings.Index(period, "-Q"); i >= 0 {
		return "Q" + period[i+2:] + " " + period[:i]
	}
	return period
}

// composeFigureLabel builds the card heading: "VAT to pay · Q1 2026".
func composeFigureLabel(label, dir, period string) string {
	parts := []string{labelDisplay(label)}
	if d := directionDisplay(dir); d != "" {
		parts = append(parts, d)
	}
	head := strings.TrimSpace(strings.Join(parts, " "))
	if p := periodDisplay(period); p != "" {
		head += " · " + p
	}
	return head
}

// composeFigureValue builds the display value: the amount as written + the unit symbol ("7 421,85 €",
// "500 mg", "5 %"). The unit is rendered from the value itself — no per-type branch beyond currency's
// trailing symbol and ratio's no-space convention.
func composeFigureValue(rawAmount, canonical, unit string) string {
	amt := rawAmount
	if amt == "" {
		amt = canonical
	}
	switch unit {
	case "EUR":
		return amt + " €"
	case "":
		return amt
	case "%":
		return amt + "%"
	}
	return amt + " " + unit
}

// composeFigureDisplay renders the value with its unit and appends any dosage frequency ("500 mg,
// 3×/day"). The frequency is a context edge shown inline so the cut-LLM card carries the full dose.
func composeFigureDisplay(rawAmount, canonical, unit, frequency string) string {
	v := composeFigureValue(rawAmount, canonical, unit)
	if frequency != "" {
		v += ", " + frequency
	}
	return v
}

// composeFigureHeading builds the card heading. A dosage reads "Amoxicillin dose" (the MEDICATION is
// the qualifier); a monetary figure reads "VAT to pay · Q1 2026" (direction + period). Same composer,
// the present context edges deciding the phrasing — no separate code path per figure type.
func composeFigureHeading(label, medication, dir, period string) string {
	if label == "dose" && medication != "" {
		return capitalizeMed(medication) + " dose"
	}
	return composeFigureLabel(label, dir, period)
}

// capitalizeMed title-cases a folded medication name for display ("amoxicillin" → "Amoxicillin").
func capitalizeMed(m string) string {
	if m == "" {
		return m
	}
	r := []rune(m)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// composeSupersessionNote renders the cross-document contradiction for a superseded figure: the
// value(s) a newer document replaced ("Previously 5 mg — updated."). Empty when nothing superseded.
func composeSupersessionNote(prior []figure.PriorValue) string {
	if len(prior) == 0 {
		return ""
	}
	parts := make([]string, 0, len(prior))
	for _, p := range prior {
		parts = append(parts, composeFigureValue(p.Raw, p.Value, p.Unit))
	}
	return "Previously " + strings.Join(parts, ", ") + " — updated."
}
