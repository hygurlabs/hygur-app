package controlplane

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// AdminConsole serves the operator-only admin surface (cost dashboard API + its
// embedded SPA), gated by a passkey-minted access token whose subject is the
// configured operator account (admin@hygur.ai). The operator bootstraps a passkey
// via a one-time enroll code, then signs in with the passkey like any device —
// reusing the existing WebAuthn + JWT machinery, no separate password system.
type AdminConsole struct {
	store           *Store
	svc             *Service
	operatorAccount string
}

// NewAdminConsole builds the admin surface. operatorAccount is the account number
// of the operator (admin@hygur.ai); an empty value disables /admin/*.
func NewAdminConsole(store *Store, svc *Service, operatorAccount string) *AdminConsole {
	return &AdminConsole{store: store, svc: svc, operatorAccount: strings.TrimSpace(operatorAccount)}
}

// Register mounts the admin API behind the operator gate.
func (a *AdminConsole) Register(r chi.Router) {
	r.Group(func(g chi.Router) {
		g.Use(a.operatorOnly)
		g.Get("/admin/cost", a.handleCost)
		g.Get("/admin/errors", a.handleErrors)
	})
}

// handleErrors returns the most recent client error reports (first-party, no
// third-party tracking) for the operator's "recent errors" panel.
func (a *AdminConsole) handleErrors(w http.ResponseWriter, r *http.Request) {
	errs, err := a.store.ListRecentErrors(200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "errors query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": errs})
}

// operatorOnly authorizes requests by a control-plane access token whose subject
// is the configured operator account: 503 if no operator is configured, 401 if
// the token is missing/invalid, 403 if authenticated as a non-operator account.
func (a *AdminConsole) operatorOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.operatorAccount == "" {
			writeErr(w, http.StatusServiceUnavailable, "admin disabled: no operator configured")
			return
		}
		raw := bearer(r)
		if raw == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		claims, err := a.svc.verifyAccessToken(raw)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if claims.Sub != a.operatorAccount {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleCost returns the fleet cost summary + per-tenant breakdown + the latest
// snapshot time (for the dashboard's freshness indicator).
func (a *AdminConsole) handleCost(w http.ResponseWriter, r *http.Request) {
	now := a.svc.clock()
	summary, err := a.store.GlobalCostSummary(now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cost summary failed")
		return
	}
	tenants, err := a.store.PerTenantCost(now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "per-tenant cost failed")
		return
	}
	captured, _ := a.store.LatestCapture()
	writeJSON(w, http.StatusOK, map[string]any{
		"summary":      summary,
		"tenants":      tenants,
		"captured_at":  captured,
		"generated_at": now.UTC().Format(time.RFC3339),
	})
}
