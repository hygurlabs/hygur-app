package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/netguard"
	"golang.org/x/net/html"
)

// This file is the shared safety layer for the web tools (web_search, fetch_url).
// Two jobs: (1) SSRF defence — never let the model make the server reach internal
// addresses (cloud metadata, k8s API, Sparky, other tenants, localhost); (2) turn
// untrusted HTML into bounded plain text. Prompt-injection defence proper (the
// "tainted context" that disables side-effecting tools once web content is in
// play) lives in the chat loop, not here.
//
// The SSRF primitives now live in internal/netguard, shared with the outbound
// connectors so there is ONE implementation. The web tools always pin
// allowPrivate=false: fetch_url/web_search must NEVER reach private targets.

// isDisallowedIP reports whether an IP must never be dialed by a web tool.
// Thin wrapper over netguard so behaviour stays identical.
func isDisallowedIP(ip net.IP) bool { return netguard.IsDisallowedIP(ip) }

// safeHTTPClient builds an HTTP client that only ever connects to public hosts,
// bounds the time, and re-validates every redirect hop (allowPrivate=false).
func safeHTTPClient(timeout time.Duration) *http.Client {
	return netguard.Client(timeout, false)
}

// validatePublicURL parses raw, enforces http/https, and fails fast if the host
// is a non-public IP literal. The web tools never allow private targets.
func validatePublicURL(raw string) (*url.URL, error) {
	return netguard.ValidateURL(raw, false, "http", "https")
}

// fetchText GETs u (public-only, size-bounded) and returns the page title + its
// readable text, capped at maxChars.
func fetchText(ctx context.Context, client *http.Client, u string, maxChars int) (title, text string, err error) {
	if _, err = validatePublicURL(u); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "HygurBot/0.1 (+https://hygur.ai)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Cap the body at 3 MB so a hostile/huge page can't exhaust memory.
	return htmlToText(io.LimitReader(resp.Body, 3<<20), maxChars)
}

// htmlToText extracts the title + visible text from HTML, dropping script/style/
// head/noscript (where injections + noise hide), collapsing whitespace, and
// capping at maxChars. Plain-text input passes through (still capped).
func htmlToText(r io.Reader, maxChars int) (title, text string, err error) {
	if maxChars <= 0 {
		maxChars = 6000
	}
	z := html.NewTokenizer(r)
	var b strings.Builder
	skipDepth := 0 // inside script/style/head/noscript
	inTitle := false
	var titleB strings.Builder
	// NB: do not skip <head> wholesale — its <title> is wanted (captured below) and
	// its <script>/<style> are skipped by their own tags anyway.
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true}
	for {
		switch z.Next() {
		case html.ErrorToken:
			t := strings.TrimSpace(collapseWS(b.String()))
			if len(t) > maxChars {
				t = t[:maxChars]
			}
			if err := z.Err(); err != nil && err != io.EOF {
				return strings.TrimSpace(titleB.String()), t, nil // partial content is fine
			}
			return strings.TrimSpace(titleB.String()), t, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			n := string(name)
			if skip[n] {
				skipDepth++
			}
			if n == "title" {
				inTitle = true
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			if skip[n] && skipDepth > 0 {
				skipDepth--
			}
			if n == "title" {
				inTitle = false
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			txt := string(z.Text())
			if inTitle {
				titleB.WriteString(txt)
				continue
			}
			b.WriteString(txt)
			b.WriteByte(' ')
			if b.Len() > maxChars*4 { // enough raw to fill maxChars after collapse
				// keep tokenizing only to find </title>? title already seen first; stop.
			}
		}
	}
}

// collapseWS squashes runs of whitespace into single spaces.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
