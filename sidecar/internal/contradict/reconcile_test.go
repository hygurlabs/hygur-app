package contradict

import "testing"

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		raw  string
		kind string
	}{
		{`{"kind":"conflict","reason":"two amounts, no update"}`, "conflict"},
		{`{"kind":"supersedes","reason":"later mail moves the date"}`, "supersedes"},
		{`{"kind":"none","reason":"different currencies"}`, "none"},
		{"Sure:\n```json\n{\"kind\":\"conflict\",\"reason\":\"x\"}\n```", "conflict"},
		{`prefix {"kind":"SUPERSEDES","reason":"y"} suffix`, "supersedes"}, // case-insensitive
		{`{"kind":"maybe","reason":"z"}`, "none"},                          // unknown → none
		{"not json", "none"},
		{"", "none"},
		{`{broken`, "none"},
	}
	for _, c := range cases {
		if got := parseVerdict(c.raw); got.Kind != c.kind {
			t.Errorf("parseVerdict(%q).Kind = %q, want %q", c.raw, got.Kind, c.kind)
		}
	}
}

func TestParseVerdict_KeepsReason(t *testing.T) {
	v := parseVerdict(`{"kind":"conflict","reason":"  amounts differ  "}`)
	if v.Kind != "conflict" || v.Reason != "amounts differ" {
		t.Fatalf("got %+v", v)
	}
}

func TestShortID(t *testing.T) {
	for in, want := range map[string]string{
		"mail:acct:123":                        "123",
		"148dd37f-bdf6-431c-9f19-12548cef9dac": "148dd37f-bdf6-431c-9f19-12548cef9dac",
		"imap:x@host":                          "x@host",
	} {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}
