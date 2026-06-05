package controlplane

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	r.Post("/enroll", s.handleEnroll)
	r.Post("/token/refresh", s.handleRefresh)
	return r
}

type enrollReq struct {
	Code string `json:"code"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
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
	var in refreshReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	now := s.clock()
	dev, newRefresh, err := s.store.Refresh(now, in.RefreshToken)
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
	writeJSON(w, http.StatusOK, tokenResp{
		AccessToken:  access,
		RefreshToken: refresh,
		Endpoint:     fmt.Sprintf("https://%s.%s", acc.TenantID, s.domain),
		TenantID:     acc.TenantID,
		ExpiresIn:    int(s.accessTTL.Seconds()),
	})
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
