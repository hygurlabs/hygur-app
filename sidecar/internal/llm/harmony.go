package llm

import "strings"

// harmonyControlTokens are OpenAI "Harmony" response-format control tokens that some
// reasoning models (served via LM Studio / vLLM) can leak into the content stream when
// the backend doesn't fully parse the channel framing — e.g. "<|channel|>final<|message|>"
// prefixing an answer. They must never reach the user's rendered text.
var harmonyControlTokens = map[string]bool{
	"<|start|>": true, "<|end|>": true, "<|message|>": true, "<|channel|>": true,
	"<|return|>": true, "<|call|>": true, "<|constrain|>": true, "<|refusal|>": true,
}

// harmonyMaxToken bounds a "<|...|>" run: past this many chars without a closing "|>",
// the leading "<|" is treated as literal text, not the start of a control token.
const harmonyMaxToken = 24

// HarmonyFilter strips Harmony control framing from a streamed content sequence. Stateful:
// call Feed on each delta and Flush once at the end. It handles tokens split across delta
// boundaries, drops control tokens, and after a <|channel|> marker drops the channel name
// up to the following <|message|> — so "<|channel|>final<|message|>" vanishes entirely.
// Unknown "<|xxx|>" runs are kept verbatim (never eat legitimate content). The zero value
// is ready to use.
type HarmonyFilter struct {
	carry            string // held-back partial token straddling a delta boundary
	dropUntilMessage bool   // inside a channel header: dropping the name until <|message|>
}

// Feed returns the sanitized text for one streamed delta (may be empty if the delta was
// pure framing).
func (f *HarmonyFilter) Feed(s string) string {
	work := f.carry + s
	f.carry = ""
	var out strings.Builder
	for len(work) > 0 {
		if f.dropUntilMessage {
			i := strings.Index(work, "<|message|>")
			if i < 0 {
				// Drop the channel name; keep only a possible partial "<|message|>" suffix.
				f.carry = tailPrefixOf(work, "<|message|>")
				return out.String()
			}
			work = work[i+len("<|message|>"):]
			f.dropUntilMessage = false
			continue
		}
		i := strings.Index(work, "<|")
		if i < 0 {
			if strings.HasSuffix(work, "<") { // a lone "<" may begin "<|" in the next delta
				out.WriteString(work[:len(work)-1])
				f.carry = "<"
			} else {
				out.WriteString(work)
			}
			return out.String()
		}
		out.WriteString(work[:i])
		rest := work[i:]
		c := strings.Index(rest, "|>")
		if c < 0 {
			if len(rest) < harmonyMaxToken { // partial token at the boundary — hold it
				f.carry = rest
				return out.String()
			}
			out.WriteString("<|") // too long to be a token — emit literally, scan on
			work = rest[2:]
			continue
		}
		tok := rest[:c+2]
		work = rest[c+2:]
		if tok == "<|channel|>" {
			f.dropUntilMessage = true
		} else if !harmonyControlTokens[tok] {
			out.WriteString(tok) // unknown "<|...|>" — keep it, don't swallow real content
		}
		// Known control tokens (including <|channel|>) are dropped.
	}
	return out.String()
}

// Flush returns any safe leftover text at end of stream. A partial control token or an
// in-progress channel header is framing, not content, and is dropped.
func (f *HarmonyFilter) Flush() string {
	c := f.carry
	f.carry = ""
	if f.dropUntilMessage || strings.HasPrefix(c, "<|") {
		return "" // mid channel-header, or a dangling partial token
	}
	return c // a plain leftover (e.g. a literal trailing "<")
}

// tailPrefixOf returns the longest suffix of s that is a prefix of tok — used to hold a
// possibly-partial token across a delta boundary. "" if no suffix qualifies.
func tailPrefixOf(s, tok string) string {
	n := len(tok) - 1
	if len(s) < n {
		n = len(s)
	}
	for ; n > 0; n-- {
		if strings.HasPrefix(tok, s[len(s)-n:]) {
			return s[len(s)-n:]
		}
	}
	return ""
}
