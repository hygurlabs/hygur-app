package controlplane

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnService adds passkey registration + login to the control plane (P1).
//
// The relying party IS the control plane, with RP ID set to the registrable
// parent domain (e.g. "hygur.ai") so a passkey registered on console.hygur.ai
// authenticates on cloud.hygur.ai (the web shell). Login resolves the instance
// slug → account, verifies the assertion, then mints the SAME device-token bundle
// as enroll/refresh (reusing the Service). Registration is authorized by a valid
// device access token (the just-enrolled device adds a passkey to its account).
//
// Ceremony SessionData is parked server-side under a one-time id (5-min TTL); the
// id travels in the begin response body and back as the `s` query param on finish,
// so no custom request header / cookie is needed cross-origin.
type WebAuthnService struct {
	wa    *webauthn.WebAuthn
	store *Store
	svc   *Service
	now   func() time.Time
}

// NewWebAuthnService builds the RP from the issuer domain + permitted origins.
func NewWebAuthnService(store *Store, svc *Service, rpID, rpDisplayName string, origins []string) (*WebAuthnService, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpDisplayName,
		RPOrigins:     origins,
	})
	if err != nil {
		return nil, err
	}
	return &WebAuthnService{wa: wa, store: store, svc: svc, now: time.Now}, nil
}

func (a *WebAuthnService) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// Register adds the passkey endpoints to r.
func (a *WebAuthnService) Register(r chi.Router) {
	r.Post("/passkey/register/begin", a.registerBegin)
	r.Post("/passkey/register/finish", a.registerFinish)
	r.Post("/passkey/login/begin", a.loginBegin)
	r.Post("/passkey/login/finish", a.loginFinish)
	// Desktop passkey handoff: the system browser logs in, then hands a long-lived
	// token to the native app via a hygur:// deep-link carrying only a one-time code.
	r.Post("/desktop/handoff", a.handleDesktopHandoff)
	r.Post("/desktop/claim", a.handleDesktopClaim)
}

type desktopReq struct {
	State string `json:"state"`
}

// handleDesktopHandoff: the web shell, right after a passkey login, calls this with
// the desktop-generated `state` to stash a fresh LONG-LIVED desktop token for the
// native app. Authed by the shell's access token. The bundle is parked one-time
// under `state` (5-min TTL); the desktop claims it via the deep-link. The raw token
// NEVER travels in the deep-link URL — only `state` does (anti scheme-hijack).
func (a *WebAuthnService) handleDesktopHandoff(w http.ResponseWriter, r *http.Request) {
	acc, err := a.accountFromToken(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in desktopReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.State) == "" {
		writeErr(w, http.StatusBadRequest, "state required")
		return
	}
	now := a.clock()
	if !acc.IsActive(now) {
		writeErr(w, http.StatusForbidden, "subscription inactive")
		return
	}
	dev, refresh, err := a.store.CreateDeviceForAccount(now, acc.AccountNumber, "desktop")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "device")
		return
	}
	access, err := a.svc.mintDesktopToken(now, acc, dev)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token")
		return
	}
	bundle, _ := json.Marshal(tokenResp{
		AccessToken:  access,
		RefreshToken: refresh,
		Endpoint:     fmt.Sprintf("https://%s.%s", acc.TenantID, a.svc.domain),
		TenantID:     acc.TenantID,
		ExpiresIn:    90 * 24 * 3600,
	})
	if err := a.store.PutWebauthnSession(in.State, acc.AccountNumber, "desktop", bundle, now.Add(5*time.Minute)); err != nil {
		writeErr(w, http.StatusInternalServerError, "stash")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDesktopClaim: the native app posts the `state` from the deep-link and gets
// the stashed token bundle (one-time, expiry-checked). No auth — `state` is the
// short-lived, desktop-generated secret.
func (a *WebAuthnService) handleDesktopClaim(w http.ResponseWriter, r *http.Request) {
	var in desktopReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.State) == "" {
		writeErr(w, http.StatusBadRequest, "state required")
		return
	}
	_, purpose, data, err := a.store.TakeWebauthnSession(a.clock(), in.State)
	if err != nil || purpose != "desktop" {
		writeErr(w, http.StatusUnauthorized, "invalid or expired")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// webauthnUser adapts an Account + its stored credentials to webauthn.User. The
// user handle is the opaque (non-PII) account number; the display name is the
// instance slug.
type webauthnUser struct {
	acc   Account
	creds []webauthn.Credential
}

func (u webauthnUser) WebAuthnID() []byte                         { return []byte(u.acc.AccountNumber) }
func (u webauthnUser) WebAuthnName() string                       { return u.acc.TenantID }
func (u webauthnUser) WebAuthnDisplayName() string                { return u.acc.TenantID }
func (u webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (a *WebAuthnService) loadUser(acc Account) (webauthnUser, error) {
	blobs, err := a.store.WebauthnCredentialBlobs(acc.AccountNumber)
	if err != nil {
		return webauthnUser{}, err
	}
	creds := make([]webauthn.Credential, 0, len(blobs))
	for _, b := range blobs {
		var c webauthn.Credential
		if err := json.Unmarshal(b, &c); err != nil {
			return webauthnUser{}, err
		}
		creds = append(creds, c)
	}
	return webauthnUser{acc: acc, creds: creds}, nil
}

// --- registration (authorized by a device access token) ---

func (a *WebAuthnService) registerBegin(w http.ResponseWriter, r *http.Request) {
	acc, err := a.accountFromToken(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := a.loadUser(acc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load user")
		return
	}
	options, session, err := a.wa.BeginRegistration(user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "begin registration")
		return
	}
	a.startSession(w, acc.AccountNumber, "register", session, options)
}

func (a *WebAuthnService) registerFinish(w http.ResponseWriter, r *http.Request) {
	acc, session, err := a.resumeSession(r, "register")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid session")
		return
	}
	user, err := a.loadUser(acc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load user")
		return
	}
	cred, err := a.wa.FinishRegistration(user, session, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "registration failed")
		return
	}
	blob, _ := json.Marshal(cred)
	if err := a.store.AddWebauthnCredential(a.clock(), acc.AccountNumber, base64url(cred.ID), blob, "passkey"); err != nil {
		writeErr(w, http.StatusInternalServerError, "store credential")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- login (the passkey IS the auth) ---

type loginBeginReq struct {
	Instance string `json:"instance"` // the instance slug (tenant id)
}

func (a *WebAuthnService) loginBegin(w http.ResponseWriter, r *http.Request) {
	var in loginBeginReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Instance) == "" {
		writeErr(w, http.StatusBadRequest, "instance required")
		return
	}
	// Uniform error: never reveal whether an instance exists or has a passkey.
	acc, err := a.store.getAccountByTenantID(strings.ToLower(strings.TrimSpace(in.Instance)))
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unknown instance or no passkey")
		return
	}
	user, err := a.loadUser(acc)
	if err != nil || len(user.creds) == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown instance or no passkey")
		return
	}
	options, session, err := a.wa.BeginLogin(user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "begin login")
		return
	}
	a.startSession(w, acc.AccountNumber, "login", session, options)
}

