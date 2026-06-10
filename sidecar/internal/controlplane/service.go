package controlplane

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/auth"
)

// Service is the control-plane HTTP API: device enrollment + token refresh
// (C8.1b). It mints per-device access JWTs whose Acc claim is the tenant id,
// signed with ONE control-plane issuer key. Tenants verify with the matching
// public key; isolation comes from the tenant-pin (Acc must equal the pod's
// HYGUR_TENANT_ID, see internal/auth.JWTAuth). This replaces the manual
// `issue-token` and the per-tenant keypairs of the C5 path.
type Service struct {
	store     *Store
	signer    ed25519.PrivateKey
	accessTTL time.Duration
	domain    string // tenant endpoint = https://<tenant_id>.<domain>
	now       func() time.Time
}

// NewService parses the issuer private key (PEM) and wires the service.
func NewService(store *Store, issuerPrivPEM, domain string, accessTTL time.Duration) (*Service, error) {
	priv, err := auth.ParseEd25519PrivateKeyPEM(issuerPrivPEM)
	if err != nil {
		return nil, fmt.Errorf("controlplane: parse issuer key: %w", err)
	}
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if domain == "" {
		domain = "hygur.ai"
	}
	return &Service{store: store, signer: priv, accessTTL: accessTTL, domain: domain, now: time.Now}, nil
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Routes returns the device-facing control-plane endpoints. (Admin endpoints —
// account/enroll-code creation — are added separately, operator-authenticated.)
func (s *Service) Routes() http.Handler {
	r := chi.NewRouter()
	s.Register(r)
	return r
}

// Register adds the device-facing routes to r, so the console can compose the
// Service and the Billing webhook on one router.
func (s *Service) Register(r chi.Router) {
	r.Post("/enroll", s.handleEnroll)
	r.Post("/token/refresh", s.handleRefresh)
	r.Post("/token/logout", s.handleLogout)
	r.Get("/billing/status", s.handleBillingStatus)
}

// handleBillingStatus returns the caller's subscription status + the Stripe
// customer-portal link, for the client Settings "Billing" panel. Authed by the
// device access token (its Sub is the account number). Read-only — no Stripe API,
// no secret key on the console: the portal is the no-code login link
// (HYGUR_STRIPE_PORTAL_URL) where the customer self-serves.
func (s *Service) handleBillingStatus(w http.ResponseWriter, r *http.Request) {
	claims, err := s.verifyAccessToken(bearer(r))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	acc, err := s.store.GetAccount(claims.Sub)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no billing account")
		return
	}
	validUntil := ""
	if acc.ValidUntil != nil {
		validUntil = acc.ValidUntil.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      acc.Status, // trialing | active | past_due | canceled
		"active":      acc.IsActive(s.clock()),
		"valid_until": validUntil,
		"portal_url":  os.Getenv("HYGUR_STRIPE_PORTAL_URL"),
	})
}

type enrollReq struct {
	Code string `json:"code"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	// RefreshToken is no longer returned for web sessions (it's set as an HttpOnly
	// cookie). Kept (omitempty) only for the desktop path, which has no cookie.
	RefreshToken string `json:"refresh_token,omitempty"`
	Endpoint     string `json:"endpoint"`
	TenantID     string `json:"tenant_id"`
	ExpiresIn    int    `json:"expires_in"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// handleEnroll: code → device + tokens. The browser/app posts the one-time code
// from the portal callback and receives its access + refresh tokens + the tenant
// endpoint to connect to.
func (s *Service) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var in enrollReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Code == "" {
		writeErr(w, http.StatusBadRequest, "code required")
		return
	}
	now := s.clock()
	dev, refresh, err := s.store.RedeemEnrollCode(now, in.Code)
	if errors.Is(err, ErrCodeInvalid) {
		writeErr(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "enrollment failed")
		return
	}
	s.issueAndRespond(w, now, dev, refresh)
}

// handleRefresh: refresh token → fresh access token (rotates both).
func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	// Web sends the refresh token in the HttpOnly cookie. The JSON-body fallback now
	// serves only the desktop app (loopback origin → no console cookie; it gets its
	// refresh token via /desktop/claim). The web's legacy localStorage→body bootstrap
	// has been removed.
	rt := ""
	if c, cerr := r.Cookie(refreshCookieName); cerr == nil {
		rt = c.Value
	}
	if rt == "" {
		var in refreshReq
		if err := json.NewDecoder(r.Body).Decode(&in); err == nil {
			rt = in.RefreshToken
		}
	}
	if rt == "" {
		writeErr(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	now := s.clock()
	dev, newRefresh, err := s.store.Refresh(now, rt)
	if errors.Is(err, ErrRefreshInvalid) {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "refresh failed")
		return
	}
	s.issueAndRespond(w, now, dev, newRefresh)
}

