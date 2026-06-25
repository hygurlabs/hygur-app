package api

import (
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/hygur/sidecar/internal/api/webui"
)

// handleWebUI serves the SPA shell (dist/index.html) with the API token
// injected so the browser can call the authenticated API on the same (loopback)
// origin — the 127.0.0.1 bind is the trust boundary, matching the model the
// native app already relies on. Client-side routing is hash-based, so this same
// shell answers every navigation; no other server-side fallback is needed. This
// route is intentionally unauthenticated: it must bootstrap the token.
func (s *Server) handleWebUI(w http.ResponseWriter, r *http.Request) {
	raw, err := webui.DistFS.ReadFile("dist/index.html")
	if err != nil {
		http.Error(w, "web UI not built — run `make webui`", http.StatusInternalServerError)
		return
	}
	// In a managed cloud tenant the loopback token is meaningless (auth is per-
	// device JWT) and must NOT be shipped to the browser — inject empty so the
	// SPA shows its Connect screen (the user pastes their device token) instead
	// of silently failing with a dead token.
	tok := s.token
	if s.managed {
		tok = ""
	}
	page := strings.ReplaceAll(string(raw), "__HYGUR_TOKEN__", tok)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// Lock the embedded SPA down: code + styles + images same-origin only, and
	// network egress restricted to THIS sidecar (loopback), the resolved cloud
	// tenant + console origins and the Tauri IPC (see buildCSP). An XSS-injected
	// script then cannot exfiltrate to an arbitrary host, frame the app, or load
	// remote code. (The Tauri init scripts are native-injected and bypass page CSP;
	// on macOS invoke() is postMessage, not a fetch, so it isn't connect-src-gated —
	// the ipc: sources keep Windows working too.)
	w.Header().Set("Content-Security-Policy", s.buildCSP())
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, page)
}

// buildCSP assembles the Content-Security-Policy header for the served SPA from
// the resolved connect-src sources (SetCSPConnectSources). The connect-src list
// ALWAYS includes the unconditional fail-safe sources — 'self' (loopback),
// ipc: and http://ipc.localhost (Tauri IPC) — so a webview with no devtools is
// never bricked by a bad config. On top of those it adds each configured origin
// (the cloud tenant upstream, the console origin, and any HYGUR_ALLOWED_ORIGINS),
// normalised to a scheme://host origin and parsed defensively: a source that
// won't parse is logged and skipped rather than panicking. If NOTHING resolves
// beyond the fail-safe sources, the legacy https://*.hygur.ai wildcard is added
// so cloud installs keep working; connect-src is therefore never empty.
//
// The rest of the policy mirrors the cloud web-shell CSP
// (hygur-cloud/nginx/cloud-shell.vhost.conf): media-src 'self' data: blob:,
// blob: in img-src, and form-action 'self'.
func (s *Server) buildCSP() string {
	// Unconditional fail-safe sources — order is stable for testability.
	connect := []string{"'self'", "ipc:", "http://ipc.localhost"}

	seen := map[string]bool{"'self'": true, "ipc:": true, "http://ipc.localhost": true}
	added := false
	for _, raw := range s.cspConnectSrc {
		o := cspOrigin(raw)
		if o == "" {
			s.logger.Warn().Str("source", raw).Msg("CSP connect-src: skipping unparseable origin")
			continue
		}
		if seen[o] {
			continue
		}
		seen[o] = true
		connect = append(connect, o)
		added = true
	}
	// Nothing resolved beyond the fail-safe sources → keep cloud installs working
	// with today's wildcard rather than emitting a connect-src that blocks the
	// tenant/console (and never an empty one).
	if !added {
		connect = append(connect, "https://*.hygur.ai")
	}

	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self'; media-src 'self' data: blob:; " +
		"connect-src " + strings.Join(connect, " ") + "; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
}

// cspOrigin normalises a configured source to a CSP-usable scheme://host[:port]
// origin (no path/query). It returns "" when the input can't be parsed into an
// absolute origin so buildCSP can skip it defensively. A bare origin without a
// path (e.g. https://console.hygur.ai) round-trips unchanged.
func cspOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// webUIAssets serves the content-hashed build assets under /assets/. The
// filenames embed a hash of their contents, so they're safe to cache forever.
func webUIAssets() http.Handler {
	sub, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		// The embedded FS is static; this can only fail if dist was absent at
		// build time, in which case there are no assets to serve anyway.
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}

// webUIPublic serves the static files Vite copies from webui/public to the dist
// root at build time: the favicon, the iOS/Android home-screen icons, and the
// PWA manifest. These are requested from the site root (e.g. /favicon.ico,
// /apple-touch-icon.png) by browsers and "Add to Home Screen", so they're routed
// individually in setupRoutes rather than under /assets/. Unlike the hashed
// bundle these filenames are stable, so they get a day-long cache, not immutable.
func webUIPublic() http.Handler {
	sub, err := fs.Sub(webui.DistFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		// Go's mime table doesn't know .webmanifest; set it so browsers treat
		// the file as a real web app manifest rather than sniffed text.
		if strings.HasSuffix(r.URL.Path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// webUIPublicFiles are the public/ root assets served by webUIPublic. Listed
// explicitly so a request to one of these paths returns the file rather than
// falling through to a 404 (there is no catch-all; SPA routing is hash-based).
var webUIPublicFiles = []string{
	"/favicon.ico",
	"/favicon-16.png",
	"/favicon-32.png",
	"/apple-touch-icon.png",
	"/icon-192.png",
	"/icon-512.png",
	"/manifest.webmanifest",
	"/sw.js", // Web Push service worker
}
