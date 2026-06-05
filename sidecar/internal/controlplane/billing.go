package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Provisioner creates the tenant's runtime (k8s namespace + workload). The real
// implementation talks to the cluster; tests use a fake. It MUST be safe to call
// for an already-provisioned account (the store's claim makes that rare, but a
// belt-and-braces idempotent Provision is preferred).
type Provisioner interface {
	Provision(ctx context.Context, acc Account) error
}

// Billing turns Stripe subscription events into provisioned accounts. It is the
// public, payment-facing half of the control plane (separate from the device
// enroll/refresh Service). Invariants (per product spec):
//   - act ONLY on a finalized, paid checkout — never on created/failed/unpaid;
//   - idempotent on the Stripe subscription id — retries, replays and success-page
//     reloads never create a second account or provision a second pod;
//   - no email — the enrollment code is delivered on the post-payment page.
type Billing struct {
	store       *Store
	provisioner Provisioner
	secret      string // Stripe webhook signing secret (whsec_…)
	tolerance   time.Duration
	now         func() time.Time
}

// NewBilling wires the billing webhook to the admin store + a provisioner.
func NewBilling(store *Store, webhookSecret string, p Provisioner) *Billing {
	return &Billing{store: store, provisioner: p, secret: webhookSecret, tolerance: 5 * time.Minute, now: time.Now}
}

func (b *Billing) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Routes exposes the Stripe webhook. (The post-payment success page that surfaces
// the enrollment code is wired separately, once the tenant host topology lands.)
func (b *Billing) Routes() http.Handler {
	r := chi.NewRouter()
	b.Register(r)
	return r
}

// Register mounts the Stripe webhook on r (compose alongside Service.Register).
func (b *Billing) Register(r chi.Router) {
	r.Post("/stripe/webhook", b.handleWebhook)
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

	// Gate hard: only a finalized, PAID subscription checkout provisions anything.
	// Everything else is acknowledged (200) but does nothing — so a declined
	// payment, a retry, a reload or any other event type never loops provisioning.
	obj := ev.Data.Object
	if ev.Type != "checkout.session.completed" || obj.Mode != "subscription" ||
		obj.PaymentStatus != "paid" || obj.Subscription == "" || obj.email() == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"ignored": true})
		return
	}

	now := b.clock()
	acc, _, err := b.store.UpsertSubscriptionAccount(now, obj.Subscription, obj.Customer, obj.ID, obj.email(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "account")
		return
	}
	claimed, err := b.store.ClaimProvisioning(now, obj.Subscription)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "claim")
		return
	}
	if claimed && b.provisioner != nil {
		if perr := b.provisioner.Provision(r.Context(), acc); perr != nil {
			// Release so a later (retried) event can try again — no silent loss.
			_ = b.store.ReleaseProvisioning(obj.Subscription)
			writeErr(w, http.StatusInternalServerError, "provision")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "account": acc.AccountNumber})
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
			return false // outside the replay window
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

// --- Store: subscription → account idempotency ------------------------------

// UpsertSubscriptionAccount maps a Stripe subscription to an account, creating it
// on first sight and returning the SAME account on every later call for that
// subscription (idempotent). Always refreshes the billing status to active. The
// bool is true only when a new account was created.
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

	// First time for this subscription. Reuse an existing account for the same
	// email (re-subscribe), else create one.
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
		`INSERT INTO stripe_subscriptions(stripe_sub_id,account_number,customer_id,checkout_session_id,provisioned_at,created_at) VALUES(?,?,?,?,NULL,?)`,
		subID, acc.AccountNumber, customerID, sessionID, now.UTC().Format(rfc),
	); ierr != nil {
		return Account{}, false, fmt.Errorf("controlplane: map subscription: %w", ierr)
	}
	return acc, true, nil
}

// ClaimProvisioning atomically marks a subscription provisioned, returning true
// only to the first caller — so the tenant pod is created exactly once even under
// duplicate/replayed events. Reset with ReleaseProvisioning if provisioning fails.
func (s *Store) ClaimProvisioning(now time.Time, subID string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE stripe_subscriptions SET provisioned_at=? WHERE stripe_sub_id=? AND provisioned_at IS NULL`,
		now.UTC().Format(rfc), subID,
	)
	if err != nil {
		return false, fmt.Errorf("controlplane: claim provisioning: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReleaseProvisioning clears the provisioned claim so a later event retries.
func (s *Store) ReleaseProvisioning(subID string) error {
	_, err := s.db.Exec(`UPDATE stripe_subscriptions SET provisioned_at=NULL WHERE stripe_sub_id=?`, subID)
	return err
}

func (s *Store) getAccountByEmail(email string) (Account, error) {
	row := s.db.QueryRow(
		`SELECT account_number,email,tenant_id,status,valid_until,created_at FROM accounts WHERE email=?`, email)
	return scanAccount(row)
}
