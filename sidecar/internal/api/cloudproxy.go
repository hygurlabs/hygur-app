// Package api — cloud-backed thin-client mode.
package api

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// SetCloudProxy turns this sidecar into a cloud-backed thin client. When upstream
// is non-empty, every request EXCEPT the locally served SPA shell, its hashed
// asset bundle, and /health//version is reverse-proxied to the cloud tenant with
// the device JWT injected as X-Hygur-Token.
//
// This is the "one binary, routes active locally or proxied per config" model:
// the desktop webview stays same-origin on the loopback sidecar (so the Tauri
// commands and the quick-capture palette keep working), while the data/AI routes
// (chat, knowledge, search, …) are answered by the tenant where the KB,
// embeddings and LLM live. An empty upstream restores local mode (no-op).
//
// Trust boundary: the loopback bind stays the boundary, exactly as in local mode.
// A local process that can reach 127.0.0.1 is forwarded with the device token —
// the same caller could already read the local KB in local mode, so cloud mode
// adds no new exposure.
func (s *Server) SetCloudProxy(upstream, deviceToken string) error {
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		s.cloudProxy = nil
		return nil
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return fmt.Errorf("cloud upstream URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("cloud upstream URL must be absolute (scheme + host), got %q", upstream)
	}

	rp := httputil.NewSingleHostReverseProxy(u)
	// Flush immediately so SSE responses (/chat, /events) stream to the client
	// instead of being buffered until the upstream closes the connection.
	rp.FlushInterval = -1

	base := rp.Director
	rp.Director = func(req *http.Request) {
		base(req) // sets scheme/host + joins the path, preserves the query string
		// Host header drives the tenant vhost (nginx) AND the cloud's own
		// DNS-rebinding Host-guard — it must be the tenant FQDN, not 127.0.0.1.
		req.Host = u.Host
		// Device auth at the cloud (the loopback SPA carries no token in managed
		// mode; the sidecar injects the per-device JWT here).
		req.Header.Set("X-Hygur-Token", deviceToken)
	}
	rp.ErrorHandler = func(w http.ResponseWriter, req *http.Request, perr error) {
		s.logger.Error().Err(perr).Str("path", req.URL.Path).Msg("cloud proxy upstream error")
		writeError(w, http.StatusBadGateway, "cloud upstream unreachable")
	}

	s.cloudProxy = rp
	return nil
}

// keepLocalPath reports whether a path must be served by THIS sidecar even in
// cloud mode: the SPA shell + its content-hashed bundle (so the webview stays
// same-origin and the Tauri commands work) and the liveness endpoints. Everything
// else is proxied. A deny-list keeps maintenance low — new cloud API routes are
// proxied automatically without touching this list.
func keepLocalPath(p string) bool {
	switch p {
	case "/", "/app", "/health", "/version":
		return true
	}
	// Edge thin-client routes run on THIS device (local Proton Bridge access) and
	// must never be forwarded to the tenant.
	if strings.HasPrefix(p, "/edge/") {
		return true
	}
	return strings.HasPrefix(p, "/assets/")
}

// cloudProxyMiddleware forwards data/AI requests to the cloud tenant when cloud
// mode is active (SetCloudProxy). No-op in local mode.
func (s *Server) cloudProxyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cloudProxy == nil || keepLocalPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		s.cloudProxy.ServeHTTP(w, r)
	})
}
