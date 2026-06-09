package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Billing turns Stripe subscription events into accounts + provisioning requests.
// Invariants (product spec):
//   - act ONLY on a finalized, paid checkout — never on created/failed/unpaid;
//   - idempotent on the Stripe subscription id (PK) — retries, replays and
//     success-page reloads never create a second account;
//   - NEVER provision inline. The webhook records a 'pending' provisioning row;
//     the out-of-band poller (which alone holds cluster rights — the
//     internet-facing console has none) creates the pod and flips it to 'ready'.
//   - no email — the enrollment code is delivered on the post-payment page.
type Billing struct {
	store     *Store
	secret    string // Stripe webhook signing secret (whsec_…)
	tolerance time.Duration
	now       func() time.Time
}

// NewBilling wires the billing webhook + success page to the admin store.
func NewBilling(store *Store, webhookSecret string) *Billing {
	return &Billing{store: store, secret: webhookSecret, tolerance: 5 * time.Minute, now: time.Now}
}

func (b *Billing) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Routes exposes the Stripe webhook + the post-payment success page.
func (b *Billing) Routes() http.Handler {
	r := chi.NewRouter()
	b.Register(r)
	return r
}

// Register mounts the billing routes on r (compose alongside Service.Register).
func (b *Billing) Register(r chi.Router) {
	r.Post("/stripe/webhook", b.handleWebhook)
	r.Get("/subscribe/success", b.handleSuccess)
}

// --- Stripe event shapes (only the fields we use) ---------------------------

type stripeEvent struct {
	Type string `json:"type"`
	Data struct {
		Object stripeObject `json:"object"`
	} `json:"data"`
}

type stripeObject struct {
	ID              string `json:"id"`
	Mode            string `json:"mode"`
	PaymentStatus   string `json:"payment_status"`
	Customer        string `json:"customer"`
	Subscription    string `json:"subscription"`
	CustomerEmail   string `json:"customer_email"`
	CustomerDetails struct {
		Email string `json:"email"`
	} `json:"customer_details"`
}

func (o stripeObject) email() string {
	if o.CustomerEmail != "" {
		return o.CustomerEmail
	}
	return o.CustomerDetails.Email
}

