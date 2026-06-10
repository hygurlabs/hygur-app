package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWebUIInjectsToken(t *testing.T) {
	s := &Server{token: "tok-abc-123"}
	rec := httptest.NewRecorder()
	s.handleWebUI(rec, httptest.NewRequest("GET", "/", nil))

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if strings.Contains(body, "__HYGUR_TOKEN__") {
		t.Error("token placeholder was not substituted")
	}
	// The token is injected into the <meta name="hygur-token"> tag, which the
	// web client reads at startup (see webui/src/lib/api.ts readToken()).
	if !strings.Contains(body, `content="tok-abc-123"`) {
		t.Error("live token not injected into the page")
	}
	if !strings.Contains(body, "<title>Hygur</title>") {
		t.Error("served page is not the Hygur web client")
	}
}

// connectSrc extracts the space-separated sources of the connect-src directive
// from a full CSP string, or returns nil if there is no connect-src.
func connectSrc(csp string) []string {
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if rest, ok := strings.CutPrefix(d, "connect-src "); ok {
			return strings.Fields(rest)
		}
	}
	return nil
}

func TestBuildCSPLocalDefault(t *testing.T) {
	// Local/default mode: no configured sources beyond the console default would
	// be passed by main, but the handler itself must always emit the fail-safe
	// loopback + IPC sources and a non-empty connect-src.
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleWebUI(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	src := connectSrc(csp)
	if len(src) == 0 {
		t.Fatal("connect-src is empty")
	}
	want := map[string]bool{"'self'": false, "ipc:": false, "http://ipc.localhost": false}
	for _, s := range src {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("connect-src missing fail-safe source %q (got %v)", k, src)
		}
	}
	// Web-shell alignment.
	for _, frag := range []string{"media-src 'self' data: blob:", "img-src 'self' data: blob:", "form-action 'self'"} {
		if !strings.Contains(csp, frag) {
			t.Errorf("CSP missing %q\nfull: %s", frag, csp)
		}
	}
}

func TestBuildCSPCloudExactOrigin(t *testing.T) {
	// Cloud mode: the exact configured tenant + console origins appear in
	// connect-src, normalised to scheme://host (path stripped), and the wildcard
	// must NOT be present once a real origin resolved.
	s := &Server{}
	s.SetCSPConnectSources([]string{"https://acme.hygur.ai/some/path", "https://console.hygur.ai"})
	rec := httptest.NewRecorder()
	s.handleWebUI(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	src := connectSrc(csp)
	has := func(v string) bool {
		for _, s := range src {
			if s == v {
				return true
			}
		}
		return false
	}
	if !has("https://acme.hygur.ai") {
		t.Errorf("connect-src missing exact tenant origin (got %v)", src)
	}
	if !has("https://console.hygur.ai") {
		t.Errorf("connect-src missing console origin (got %v)", src)
	}
	if has("https://*.hygur.ai") {
		t.Errorf("wildcard must not appear once a real origin resolved (got %v)", src)
	}
}

func TestBuildCSPMalformedFallsBackToWildcard(t *testing.T) {
	// A malformed/empty upstream resolves to nothing → fall back to the
	// *.hygur.ai wildcard, never an empty connect-src.
	s := &Server{}
	s.SetCSPConnectSources([]string{"://nonsense", "   ", "not a url at all"})
	rec := httptest.NewRecorder()
	s.handleWebUI(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	src := connectSrc(csp)
	if len(src) == 0 {
		t.Fatal("connect-src is empty after malformed sources")
	}
	found := false
	for _, s := range src {
		if s == "https://*.hygur.ai" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected *.hygur.ai wildcard fallback (got %v)", src)
	}
}
