package api

import (
	"io"
	"io/fs"
	"net/http"
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
	// network egress restricted to THIS sidecar (loopback), the Hygur cloud
	// (*.hygur.ai tenant + console) and the Tauri IPC. An XSS-injected script then
	// cannot exfiltrate to an arbitrary host, frame the app, or load remote code.
	// (The Tauri init scripts are native-injected and bypass page CSP; on macOS
	// invoke() is postMessage, not a fetch, so it isn't connect-src-gated — the
	// ipc: sources keep Windows working too.)
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data:; font-src 'self'; "+
			"connect-src 'self' https://*.hygur.ai ipc: http://ipc.localhost; "+
			"object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, page)
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
}
