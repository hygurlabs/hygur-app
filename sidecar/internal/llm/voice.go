package llm

// ProseVoiceGuidance is a short, model- and language-agnostic voice block for
// user-facing prose surfaces (mail drafts, chronicle, positions, follow-up
// report, chat, daily brief). It distils the *intent* of an editorial pass —
// plainness, concreteness, no filler — into a handful of generic principles,
// deliberately without enumerated cases, domain examples or profile
// assumptions, and short enough to stay marginal on small local models. It does
// not touch grounding or attribution: those live in baseFormatGuidance and the
// per-surface prompts. The deterministic counterpart (typography, preamble
// stripping) is internal/prose.Tidy, which works regardless of model obedience.
const ProseVoiceGuidance = `Write plainly and get to the point — no preamble, no scene-setting, no signposting of what you're about to say. Prefer the concrete, specific word; drop intensifiers, filler verbs, and dead transitions that carry no information or real logical link. Make each point once: don't restate it, justify it, or address the reader as a student. Vary sentence length, and keep a useful repetition rather than cycling synonyms. Use an explicit "not X but Y" contrast only when the opposition is genuinely intended. Speak to the reader directly, like a capable colleague who knows their world — warm and human, never robotic, never effusive.`

// WithVoice appends the shared prose-voice block to a system prompt. Used only on
// user-facing prose surfaces; never on JSON/extraction prompts (which must stay
// strict and unpadded). Keeping the wiring in one helper makes "is this surface
// voiced?" directly testable from the assembled prompt value.
func WithVoice(systemPrompt string) string {
	return systemPrompt + "\n\n" + ProseVoiceGuidance
}
