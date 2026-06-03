package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/hygur/sidecar/internal/version"
)

// APIVersionHeader carries the API contract version on both requests (the
// client's) and responses (the server's). See internal/version.APIVersion.
const APIVersionHeader = "X-Hygur-API"

// handleVersion reports the server's API and build version. Public (no auth) so
// a client can detect version skew up front, independently of credentials.
// GET /version
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"api":            version.APIVersion,
		"min_client_api": version.MinClientAPIVersion,
		"app":            version.Version,
	})
}

// apiVersionMiddleware advertises the server's API version on every response
// and, when a client declares its own via X-Hygur-API, rejects clients older
// than the minimum supported with 426 Upgrade Required. A missing header means
// "no negotiation" and is allowed — the loopback web UI, curl, and other
// non-versioned clients keep working unchanged. A malformed header is ignored
// rather than failing the request.
func (s *Server) apiVersionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(APIVersionHeader, strconv.Itoa(version.APIVersion))

		if raw := r.Header.Get(APIVersionHeader); raw != "" {
			if clientV, err := strconv.Atoi(raw); err == nil && clientV < version.MinClientAPIVersion {
				writeJSON(w, http.StatusUpgradeRequired, map[string]any{
					"code": "CLIENT_TOO_OLD",
					"message": fmt.Sprintf(
						"This client speaks API v%d but the server requires at least v%d — please update the app.",
						clientV, version.MinClientAPIVersion,
					),
					"server_api":     version.APIVersion,
					"min_client_api": version.MinClientAPIVersion,
				})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