func (b *Billing) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body")
		return
	}
	if !verifyStripeSig(r.Header.Get("Stripe-Signature"), body, b.secret, b.clock(), b.tolerance) {
		writeErr(w, http.StatusBadRequest, "invalid signature")
		return
	}
	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	obj := ev.Data.Object
	now := b.clock()

	switch ev.Type {
	case "checkout.session.completed":
		// Record the account + a 'pending' provisioning row ONLY on a finalized,
		// paid subscription checkout. Idempotent on the subscription id. The poller
		// turns 'pending' into a live pod — nothing is provisioned here.
		if obj.Mode != "subscription" || obj.PaymentStatus != "paid" ||
			obj.Subscription == "" || obj.email() == "" {
			break
		}
		if _, _, err := b.store.UpsertSubscriptionAccount(now, obj.Subscription, obj.Customer, obj.ID, obj.email(), nil); err != nil {
			writeErr(w, http.StatusInternalServerError, "account")
			return
		}

	case "customer.subscription.deleted":
		// Suspend auth immediately + queue the pod for reaping by the poller.
		if err := b.store.SetSubscriptionBySub(obj.ID, "canceled", &now); err != nil {
			writeErr(w, http.StatusInternalServerError, "suspend")
			return
		}
		_ = b.store.SetProvisionState(obj.ID, "deprovision")

	case "invoice.payment_failed":
		if err := b.store.SetSubscriptionBySub(obj.Subscription, "past_due", &now); err != nil {
			writeErr(w, http.StatusInternalServerError, "suspend")
			return
		}
		// Queue the live pod for scale-to-0 so its scheduler stops calling the LLM
		// (per-token cost) during Stripe's dunning window. No-op if not yet 'ready'.
		_ = b.store.SuspendIfReady(obj.Subscription)

	case "invoice.paid":
		if err := b.store.SetSubscriptionBySub(obj.Subscription, "active", nil); err != nil {
			writeErr(w, http.StatusInternalServerError, "reactivate")
			return
		}
		// Bring a suspended tenant back online (scale-to-1). No-op on first/normal
		// payments, so it never disturbs a pending→ready provisioning.
		_ = b.store.ResumeIfSuspended(obj.Subscription)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var successPage = template.Must(template.New("s").Parse(`<!doctype html><html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hygur Cloud</title><style>
body{font-family:ui-sans-serif,system-ui,sans-serif;background:#0f0f10;color:#e9e9ea;display:grid;place-items:center;min-height:100vh;margin:0}
.card{max-width:30rem;padding:2.5rem;text-align:center}
h1{font-size:1.5rem;margin:0 0 .5rem}p{color:#9a9a9c;line-height:1.5}
code{display:block;font-size:1.4rem;letter-spacing:.12em;background:#1b1b1d;border:1px solid #2a2a2d;border-radius:.6rem;padding:1rem;margin:1.5rem 0}
.muted{font-size:.85rem}
</style></head><body><div class="card">
{{if .Ready}}<h1>Welcome to Hygur Cloud</h1>
<p>Your private space is ready. Open the Hygur app and paste this one-time enrollment code:</p>
<code>{{.Code}}</code>
<p class="muted">The code expires in 30 minutes. Lost it? Reload this page.</p>
{{else}}<h1>Setting up your space…</h1>
<p>Payment received — we're preparing your private Hygur instance. This takes a moment; refresh this page shortly to get your enrollment code.</p>
{{end}}</div></body></html>`))

// handleSuccess is the Stripe post-payment landing. It resolves the checkout
// session → account and, once the poller has provisioned the tenant (state
// 'ready'), mints + shows a one-time enrollment code. No email.
func (b *Billing) handleSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	acc, state, err := b.store.SubscriptionBySession(sid)
	if err != nil || state != "ready" {
		// Unknown session (webhook not arrived) or pod not provisioned yet.
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	code, err := b.store.CreateEnrollCode(b.clock(), acc.AccountNumber, "web", 30*time.Minute)
	if err != nil {
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	_ = successPage.Execute(w, map[string]any{"Ready": true, "Code": code})
}

// verifyStripeSig validates a `Stripe-Signature: t=…,v1=…` header: HMAC-SHA256 of
// `t.payload` keyed by the webhook secret, with a replay-window check on `t`.
func verifyStripeSig(header string, payload []byte, secret string, now time.Time, tolerance time.Duration) bool {
	if secret == "" || header == "" {
		return false
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == "" || len(sigs) == 0 {
		return false
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if tolerance > 0 {
		delta := now.Unix() - tsInt
		if delta < 0 {
			delta = -delta
		}
		if time.Duration(delta)*time.Second > tolerance {
			return false
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(payload)))
	expected := hex.EncodeToString(mac.Sum(nil))
	for _, sig := range sigs {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return true
		}
	}
	return false
}

// --- Store: subscription → account + provisioning state ---------------------

// ProvisionRow is a tenant awaiting (or leaving) provisioning, for the poller.
type ProvisionRow struct {
	SubID     string
	Account   string
	TenantID  string
	SessionID string
	State     string
}

// UpsertSubscriptionAccount maps a Stripe subscription to an account, creating it
// (with provision_state='pending') on first sight and returning the SAME account
// on every later call for that subscription (idempotent). Always refreshes the
// billing status to active. The bool is true only when a new account was created.
func (s *Store) UpsertSubscriptionAccount(now time.Time, subID, customerID, sessionID, email string, validUntil *time.Time) (Account, bool, error) {
	if subID == "" || email == "" {
		return Account{}, false, errors.New("controlplane: subscription id + email required")
	}
	var accNum string
	err := s.db.QueryRow(`SELECT account_number FROM stripe_subscriptions WHERE stripe_sub_id=?`, subID).Scan(&accNum)
	if err == nil {
		if uerr := s.SetSubscription(accNum, "active", validUntil); uerr != nil {
			return Account{}, false, uerr
		}
		if sessionID != "" {
			_, _ = s.db.Exec(`UPDATE stripe_subscriptions SET checkout_session_id=? WHERE stripe_sub_id=?`, sessionID, subID)
		}
		acc, gerr := s.GetAccount(accNum)
		return acc, false, gerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, fmt.Errorf("controlplane: lookup subscription: %w", err)
	}

	acc, cerr := s.CreateAccount(now, email, "active", validUntil)
	if cerr != nil {
		existing, gerr := s.getAccountByEmail(email)
		if gerr != nil {
			return Account{}, false, cerr
		}
		acc = existing
		_ = s.SetSubscription(acc.AccountNumber, "active", validUntil)
	}
	if _, ierr := s.db.Exec(
		`INSERT INTO stripe_subscriptions(stripe_sub_id,account_number,customer_id,checkout_session_id,provision_state,created_at) VALUES(?,?,?,?, 'pending', ?)`,
		subID, acc.AccountNumber, customerID, sessionID, now.UTC().Format(rfc),
	); ierr != nil {
		return Account{}, false, fmt.Errorf("controlplane: map subscription: %w", ierr)
	}
	return acc, true, nil
}

// SetSubscriptionBySub updates the account's billing status from a Stripe
// lifecycle event (canceled / past_due / active). Unknown subscription = no-op.
// A non active/trialing status makes Account.IsActive false, so the control plane
// stops issuing/refreshing tokens for that tenant.
func (s *Store) SetSubscriptionBySub(subID, status string, validUntil *time.Time) error {
	if subID == "" {
		return nil
	}
	var accNum string
	err := s.db.QueryRow(`SELECT account_number FROM stripe_subscriptions WHERE stripe_sub_id=?`, subID).Scan(&accNum)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("controlplane: lookup subscription: %w", err)
	}
	return s.SetSubscription(accNum, status, validUntil)
}

// SetProvisionState moves a subscription through pending→ready / →suspend→suspended /
// →resume / →deprovision→gone→purged. Setting 'ready' stamps provisioned_at; 'gone'
// stamps reaped_at (the retention clock). Used by the poller.
func (s *Store) SetProvisionState(subID, state string) error {
	switch state {
	case "ready":
		_, err := s.db.Exec(`UPDATE stripe_subscriptions SET provision_state=?, provisioned_at=? WHERE stripe_sub_id=?`,
			state, time.Now().UTC().Format(rfc), subID)
		return err
	case "gone":
		_, err := s.db.Exec(`UPDATE stripe_subscriptions SET provision_state=?, reaped_at=? WHERE stripe_sub_id=?`,
			state, time.Now().UTC().Format(rfc), subID)
		return err
	}
	_, err := s.db.Exec(`UPDATE stripe_subscriptions SET provision_state=? WHERE stripe_sub_id=?`, state, subID)
	return err
}

// SuspendIfReady queues a tenant for scale-to-0 after a failed payment, but only
// if its pod is currently live ('ready'). A miss (pending / already-suspended /
// gone) is a no-op, so dunning on a not-yet-provisioned tenant is safe.
func (s *Store) SuspendIfReady(subID string) error {
	if subID == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE stripe_subscriptions SET provision_state='suspend' WHERE stripe_sub_id=? AND provision_state='ready'`,
		subID)
	return err
}

// ResumeIfSuspended queues a recovered tenant for scale-to-1 on a successful
// payment, but only if it was suspended (or mid-suspend) — so a first/normal
// invoice.paid never disturbs a pending→ready provisioning.
func (s *Store) ResumeIfSuspended(subID string) error {
	if subID == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE stripe_subscriptions SET provision_state='resume' WHERE stripe_sub_id=? AND provision_state IN ('suspend','suspended')`,
		subID)
	return err
}

// ListProvisions returns subscriptions in the given provision_state (e.g.
// 'pending' to create, 'suspend'/'resume' to scale, 'deprovision' to reap) —
// the poller's work queue.
func (s *Store) ListProvisions(state string) ([]ProvisionRow, error) {
	rows, err := s.db.Query(`
		SELECT ss.stripe_sub_id, ss.account_number, a.tenant_id, ss.checkout_session_id, ss.provision_state
		FROM stripe_subscriptions ss JOIN accounts a ON a.account_number = ss.account_number
		WHERE ss.provision_state = ?`, state)
	if err != nil {
		return nil, err
	}
	return scanProvisionRows(rows)
}

// ListPurgeable returns reaped ('gone') tenants whose retention window has elapsed
// (reaped_at older than `retention` before now) — the disk-reclaim work queue. The
// PV is Retain and the tenant DEK was destroyed with its namespace, so the data is
// already crypto-shredded; this only reclaims the orphaned host directory.
func (s *Store) ListPurgeable(now time.Time, retention time.Duration) ([]ProvisionRow, error) {
	cutoff := now.Add(-retention).UTC().Format(rfc)
	rows, err := s.db.Query(`
		SELECT ss.stripe_sub_id, ss.account_number, a.tenant_id, ss.checkout_session_id, ss.provision_state
		FROM stripe_subscriptions ss JOIN accounts a ON a.account_number = ss.account_number
		WHERE ss.provision_state='gone' AND ss.reaped_at IS NOT NULL AND ss.reaped_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	return scanProvisionRows(rows)
}

func scanProvisionRows(rows *sql.Rows) ([]ProvisionRow, error) {
	defer rows.Close()
	var out []ProvisionRow
	for rows.Next() {
		var r ProvisionRow
		if err := rows.Scan(&r.SubID, &r.Account, &r.TenantID, &r.SessionID, &r.State); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountActiveTenants counts subscriptions with a live (ready/pending) pod — the
// poller enforces a cap against this to protect the single node.
func (s *Store) CountActiveTenants() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM stripe_subscriptions WHERE provision_state IN ('pending','ready')`).Scan(&n)
	return n, err
}

// SubscriptionBySession resolves a checkout session to its account + provision
// state (for the post-payment success page).
func (s *Store) SubscriptionBySession(sessionID string) (Account, string, error) {
	if sessionID == "" {
		return Account{}, "", ErrNotFound
	}
	var accNum, state string
	err := s.db.QueryRow(`SELECT account_number, provision_state FROM stripe_subscriptions WHERE checkout_session_id=?`, sessionID).Scan(&accNum, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, "", ErrNotFound
	}
	if err != nil {
		return Account{}, "", err
	}
	acc, gerr := s.GetAccount(accNum)
	return acc, state, gerr
}

func (s *Store) getAccountByEmail(email string) (Account, error) {
	row := s.db.QueryRow(
		`SELECT account_number,email,tenant_id,status,valid_until,created_at FROM accounts WHERE email=?`, email)
	return scanAccount(row)
}
