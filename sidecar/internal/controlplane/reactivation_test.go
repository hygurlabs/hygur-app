package controlplane

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WP-SEC1-reactivation tests: passkey-gated recovery of a dormant tenant, with the
// Stripe customer-id match acting only as a discovery signal. Fictional data only.

const (
	testCustomer = "cus_test123"
	testEmail    = "alice.bernard@acme.example"
	testOrigin   = "https://cloud.hygur.ai"
	testRPID     = "hygur.ai"
)

// --- a tiny software WebAuthn authenticator (ES256) for the security proofs ------

type softAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	credID := make([]byte, 16)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("cred id: %v", err)
	}
	return &softAuthenticator{key: key, credID: credID}
}

// coseKey encodes the public key as a COSE_Key EC2/ES256/P-256 map (fixed layout:
// {1:2, 3:-7, -1:1, -2:X, -3:Y}), matching what go-webauthn's webauthncose parses.
func (a *softAuthenticator) coseKey() []byte {
	pad := func(b []byte) []byte {
		out := make([]byte, 32)
		copy(out[32-len(b):], b)
		return out
	}
	out := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}
	out = append(out, pad(a.key.PublicKey.X.Bytes())...)
	out = append(out, 0x22, 0x58, 0x20)
	out = append(out, pad(a.key.PublicKey.Y.Bytes())...)
	return out
}

func (a *softAuthenticator) credentialBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := json.Marshal(webauthn.Credential{
		ID:        a.credID,
		PublicKey: a.coseKey(),
		Flags:     webauthn.CredentialFlags{UserPresent: true},
	})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	return blob
}

// assertion builds the JSON assertion body for reactivate/finish. Signing with
// a.key yields a VALID assertion; signing with any other key yields an INVALID one.
func (a *softAuthenticator) assertion(t *testing.T, challengeB64url string, signer *ecdsa.PrivateKey) []byte {
	t.Helper()
	clientData, _ := json.Marshal(map[string]any{
		"type":        "webauthn.get",
		"challenge":   challengeB64url,
		"origin":      testOrigin,
		"crossOrigin": false,
	})
	rpHash := sha256.Sum256([]byte(testRPID))
	authData := append([]byte{}, rpHash[:]...)
	authData = append(authData, 0x01)       // flags: UP only (matches stored cred flags)
	authData = append(authData, 0, 0, 0, 0) // sign counter 0
	cdHash := sha256.Sum256(clientData)
	signed := append(append([]byte{}, authData...), cdHash[:]...)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, signer, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"id":    base64.RawURLEncoding.EncodeToString(a.credID),
		"rawId": base64.RawURLEncoding.EncodeToString(a.credID),
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
		},
	})
	return body
}

// --- store fixtures -------------------------------------------------------------

func provState(t *testing.T, s *Store, subID string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(`SELECT provision_state FROM stripe_subscriptions WHERE stripe_sub_id=?`, subID).Scan(&st); err != nil {
		t.Fatalf("provState %s: %v", subID, err)
	}
	return st
}

// seedDormant creates a dormant-in-grace tenant (account + sub) with a passkey, and a
// fresh stub account + sub from a re-subscription (same Stripe customer). Returns the
// dormant + stub accounts.
func seedDormant(t *testing.T, s *Store, now time.Time, auth *softAuthenticator) (dormant, stub Account) {
	t.Helper()
	// Original subscription → dormant (data + DEK kept, in grace).
	dormant, _, err := s.UpsertSubscriptionAccount(now, "sub_old", testCustomer, "cs_old", testEmail, nil)
	if err != nil {
		t.Fatalf("seed dormant: %v", err)
	}
	if err := s.SetProvisionState("sub_old", "ready"); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := s.EnterDormant("sub_old", now); err != nil {
		t.Fatalf("EnterDormant: %v", err)
	}
	if err := s.SetSubscription(dormant.AccountNumber, "canceled", nil); err != nil {
		t.Fatalf("cancel dormant account: %v", err)
	}
	if auth != nil {
		if err := s.AddWebauthnCredential(now, dormant.AccountNumber, base64url(auth.credID), auth.credentialBlob(t), "passkey"); err != nil {
			t.Fatalf("add passkey: %v", err)
		}
	}
	// Re-subscription → a brand-new stub account + sub ('pending'), NEVER adopting the
	// dormant tenant (WP-SEC1). Same Stripe customer surfaces the recovery offer.
	stub, _, err = s.UpsertSubscriptionAccount(now, "sub_new", testCustomer, "cs_new", testEmail, nil)
	if err != nil {
		t.Fatalf("seed stub: %v", err)
	}
	if stub.AccountNumber == dormant.AccountNumber {
		t.Fatal("stub adopted the dormant account — WP-SEC1 regression")
	}
	return dormant, stub
}

