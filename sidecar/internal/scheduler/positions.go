package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/prose"
	"github.com/hygur/sidecar/internal/store"
)

// Angle A-2b — the self-model's outward voice. A short, grounded summary of the
// user's standing positions, built STRICTLY from their confirmed decisions: it
// reflects what was decided, cited to the decisions, and infers nothing beyond them
// (no values, traits, or psychology — that is the trust line). The LLM only phrases
// facts that are already there. Content-addressed cache (store): regenerates only
// when the decisions change, so the read paths stay cheap.

const (
	positionsMaxDecisions = 24  // bound the prompt; most-recent decisions win
	positionsRationaleCap = 240 // per-decision rationale snippet
	positionsMaxTokens    = 320 // <= ~120-word summary, bounded
)

// positionsSystemPrompt — strict, generic, no enumerated cases. The guardrail is in
// the rules: summarize only what was decided, attribute nothing beyond it.
const positionsPromptBase = `You are Hygur, reflecting the user's standing positions back to them, strictly from their own confirmed decisions.

You are given the user's CONFIRMED DECISIONS (numbered: statement, optional reasoning, date). Write a short, plain-prose summary (<= 120 words) of where they currently stand — the positions they have actually decided.

Rules:
- Address the user directly as "you" (e.g. "You have decided…"). Never write "the user" or "the author".
- Use ONLY the decisions given. Summarize only what was explicitly decided.
- Infer NO values, beliefs, traits, motives, or feelings beyond the decisions themselves. Describe positions, not the person.
- Group related decisions into the same thread where they clearly belong; keep distinct matters distinct.
- If there is only one decision, state it plainly. If there is nothing of substance, write nothing at all.
- A sober, readable register in your own plain voice. No heading, no preamble — only the prose.`

// positionsSystemPrompt = base + the shared prose-voice block.
var positionsSystemPrompt = llm.WithVoice(positionsPromptBase)

// PositionsSynopsis returns the grounded "standing positions" summary (A-2b). The
// cache is keyed by a fingerprint of the standing decisions, so a hit means the
// summary is current. allowGenerate=false (the chat path) returns whatever is cached
// — possibly stale, never "", and never an LLM call — so the chat stays cheap; the
// digest (allowGenerate=true) regenerates on a fingerprint miss. Fail-open: on any
// generation error it returns the previous cached text (or "").
func (d *DailyBrief) PositionsSynopsis(ctx context.Context, allowGenerate bool) string {
	if d == nil || d.store == nil {
		return ""
	}
	decs, err := d.store.ListDecisions(ctx, "", store.DecisionStanding)
	if err != nil || len(decs) == 0 {
		return ""
	}
	fp := positionsFingerprint(decs)
	cached, cachedFP, found, _ := d.store.GetPositionsSynopsis(ctx)
	if found && cachedFP == fp {
		return cached // current
	}
	if !allowGenerate {
		return cached // cheap path: cached-or-empty, no LLM
	}
	text, gerr := d.generatePositions(ctx, decs)
	if gerr != nil || strings.TrimSpace(text) == "" {
		if gerr != nil {
			d.logger.Warn().Err(gerr).Msg("positions synopsis generate failed (fail-open)")
		}
		return cached // keep the prior summary rather than blanking the surface
	}
	if perr := d.store.PutPositionsSynopsis(ctx, text, fp); perr != nil {
		d.logger.Debug().Err(perr).Msg("cache positions synopsis")
	}
	return text
}

// positionsFingerprint hashes the standing-decision set's identity (id + statement +
// decided-on + updated-at), order-independent, so any add/remove/edit/redate of a
// decision changes the fingerprint and triggers a regeneration.
func positionsFingerprint(decs []*store.Decision) string {
	parts := make([]string, 0, len(decs))
	for _, d := range decs {
		parts = append(parts, d.ID+"|"+d.Statement+"|"+d.DecidedOn+"|"+d.UpdatedAt)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// generatePositions runs the bounded, grounded LLM call over the decisions. Streamed
// on the chat model (prose quality) with thinking off; low-frequency + cached, so the
// cost is bounded. ListDecisions already orders most-recent first.
func (d *DailyBrief) generatePositions(ctx context.Context, decs []*store.Decision) (string, error) {
	if len(decs) > positionsMaxDecisions {
		decs = decs[:positionsMaxDecisions]
	}
	var b strings.Builder
	for i, dec := range decs {
		fmt.Fprintf(&b, "%d. %s", i+1, strings.TrimSpace(dec.Statement))
		if len(dec.DecidedOn) >= 10 {
			fmt.Fprintf(&b, " (%s)", dec.DecidedOn[:10])
		}
		if r := strings.TrimSpace(dec.Rationale); r != "" {
			r = strings.ReplaceAll(r, "\n", " ")
			if len(r) > positionsRationaleCap {
				r = r[:positionsRationaleCap] + "…"
			}
			fmt.Fprintf(&b, " — %s", r)
		}
		b.WriteString("\n")
	}

	var sb strings.Builder
	err := d.llm.StreamChat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: positionsSystemPrompt},
			{Role: "user", Content: "CONFIRMED DECISIONS:\n" + b.String()},
		},
		Temperature:        0.4,
		MaxTokens:          positionsMaxTokens,
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}, func(delta string, _ bool, _ *llm.Usage) error {
		sb.WriteString(delta)
		return nil
	})
	if err != nil {
		return "", err
	}
	// Couche B: deterministic cleanup before the content-addressed cache.
	return prose.Tidy(strings.TrimSpace(sb.String()), ""), nil
}
