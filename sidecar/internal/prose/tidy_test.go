package prose

import "testing"

// These golden tests pin the deterministic transform (the pure `tidy`), so they
// hold regardless of which model produced the text — the whole point of Couche B.

func TestStripPreamble(t *testing.T) {
	cases := []struct{ name, in, lang, want string }{
		{
			name: "english lead-in stripped",
			in:   "Here is the reply:\n\nDear Sir,\nThanks.",
			lang: "en",
			want: "Dear Sir,\nThanks.",
		},
		{
			name: "sure lead-in stripped",
			in:   "Sure, here's the draft:\n\nHello team.",
			lang: "en",
			want: "Hello team.",
		},
		{
			name: "single-line colon is content, not preamble",
			in:   "Here is the situation: we owe 500.",
			lang: "en",
			want: "Here is the situation: we owe 500.",
		},
		{
			name: "long here-is sentence is not a lead-in",
			in:   "Here is the situation we discussed at length yesterday with the whole team:\nwe owe 500.",
			lang: "en",
			want: "Here is the situation we discussed at length yesterday with the whole team:\nwe owe 500.",
		},
		{
			name: "preamble that is the only content is kept",
			in:   "Voici :\n\n",
			lang: "en", // English path → isolate the strip decision from typography
			want: "Voici :\n\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tidy(c.in, c.lang); got != c.want {
				t.Errorf("tidy(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestFrenchTypography(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "elision apostrophes",
			in:   "C'est l'heure de l'appel.",
			want: "C’est l’heure de l’appel.",
		},
		{
			name: "straight quotes become guillemets",
			in:   `Elle a répondu "oui" ce matin.`,
			want: "Elle a répondu «" + thinNbsp + "oui" + thinNbsp + "» ce matin.",
		},
		{
			name: "thin nbsp before strong punctuation",
			in:   "Je confirme le rendez-vous ; est-ce possible ?",
			want: "Je confirme le rendez-vous" + thinNbsp + "; est-ce possible" + thinNbsp + "?",
		},
		{
			name: "nbsp before sentence colon",
			in:   "Note importante : relire le contrat.",
			want: "Note importante" + nbsp + ": relire le contrat.",
		},
		{
			name: "digits keep a tight colon",
			in:   "Le train part à 14:30 demain.",
			want: "Le train part à 14:30 demain.",
		},
		{
			name: "double spaces collapsed",
			in:   "Le  contrat  est  signé.",
			want: "Le contrat est signé.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tidy(c.in, "fr"); got != c.want {
				t.Errorf("tidy(%q,fr)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestProtectsCodeAndURLs(t *testing.T) {
	// French text, but the inline code, fenced block and URL must survive verbatim
	// (no apostrophe/colon/quote rewriting inside them).
	in := "Regarde `a:b` et https://x.io/a?b=1 ; c'est là.\n```\nx:y \"z\"\n```"
	want := "Regarde `a:b` et https://x.io/a?b=1" + thinNbsp + "; c’est là.\n```\nx:y \"z\"\n```"
	if got := tidy(in, "fr"); got != want {
		t.Errorf("tidy protect\n got %q\nwant %q", got, want)
	}
}

func TestEnglishPathPreservesLayout(t *testing.T) {
	// English output (e.g. the daily brief): only the preamble is stripped; no
	// French typography, and the markdown hard break (two trailing spaces) survives.
	in := "Sure, here is the brief:\n\n## Today\n\nDo X.  \nThen Y."
	want := "## Today\n\nDo X.  \nThen Y."
	if got := tidy(in, "en"); got != want {
		t.Errorf("english layout\n got %q\nwant %q", got, want)
	}
}

func TestDetectFrench(t *testing.T) {
	if !detectFrench("le chat est noir et il dort") {
		t.Error("expected French")
	}
	if detectFrench("the cat is black and it sleeps") {
		t.Error("expected English")
	}
}

func TestKillSwitchAndBlank(t *testing.T) {
	defer SetEnabled(true)
	SetEnabled(false)
	in := "Voici :\n\nBonjour, c'est l'heure."
	if got := Tidy(in, "fr"); got != in {
		t.Errorf("kill-switch off must be identity: got %q", got)
	}
	SetEnabled(true)
	if got := Tidy("", "fr"); got != "" {
		t.Errorf("empty got %q", got)
	}
	if got := Tidy("   ", "fr"); got != "   " {
		t.Errorf("blank got %q", got)
	}
}