// --- FindDormantByCustomer ------------------------------------------------------

func TestFindDormantByCustomer(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, _ := seedDormant(t, s, now, nil)

	// Match: exactly one dormant-in-grace sub for the customer.
	acc, ten, ok, err := s.FindDormantByCustomer(testCustomer, now, dormantRetention)
	if err != nil || !ok || acc != dormant.AccountNumber || ten != dormant.TenantID {
		t.Fatalf("match: acc=%q ten=%q ok=%v err=%v", acc, ten, ok, err)
	}

	// Empty customer id → nothing (fail closed).
	if _, _, ok, _ := s.FindDormantByCustomer("", now, dormantRetention); ok {
		t.Error("empty customer id should not match")
	}

	// Grace expired → nothing.
	if _, _, ok, _ := s.FindDormantByCustomer(testCustomer, now.Add(31*24*time.Hour), dormantRetention); ok {
		t.Error("grace-expired dormant should not match")
	}

	// Non-dormant (the stub sub_new shares the customer but is 'pending') is ignored:
	// the only match is still the single dormant sub, so ok stays true and unique.
	if _, _, ok, _ := s.FindDormantByCustomer(testCustomer, now, dormantRetention); !ok {
		t.Error("a non-dormant sub with the same customer must not suppress the dormant match")
	}

	// >1 dormant-in-grace for the same customer → ambiguous → fail closed.
	if _, _, err := s.UpsertSubscriptionAccount(now, "sub_old2", testCustomer, "cs_old2", testEmail, nil); err != nil {
		t.Fatalf("second dormant: %v", err)
	}
	_ = s.EnterDormant("sub_old2", now)
	if _, _, ok, _ := s.FindDormantByCustomer(testCustomer, now, dormantRetention); ok {
		t.Error(">1 dormant match must fail closed (ok=false)")
	}
}

// --- ReattachSubscriptionToDormant ----------------------------------------------

func TestReattachSubscriptionToDormant(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, stub := seedDormant(t, s, now, nil)

	// Simulate the poller having raced ahead and provisioned the stub tenant: a second
	// sub row still pointing at the stub account. Reattach must tear it down.
	if _, err := s.db.Exec(
		`INSERT INTO stripe_subscriptions(stripe_sub_id,account_number,customer_id,checkout_session_id,provision_state,created_at) VALUES('sub_stubpod',?,?,'','ready',?)`,
		stub.AccountNumber, testCustomer, now.UTC().Format(rfc)); err != nil {
		t.Fatalf("seed raced stub pod: %v", err)
	}

	if err := s.ReattachSubscriptionToDormant("sub_new", dormant.AccountNumber, stub.AccountNumber, now, dormantRetention); err != nil {
		t.Fatalf("reattach: %v", err)
	}

	// (a) the paid sub now drives the dormant tenant.
	if _, acc, _, st, err := s.SubscriptionRowBySession("cs_new"); err != nil || acc != dormant.AccountNumber || st != "resume" {
		t.Fatalf("new sub: acc=%q st=%q err=%v, want dormant+resume", acc, st, err)
	}
	// (b) the old dormant sub is superseded.
	if st := provState(t, s, "sub_old"); st != "superseded" {
		t.Errorf("old dormant sub = %q, want superseded", st)
	}
	// (c) the recovered account is active again.
	if a, _ := s.GetAccount(dormant.AccountNumber); !a.IsActive(now) {
		t.Errorf("dormant account not active after reattach: %+v", a)
	}
	// (d) the stub account is canceled and its raced tenant deprovisioned.
	if a, _ := s.GetAccount(stub.AccountNumber); a.Status != "canceled" {
		t.Errorf("stub account = %q, want canceled", a.Status)
	}
	if st := provState(t, s, "sub_stubpod"); st != "deprovision" {
		t.Errorf("raced stub pod = %q, want deprovision", st)
	}

	// Idempotent: a repeat call is a no-op (no error, states unchanged).
	if err := s.ReattachSubscriptionToDormant("sub_new", dormant.AccountNumber, stub.AccountNumber, now, dormantRetention); err != nil {
		t.Fatalf("idempotent reattach: %v", err)
	}
	if _, _, _, st, _ := s.SubscriptionRowBySession("cs_new"); st != "resume" {
		t.Errorf("idempotent changed new sub state to %q", st)
	}
	if st := provState(t, s, "sub_old"); st != "superseded" {
		t.Errorf("idempotent changed old sub state to %q", st)
	}
}

