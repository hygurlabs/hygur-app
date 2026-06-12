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

	"golang.org/x/net/html"
)

// This file is the shared safety layer for the web tools (web_search, fetch_url).
// Two jobs: (1) SSRF defence — never let the model make the server reach internal
// addresses (cloud metadata, k8s API, Sparky, other tenants, localhost); (2) turn
// untrusted HTML into bounded plain text. Prompt-injection defence proper (the
// "tainted context" that disables side-effecting tools once web content is in
// play) lives in the chat loop, not here.

// isDisallowedIP reports whether an IP must never be dialed by a web tool:
// loopback, private (RFC1918 + ULA), link-local (incl. 169.254.169.254 metadata),
// CGNAT, unspecified, and multicast.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT 100.64.0.0/10 (sometimes routes to cloud metadata/infra).
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

// safeDialContext resolves the target at DIAL time and refuses if any resolved IP
// is disallowed — closing the DNS-rebinding/TOCTOU window (a name that resolves
// public at validation but internal at connect). Re-runs on every redirect hop.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if isDisallowedIP(ip) {
			return nil, fmt.Errorf("refusing to connect to non-public address %s (%s)", ip, host)
		}
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// safeHTTPClient builds an HTTP client that only ever connects to public hosts
// (via safeDialContext), bounds the time, and re-validates every redirect hop.
func safeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: safeDialContext, MaxIdleConns: 4},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil // each hop still dials through safeDialContext
		},
	}
}

// validatePublicURL parses raw, enforces http/https, and fails fast if the host
// resolves to a non-public address. (Defence in depth — safeDialContext is the
// authoritative guard, but this rejects obviously-bad URLs before any request.)
func validatePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http/https URLs are allowed (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	if ip := net.ParseIP(host); ip != nil && isDisallowedIP(ip) {
		return nil, fmt.Errorf("refusing non-public address %s", host)
	}
	return u, nil
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
	skipDepth := 0          // inside script/style/head/noscript
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
