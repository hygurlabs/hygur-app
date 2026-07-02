package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestIsDisallowedIP pins the SSRF classifier: every internal/reserved range is
// refused, only genuinely public addresses pass.
func TestIsDisallowedIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":              false, // public v4
		"2001:4860:4860::8888": false, // public v6
		"127.0.0.1":            true,  // loopback
		"::1":                  true,  // loopback v6
		"10.0.0.1":             true,  // RFC1918
		"192.168.1.1":          true,  // RFC1918
		"172.16.0.1":           true,  // RFC1918
		"169.254.169.254":      true,  // link-local (cloud metadata)
		"100.64.0.1":           true,  // CGNAT
		"fc00::1":              true,  // ULA
	}
	for s, want := range cases {
		if got := IsDisallowedIP(net.ParseIP(s)); got != want {
			t.Errorf("IsDisallowedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

// TestDialerToggle proves the *net.Dialer path (used by the IMAP connector):
// with allowPrivate=false a loopback target is refused before connecting; with
// allowPrivate=true the same target connects.
func TestDialerToggle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1:PORT

	if _, err := Dialer(2*time.Second, false).Dial("tcp", addr); err == nil {
		t.Error("Dialer(allowPrivate=false) should refuse a loopback target")
	} else if !strings.Contains(err.Error(), "non-public") {
		t.Errorf("dial error = %q, want it to mention non-public", err)
	}

	conn, err := Dialer(2*time.Second, true).Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dialer(allowPrivate=true) should reach a loopback target: %v", err)
	}
	_ = conn.Close()
}

// TestClientToggle proves the HTTP path (used by fetch_url + the CalDAV
// connector) blocks/allows a loopback target symmetrically.
func TestClientToggle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := Client(2*time.Second, false).Do(req); err == nil {
		t.Error("Client(allowPrivate=false) should refuse a loopback target")
	} else if !strings.Contains(err.Error(), "non-public") {
		t.Errorf("client error = %q, want it to mention non-public", err)
	}

	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := Client(2*time.Second, true).Do(req2)
	if err != nil {
		t.Fatalf("Client(allowPrivate=true) should reach a loopback target: %v", err)
	}
	_ = resp.Body.Close()
}

// TestValidateURL covers the scheme allowlist and the IP-literal fast-fail.
func TestValidateURL(t *testing.T) {
	if _, err := ValidateURL("https://example.com/x", false); err != nil {
		t.Errorf("public https URL should validate: %v", err)
	}
	bad := []string{
		"ftp://example.com",       // scheme
		"file:///etc/passwd",      // scheme
		"http://169.254.169.254/", // metadata IP literal
		"http://10.0.0.5/",        // private IP literal
	}
	for _, u := range bad {
		if _, err := ValidateURL(u, false); err == nil {
			t.Errorf("ValidateURL(%q) should have errored", u)
		}
	}
	// allowPrivate lifts only the IP check, never the scheme allowlist.
	if _, err := ValidateURL("http://127.0.0.1/", true); err != nil {
		t.Errorf("ValidateURL loopback with allowPrivate should pass: %v", err)
	}
	if _, err := ValidateURL("file:///etc/passwd", true); err == nil {
		t.Error("ValidateURL(file://) must be rejected even with allowPrivate")
	}
}