func (a *WebAuthnService) loginFinish(w http.ResponseWriter, r *http.Request) {
	acc, session, err := a.resumeSession(r, "login")
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid session")
		return
	}
	user, err := a.loadUser(acc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "load user")
		return
	}
	cred, err := a.wa.FinishLogin(user, session, r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "authentication failed")
		return
	}
	// Persist sign-count / clone-warning updates from this assertion.
	if blob, mErr := json.Marshal(cred); mErr == nil {
		_ = a.store.UpdateWebauthnCredential(base64url(cred.ID), blob)
	}

	now := a.clock()
	if !acc.IsActive(now) {
		writeErr(w, http.StatusForbidden, "subscription inactive")
		return
	}
	dev, refresh, err := a.store.CreateDeviceForAccount(now, acc.AccountNumber, "passkey")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "issue device")
		return
	}
	// Mints access + refresh + resolves the endpoint — same bundle as enroll.
	a.svc.issueAndRespond(w, now, dev, refresh)
}

// --- auth + session helpers ---

// accountFromToken authorizes a request by a device access token (Authorization:
// Bearer <jwt> or X-Hygur-Token) issued by this control plane.
func (a *WebAuthnService) accountFromToken(r *http.Request) (Account, error) {
	raw := bearer(r)
	if raw == "" {
		return Account{}, errors.New("missing access token")
	}
	claims, err := a.svc.verifyAccessToken(raw)
	if err != nil {
		return Account{}, err
	}
	return a.store.GetAccount(claims.Sub)
}

func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return strings.TrimSpace(r.Header.Get("X-Hygur-Token"))
}

// startSession parks the ceremony SessionData under a one-time id and returns the
// browser options ({"publicKey":{...}}) plus that id in the body.
func (a *WebAuthnService) startSession(w http.ResponseWriter, account, purpose string, session *webauthn.SessionData, options any) {
	data, err := json.Marshal(session)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session")
		return
	}
	id := newSessionID()
	if err := a.store.PutWebauthnSession(id, account, purpose, data, a.clock().Add(5*time.Minute)); err != nil {
		writeErr(w, http.StatusInternalServerError, "session")
		return
	}
	raw, _ := json.Marshal(options) // {"publicKey":{...}}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		writeErr(w, http.StatusInternalServerError, "options")
		return
	}
	m["session_id"] = id
	writeJSON(w, http.StatusOK, m)
}

// resumeSession consumes the one-time session id (?s=<id>) and returns the account
// + parsed SessionData, enforcing the expected purpose.
func (a *WebAuthnService) resumeSession(r *http.Request, purpose string) (Account, webauthn.SessionData, error) {
	id := r.URL.Query().Get("s")
	if id == "" {
		return Account{}, webauthn.SessionData{}, errors.New("missing session")
	}
	account, p, data, err := a.store.TakeWebauthnSession(a.clock(), id)
	if err != nil || p != purpose {
		return Account{}, webauthn.SessionData{}, errors.New("invalid session")
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		return Account{}, webauthn.SessionData{}, err
	}
	acc, err := a.store.GetAccount(account)
	if err != nil {
		return Account{}, webauthn.SessionData{}, err
	}
	return acc, sd, nil
}

func newSessionID() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// CORSMiddleware permits the configured cross-origin web shells (e.g.
// https://cloud.hygur.ai) to call the control plane (enroll + passkey ceremonies).
// Loopback origins are always allowed; everything else must be listed. Unknown
// origins get no CORS headers (the browser then blocks them).
func CORSMiddleware(origins []string) func(http.Handler) http.Handler {
	allow := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			allow[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := r.Header.Get("Origin")
			lo := strings.ToLower(o)
			if o != "" && (allow[lo] ||
				strings.HasPrefix(lo, "http://localhost") || strings.HasPrefix(lo, "http://127.0.0.1")) {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Hygur-Token")
				w.Header().Set("Access-Control-Allow-Credentials", "true") // refresh cookie on /token/*
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