func TestReattachSubscriptionToDormant_GraceExpired(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, stub := seedDormant(t, s, now, nil)

	// The dormant tenant fell out of grace: reattach must refuse (fail closed) and
	// leave every row untouched — never resurrect a crypto-shredded tenant.
	later := now.Add(31 * 24 * time.Hour)
	err := s.ReattachSubscriptionToDormant("sub_new", dormant.AccountNumber, stub.AccountNumber, later, dormantRetention)
	if err != ErrGraceExpired {
		t.Fatalf("reattach past grace = %v, want ErrGraceExpired", err)
	}
	if _, acc, _, st, _ := s.SubscriptionRowBySession("cs_new"); acc != stub.AccountNumber || st != "pending" {
		t.Errorf("new sub moved despite grace expiry: acc=%q st=%q", acc, st)
	}
	if st := provState(t, s, "sub_old"); st != "dormant" {
		t.Errorf("old sub changed despite grace expiry: %q", st)
	}
	if a, _ := s.GetAccount(dormant.AccountNumber); a.Status != "canceled" {
		t.Errorf("dormant account changed despite grace expiry: %q", a.Status)
	}
}

// TestReattachSubscriptionToDormant_Rollback forces a failure MID-transaction (a
// trigger that aborts step b) and asserts the whole reattach rolls back — step a's
// repoint must not survive a later failure.
func TestReattachSubscriptionToDormant_Rollback(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, stub := seedDormant(t, s, now, nil)

	if _, err := s.db.Exec(`CREATE TRIGGER fail_supersede BEFORE UPDATE OF provision_state ON stripe_subscriptions
		WHEN NEW.provision_state='superseded' BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	err := s.ReattachSubscriptionToDormant("sub_new", dormant.AccountNumber, stub.AccountNumber, now, dormantRetention)
	if err == nil {
		t.Fatal("reattach should fail when step (b) aborts")
	}
	if _, derr := s.db.Exec(`DROP TRIGGER fail_supersede`); derr != nil {
		t.Fatalf("drop trigger: %v", derr)
	}

	// Full rollback: step (a)'s repoint of sub_new must NOT have survived.
	if _, acc, _, st, _ := s.SubscriptionRowBySession("cs_new"); acc != stub.AccountNumber || st != "pending" {
		t.Errorf("partial commit after mid-tx failure: new sub acc=%q st=%q, want stub+pending", acc, st)
	}
	if st := provState(t, s, "sub_old"); st != "dormant" {
		t.Errorf("old sub changed after rollback: %q", st)
	}
}

// --- endpoint: reactivate/begin -------------------------------------------------

func reactivateRouter(t *testing.T, s *Store, now time.Time) *WebAuthnService {
	t.Helper()
	wa := testWebAuthn(t, s)
	fixed := func() time.Time { return now }
	wa.now = fixed
	wa.svc.now = fixed
	return wa
}

// F3: a dormant account with no passkey cannot be recovered here → 400 no_passkey.
func TestReactivate_BeginNoPasskey(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, _ := seedDormant(t, s, now, nil) // no passkey added

	wa := reactivateRouter(t, s, now)
	r := chi.NewRouter()
	wa.RegisterReactivation(r)

	body, _ := json.Marshal(reactivateBeginReq{Instance: dormant.TenantID, SessionID: "cs_new"})
	req := httptest.NewRequest(http.MethodPost, "/account/reactivate/begin", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no-passkey begin = %d, want 400", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "no_passkey" {
		t.Errorf("error = %q, want no_passkey", resp["error"])
	}
}

// beginReactivate drives reactivate/begin and returns the parked session id + the
// server-issued challenge (base64url).
func beginReactivate(t *testing.T, wa *WebAuthnService, instance, sessionID string) (sid, challenge string) {
	t.Helper()
	r := chi.NewRouter()
	wa.RegisterReactivation(r)
	body, _ := json.Marshal(reactivateBeginReq{Instance: instance, SessionID: sessionID})
	req := httptest.NewRequest(http.MethodPost, "/account/reactivate/begin", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate/begin = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode begin: %v", err)
	}
	sid, _ = resp["session_id"].(string)
	pk, _ := resp["publicKey"].(map[string]any)
	if pk == nil {
		t.Fatalf("no publicKey in begin response: %v", resp)
	}
	challenge, _ = pk["challenge"].(string)
	if sid == "" || challenge == "" {
		t.Fatalf("missing session_id/challenge: %v", resp)
	}
	return sid, challenge
}

func finishReactivate(t *testing.T, wa *WebAuthnService, sid string, assertion []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	wa.RegisterReactivation(r)
	req := httptest.NewRequest(http.MethodPost, "/account/reactivate/finish?s="+sid, bytes.NewReader(assertion))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// SECURITY PROOF (fail closed): an INVALID passkey assertion must NOT reattach and
// must leave every row untouched.
func TestReactivate_InvalidAssertion_NoReattach(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	auth := newSoftAuthenticator(t)
	dormant, stub := seedDormant(t, s, now, auth)
	wa := reactivateRouter(t, s, now)

	sid, challenge := beginReactivate(t, wa, dormant.TenantID, "cs_new")

	// Sign with a DIFFERENT key → the assertion is well-formed but does not verify.
	attacker, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rec := finishReactivate(t, wa, sid, auth.assertion(t, challenge, attacker))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid assertion = %d, want 401", rec.Code)
	}
	// Reattach NOT called: state is exactly as seeded.
	if _, acc, _, st, _ := s.SubscriptionRowBySession("cs_new"); acc != stub.AccountNumber || st != "pending" {
		t.Errorf("invalid assertion moved the sub: acc=%q st=%q", acc, st)
	}
	if st := provState(t, s, "sub_old"); st != "dormant" {
		t.Errorf("invalid assertion changed the dormant sub: %q", st)
	}
	if a, _ := s.GetAccount(dormant.AccountNumber); a.IsActive(now) {
		t.Error("invalid assertion reactivated the dormant account")
	}
}

// SECURITY PROOF (happy path): a VALID assertion of the dormant account's own passkey
// reattaches the subscription, resumes the tenant, and issues a token bundle.
func TestReactivate_ValidAssertion_Reattaches(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	auth := newSoftAuthenticator(t)
	dormant, stub := seedDormant(t, s, now, auth)
	wa := reactivateRouter(t, s, now)

	sid, challenge := beginReactivate(t, wa, dormant.TenantID, "cs_new")
	rec := finishReactivate(t, wa, sid, auth.assertion(t, challenge, auth.key))

	if rec.Code != http.StatusOK {
		t.Fatalf("valid assertion = %d, body %s", rec.Code, rec.Body.String())
	}
	var bundle tokenResp
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.AccessToken == "" || bundle.TenantID != dormant.TenantID {
		t.Fatalf("token bundle for wrong/empty tenant: %+v", bundle)
	}
	// Reattach happened.
	if _, acc, _, st, _ := s.SubscriptionRowBySession("cs_new"); acc != dormant.AccountNumber || st != "resume" {
		t.Errorf("valid assertion did not reattach: acc=%q st=%q", acc, st)
	}
	if st := provState(t, s, "sub_old"); st != "superseded" {
		t.Errorf("old dormant sub = %q, want superseded", st)
	}
	if a, _ := s.GetAccount(dormant.AccountNumber); !a.IsActive(now) {
		t.Error("dormant account not active after valid recovery")
	}
	if a, _ := s.GetAccount(stub.AccountNumber); a.Status != "canceled" {
		t.Errorf("stub account = %q, want canceled", a.Status)
	}
}

// --- discovery on the success page changes NO state -----------------------------

func TestSuccessPage_DiscoveryChangesNoState(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	dormant, stub := seedDormant(t, s, now, nil)

	b := NewBilling(s, whSecret)
	b.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/subscribe/success?session_id=cs_new", nil)
	rec := httptest.NewRecorder()
	b.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("success = %d", rec.Code)
	}
	page := rec.Body.String()
	// The recovery offer is surfaced (banner + the dormant slug).
	if !bytes.Contains([]byte(page), []byte("Recover with your passkey")) {
		t.Error("success page missing the recovery banner")
	}
	if !bytes.Contains([]byte(page), []byte(dormant.TenantID)) {
		t.Errorf("success page missing the dormant slug %q", dormant.TenantID)
	}
	// Discovery is read-only: NOTHING moved.
	if _, acc, _, st, _ := s.SubscriptionRowBySession("cs_new"); acc != stub.AccountNumber || st != "pending" {
		t.Errorf("discovery mutated the stub sub: acc=%q st=%q", acc, st)
	}
	if st := provState(t, s, "sub_old"); st != "dormant" {
		t.Errorf("discovery mutated the dormant sub: %q", st)
	}
	if a, _ := s.GetAccount(dormant.AccountNumber); a.Status != "canceled" {
		t.Errorf("discovery mutated the dormant account: %q", a.Status)
	}
}
