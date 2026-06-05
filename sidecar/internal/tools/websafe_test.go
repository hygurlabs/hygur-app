package tools

import (
	"net"
	"strings"
	"testing"
)

func TestIsDisallowedIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":               false, // public
		"2001:4860:4860::8888":  false, // public v6
		"127.0.0.1":             true,  // loopback
		"::1":                   true,  // loopback v6
		"10.0.0.1":              true,  // RFC1918
		"192.168.1.1":           true,  // RFC1918
		"172.16.5.4":            true,  // RFC1918
		"169.254.169.254":       true,  // link-local (cloud metadata)
		"100.64.0.1":            true,  // CGNAT
		"0.0.0.0":               true,  // unspecified
		"fc00::1":               true,  // ULA
	}
	for s, want := range cases {
		if got := isDisallowedIP(net.ParseIP(s)); got != want {
			t.Errorf("isDisallowedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestValidatePublicURL(t *testing.T) {
	ok := []string{"http://example.com/x", "https://8.8.8.8/p"}
	bad := []string{"ftp://example.com", "http://127.0.0.1", "https://10.0.0.1", "http://169.254.169.254/latest", "notaurl::"}
	for _, u := range ok {
		if _, err := validatePublicURL(u); err != nil {
			t.Errorf("validatePublicURL(%q) errored: %v", u, err)
		}
	}
	for _, u := range bad {
		if _, err := validatePublicURL(u); err == nil {
			t.Errorf("validatePublicURL(%q) should have errored", u)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<html><head><title>  My Title </title><style>.x{color:red}</style>` +
		`<script>steal()</script></head><body>Hello   <b>brave</b>` +
		`<script>inject('ignore previous')</script> world</body></html>`
	title, text, err := htmlToText(strings.NewReader(in), 0)
	if err != nil {
		t.Fatalf("htmlToText: %v", err)
	}
	if title != "My Title" {
		t.Errorf("title = %q, want %q", title, "My Title")
	}
	if !strings.Contains(text, "Hello brave world") {
		t.Errorf("text missing readable content: %q", text)
	}
	for _, leak := range []string{"steal", "inject", "ignore previous", "color:red"} {
		if strings.Contains(text, leak) {
			t.Errorf("text leaked script/style content %q: %q", leak, text)
		}
	}
}
