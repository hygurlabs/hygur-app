package llm

import "testing"

// feed runs a whole sequence of deltas through one filter and returns the joined output.
func feed(deltas ...string) string {
	var f HarmonyFilter
	out := ""
	for _, d := range deltas {
		out += f.Feed(d)
	}
	return out + f.Flush()
}

func TestHarmonyFilter(t *testing.T) {
	cases := []struct {
		name   string
		deltas []string
		want   string
	}{
		{"channel header prefix", []string{"<|channel|>final<|message|>Le numéro est 42."}, "Le numéro est 42."},
		{"header split across deltas", []string{"<|channel|>", "final", "<|message|>", "Hello"}, "Hello"},
		{"token split mid-run", []string{"<|chan", "nel|>final<|mess", "age|>Hi"}, "Hi"},
		{"plain text untouched", []string{"a < b and c > d"}, "a < b and c > d"},
		{"literal trailing lt", []string{"ends with <"}, "ends with <"},
		{"unknown token kept", []string{"see <|foo|> here"}, "see <|foo|> here"},
		{"standalone end token dropped", []string{"done<|end|> now"}, "done now"},
		{"start token dropped", []string{"<|start|>assistant<|channel|>final<|message|>Hi"}, "assistantHi"},
	}
	for _, c := range cases {
		if got := feed(c.deltas...); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// Streaming a "final" channel one rune at a time must still strip the framing cleanly.
	src := "<|channel|>final<|message|>Bonjour Elric"
	var f HarmonyFilter
	out := ""
	for _, r := range src {
		out += f.Feed(string(r))
	}
	if out += f.Flush(); out != "Bonjour Elric" {
		t.Errorf("rune-by-rune: got %q, want %q", out, "Bonjour Elric")
	}
}