// issueAndRespond looks up the account (for tenant id + active check), mints the
// access JWT, and returns the token bundle. Shared by enroll + refresh.
func (s *Service) issueAndRespond(w http.ResponseWriter, now time.Time, dev Device, refresh string) {
	acc, err := s.store.GetAccount(dev.AccountNumber)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "account lookup failed")
		return
	}
	if !acc.IsActive(now) {
		writeErr(w, http.StatusForbidden, "subscription inactive")
		return
	}
	access, err := s.mintAccess(now, acc, dev)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	// Refresh token → HttpOnly cookie (out of JS/XSS reach) for the web shell. It is
	// NOT echoed in the body here (tokenResp.RefreshToken stays empty); the desktop
	// path receives its refresh token via /desktop/claim instead.
	//
	// EXCEPT the operator/admin account: its cookie sits on the shared hygur.ai
	// domain, so a tenant web shell's refresh-on-load would pick it up and get
	// hijacked onto operator.hygur.ai. The admin SPA re-authenticates per session
	// via passkey instead of relying on the shared cookie.
	if acc.TenantID != operatorTenantID {
		s.setRefreshCookie(w, refresh)
	}
	writeJSON(w, http.StatusOK, tokenResp{
		AccessToken: access,
		Endpoint:    fmt.Sprintf("https://%s.%s", acc.TenantID, s.domain),
		TenantID:    acc.TenantID,
		ExpiresIn:   int(s.accessTTL.Seconds()),
	})
}

const refreshCookieName = "hygur_rt"

// operatorTenantID is the sentinel tenant_id of the admin/operator account
// (admin@hygur.ai). It has no tenant pod — it exists only to authenticate the
// operator cost dashboard — so its session is deliberately cookie-less (see
// issueAndRespond): a shared hygur.ai cookie would otherwise be adopted by a real
// tenant's web shell on refresh-on-load and redirect it to operator.hygur.ai.
const operatorTenantID = "operator"

// setRefreshCookie stores the refresh token in an HttpOnly, Secure, SameSite=Lax
// cookie scoped to the registrable domain + the /token path — sent only to the
// refresh/logout endpoints and never readable by JavaScript, so an XSS on the web
// shell can't steal the long-lived credential. Lax (not Strict) so the cookie
// survives a top-level navigation arriving from an external link.
func (s *Service) setRefreshCookie(w http.ResponseWriter, refresh string) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: refresh, Path: "/token", Domain: s.domain,
		MaxAge: 90 * 24 * 3600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// handleLogout clears the refresh cookie (full web sign-out).
func (s *Service) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/token", Domain: s.domain,
		MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Service) mintAccess(now time.Time, acc Account, dev Device) (string, error) {
	return auth.SignDeviceToken(s.signer, auth.DeviceClaims{
		Sub: acc.AccountNumber,
		Acc: acc.TenantID, // tenant-pin target (must equal the pod's HYGUR_TENANT_ID)
		Dev: dev.DeviceID,
		Jti: dev.JTI,
		Iat: now.Unix(),
		Exp: now.Add(s.accessTTL).Unix(),
	})
}

// mintDesktopToken issues a LONG-LIVED (90-day) access token for the native
// desktop client. Unlike the web shell (which refreshes a 15-min token), the
// desktop's loopback proxy injects this token on every request and has no refresh
// loop — so it must outlive a session. Tenant-pinned + jti-revocable like any device.
func (s *Service) mintDesktopToken(now time.Time, acc Account, dev Device) (string, error) {
	return auth.SignDeviceToken(s.signer, auth.DeviceClaims{
		Sub: acc.AccountNumber,
		Acc: acc.TenantID,
		Dev: dev.DeviceID,
		Jti: dev.JTI,
		Iat: now.Unix(),
		Exp: now.Add(90 * 24 * time.Hour).Unix(),
	})
}

// verifyAccessToken validates a device access token this control plane issued,
// using the issuer key's public half. Used to authorize passkey registration (the
// just-enrolled device adds a passkey to its account). Returns the claims.
func (s *Service) verifyAccessToken(raw string) (auth.DeviceClaims, error) {
	pub, ok := s.signer.Public().(ed25519.PublicKey)
	if !ok {
		return auth.DeviceClaims{}, fmt.Errorf("controlplane: issuer key not ed25519")
	}
	return auth.VerifyDeviceToken(pub, raw, s.clock())
}
