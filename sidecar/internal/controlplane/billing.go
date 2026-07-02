package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"
)

// cloudShellURL is the web-shell origin a customer opens their space on. The
// success page deep-links here with the enrollment code pre-loaded.
func cloudShellURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("HYGUR_CLOUD_SHELL_URL")), "/"); v != "" {
		return v
	}
	return "https://cloud.hygur.ai"
}

// dormantRetention is the reactivation grace: a fully-canceled tenant stays dormant
// (data + DEK kept, pod scaled to 0) for this long before it is crypto-shredded.
// The poller's `dormant-expired`/`purgeable` reapers default to the same 30 days
// (main.go flags); the success-page discovery + passkey recovery reuse it so a match
// is only ever offered while the tenant is still resurrectable.
const dormantRetention = 30 * 24 * time.Hour

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
		// Access ends now (period end, or dunning exhausted): suspend auth, then
		// enter the 30-day reactivation grace instead of reaping. The pod is scaled
		// to 0 but the namespace, DEK, PV and vhost are KEPT, so a late renewal
		// restores the data intact. Crypto-shred happens only when the grace
		// elapses (see docs/TENANT_LIFECYCLE.md).
		if err := b.store.SetSubscriptionBySub(obj.ID, "canceled", &now); err != nil {
			writeErr(w, http.StatusInternalServerError, "suspend")
			return
		}
		_ = b.store.EnterDormant(obj.ID, now)

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
{{if not .Ready}}{{if not .Recover}}<meta http-equiv="refresh" content="8">{{end}}{{end}}
<title>Hygur Cloud</title><style>
:root{--bg:#fbfaf7;--surface:#fff;--text:#1b1b18;--muted:#6b6b63;--faint:#9a978c;--accent:#2e6a57;--border:#e7e3d8}
*{box-sizing:border-box}
body{font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;background:var(--bg);color:var(--text);display:grid;place-items:center;min-height:100vh;margin:0;padding:1.5rem}
.card{max-width:30rem;width:100%;background:var(--surface);border:1px solid var(--border);border-radius:1rem;padding:2.25rem;text-align:center;box-shadow:0 1px 3px rgba(0,0,0,.04)}
h1{font-family:ui-serif,Georgia,"Times New Roman",serif;font-size:1.6rem;font-weight:600;margin:0 0 .4rem}
p{color:var(--muted);line-height:1.55;margin:.5rem 0}
.muted{font-size:.85rem;color:var(--faint)}
.space{margin:1.25rem 0 .2rem;font-family:ui-serif,Georgia,serif;font-size:1.35rem;color:var(--accent)}
.url{font-family:ui-monospace,Menlo,monospace;font-size:.9rem;color:var(--muted);word-break:break-all}
.btn{display:inline-block;margin:1.25rem 0 .25rem;background:var(--accent);color:#fff;text-decoration:none;font-weight:600;padding:.7rem 1.4rem;border-radius:.6rem;border:0;cursor:pointer;font-size:1rem}
.btn:disabled{opacity:.55;cursor:default}
.qr{margin:1.1rem auto .2rem;width:180px;height:180px;border:1px solid var(--border);border-radius:.6rem;padding:.4rem;background:#fff}
code{display:block;font-family:ui-monospace,Menlo,monospace;font-size:1.25rem;letter-spacing:.1em;background:var(--bg);border:1px solid var(--border);border-radius:.6rem;padding:.85rem;margin:.75rem 0;word-break:break-all}
.note{margin-top:1.25rem;padding-top:1rem;border-top:1px solid var(--border)}
.recover{background:#eef6f1;border:1px solid #cfe6db;border-radius:.8rem;padding:1.25rem;margin:0 0 1.4rem}
.recover h1{color:var(--accent)}
.recover-alt{margin-top:1.4rem;text-align:left}
.recover-alt summary{cursor:pointer;color:var(--accent);font-weight:600;text-align:center}
.recover-alt input{width:100%;font-family:ui-monospace,Menlo,monospace;font-size:.95rem;padding:.6rem;border:1px solid var(--border);border-radius:.5rem;margin:.7rem 0}
.status{min-height:1.1rem}
</style></head><body><div class="card">
{{if .Recover}}<div class="recover">
<h1>Welcome back</h1>
<p>You already have a Hygur space — <strong>{{.DormantSlug}}</strong> — from before. Recover it with your passkey and your data and settings come back intact, instead of starting over with an empty space.</p>
<button class="btn" id="recover-btn" type="button">Recover with your passkey →</button>
<p class="muted status" id="recover-status" role="status"></p>
<p class="muted">Prefer a clean slate? Just continue with the new space below.</p>
</div>{{end}}
{{if .Ready}}<h1>Welcome to Hygur Cloud</h1>
<p>Your instance name, which you'll use to sign in, is:</p>
<div class="space">{{.Slug}}</div>
<div class="url">{{.URL}}</div>
<a class="btn" href="{{.DeepLink}}" rel="noreferrer">Open your space →</a>
{{if .QR}}<img class="qr" src="{{.QR}}" alt="Scan to open your space"><p class="muted">Scan to open on your phone</p>{{end}}
<div class="note">
<p class="muted">Bookmark <strong>{{.URL}}</strong>. To sign in later, open it and enter this one-time code:</p>
<code>{{.Code}}</code>
<p class="muted">Expires in 30 minutes and works once; reload this page for a fresh one. On first open, <strong>add a passkey</strong> so you can sign in from any device. Without one, you can only return from this browser.</p>
</div>
{{else}}<h1>Setting up your space…</h1>
<p>Payment received. We're creating your private, encrypted space: its own database with its own encryption key.</p>
<p class="muted"><strong>Keep this tab open.</strong> Your space and a one-time enrollment code appear here as soon as it's ready (usually under a minute). We don't send it by email — this page refreshes itself.</p>
{{end}}
{{if .HasSession}}<details class="note recover-alt">
<summary>Recover a different space</summary>
<p class="muted">Already had a Hygur space under another name? Enter it and sign in with that space's passkey to bring it back.</p>
<input id="recover-instance" type="text" placeholder="brave-azure-harbor" autocapitalize="off" autocorrect="off" autocomplete="off" spellcheck="false">
<button class="btn" id="recover-alt-btn" type="button">Recover →</button>
<p class="muted status" id="recover-alt-status" role="status"></p>
</details>
<div id="recover-cfg" data-session="{{.SessionID}}" data-slug="{{.DormantSlug}}" data-cloud="{{.CloudURL}}" hidden></div>
<script>
(function(){
  var cfg = document.getElementById("recover-cfg");
  if(!cfg) return;
  var SESSION = cfg.dataset.session || "";
  var CLOUD = (cfg.dataset.cloud || "").replace(/\/+$/, "");
  function b64urlToBuf(s){
    s = String(s).replace(/-/g, "+").replace(/_/g, "/");
    while(s.length % 4) s += "=";
    var bin = atob(s), buf = new Uint8Array(bin.length), i;
    for(i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
    return buf.buffer;
  }
  function bufToB64url(buf){
    var bytes = new Uint8Array(buf), bin = "", i;
    for(i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }
  function setStatus(el, msg){ if(el) el.textContent = msg; }
  async function recover(instance, statusEl, btn){
    instance = (instance || "").trim().toLowerCase();
    if(!instance){ setStatus(statusEl, "Enter your space name."); return; }
    if(!window.PublicKeyCredential){ setStatus(statusEl, "This browser does not support passkeys."); return; }
    if(btn) btn.disabled = true;
    setStatus(statusEl, "Starting...");
    try {
      var beginResp = await fetch("/account/reactivate/begin", {
        method: "POST", credentials: "include",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({instance: instance, session_id: SESSION})
      });
      if(!beginResp.ok){
        var e = {};
        try { e = await beginResp.json(); } catch(_){}
        setStatus(statusEl, (e && e.error === "no_passkey")
          ? "That space has no passkey, so it cannot be recovered here."
          : "Could not start recovery for that space.");
        if(btn) btn.disabled = false;
        return;
      }
      var opts = await beginResp.json();
      var pk = opts.publicKey || {};
      pk.challenge = b64urlToBuf(pk.challenge);
      if(pk.allowCredentials){
        pk.allowCredentials = pk.allowCredentials.map(function(c){
          return Object.assign({}, c, {id: b64urlToBuf(c.id)});
        });
      }
      setStatus(statusEl, "Waiting for your passkey...");
      var assertion = await navigator.credentials.get({publicKey: pk});
      var rp = assertion.response;
      var body = {
        id: assertion.id,
        rawId: bufToB64url(assertion.rawId),
        type: assertion.type,
        response: {
          clientDataJSON: bufToB64url(rp.clientDataJSON),
          authenticatorData: bufToB64url(rp.authenticatorData),
          signature: bufToB64url(rp.signature),
          userHandle: rp.userHandle ? bufToB64url(rp.userHandle) : null
        }
      };
      var finishResp = await fetch("/account/reactivate/finish?s=" + encodeURIComponent(opts.session_id), {
        method: "POST", credentials: "include",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(body)
      });
      if(!finishResp.ok){
        setStatus(statusEl, finishResp.status === 409
          ? "That space's recovery window has closed."
          : "Passkey did not verify. Please try again.");
        if(btn) btn.disabled = false;
        return;
      }
      var bundle = await finishResp.json();
      setStatus(statusEl, "Recovered - opening your space...");
      window.location.href = CLOUD + "/" + encodeURIComponent(bundle.tenant_id || instance);
    } catch(err){
      setStatus(statusEl, "Recovery was cancelled or failed. Please try again.");
      if(btn) btn.disabled = false;
    }
  }
  var autoBtn = document.getElementById("recover-btn");
  if(autoBtn) autoBtn.addEventListener("click", function(){
    recover(cfg.dataset.slug || "", document.getElementById("recover-status"), autoBtn);
  });
  var altBtn = document.getElementById("recover-alt-btn");
  if(altBtn) altBtn.addEventListener("click", function(){
    var input = document.getElementById("recover-instance");
    recover(input ? input.value : "", document.getElementById("recover-alt-status"), altBtn);
  });
})();
</script>{{end}}</div></body></html>`))

// handleSuccess is the Stripe post-payment landing. It resolves the checkout
// session → account and, once the poller has provisioned the tenant (state
// 'ready'), mints + shows a one-time enrollment code. No email.
func (b *Billing) handleSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The ready page embeds a live one-time enroll code — never cache it, and
	// don't leak the page URL via Referer when the user taps through to the shell.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	acc, state, err := b.store.SubscriptionBySession(sid)
	if err != nil {
		// Unknown session (webhook not arrived yet).
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	// Discovery ONLY (changes NO state): if the paying Stripe customer still has a
	// dormant tenant in grace on a DIFFERENT account, surface a "recover with your
	// passkey" offer. The passkey assertion is the sole security gate — matching the
	// customer id never re-attaches anything, it only shows the button.
	offerRecover, dormantSlug := false, ""
	if _, _, custID, _, rerr := b.store.SubscriptionRowBySession(sid); rerr == nil {
		if dAcc, dSlug, ok, ferr := b.store.FindDormantByCustomer(custID, b.clock(), dormantRetention); ferr == nil && ok && dAcc != acc.AccountNumber {
			offerRecover, dormantSlug = true, dSlug
		}
	}
	// Fields shared by every page that resolved a session, so the recover banner +
	// inline ceremony + the "recover a different space" fallback (F2) can render.
	data := map[string]any{
		"HasSession":  true,
		"Recover":     offerRecover,
		"DormantSlug": dormantSlug,
		"SessionID":   sid,
		"CloudURL":    cloudShellURL(),
	}
	if state != "ready" {
		// Pod not provisioned yet — still offer recovery (and don't auto-refresh over
		// an in-progress ceremony; the template drops the meta-refresh when Recover).
		data["Ready"] = false
		_ = successPage.Execute(w, data)
		return
	}
	code, err := b.store.CreateEnrollCode(b.clock(), acc.AccountNumber, "web", 30*time.Minute)
	if err != nil {
		_ = successPage.Execute(w, map[string]any{"Ready": false})
		return
	}
	// Deep-link to the web shell with the code pre-loaded + a QR for mobile.
	shell := cloudShellURL()
	slug := acc.TenantID
	displayURL := strings.TrimPrefix(strings.TrimPrefix(shell, "https://"), "http://") + "/" + slug
	deepLink := shell + "/" + slug + "?code=" + code
	var qr template.URL
	if png, qerr := qrcode.Encode(deepLink, qrcode.Medium, 220); qerr == nil {
		qr = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	}
	data["Ready"] = true
	data["Code"] = code
	data["Slug"] = slug
	data["URL"] = displayURL
	data["DeepLink"] = template.URL(deepLink)
	data["QR"] = qr
	_ = successPage.Execute(w, data)
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

	// A new subscription ALWAYS creates a new account/tenant. We never adopt a
	// pre-existing account by matching its email: the checkout email is TYPED BY THE
	// PAYER and is not proof of ownership, so adopting by it let anyone paying with a
	// victim's email bind to — and take over — the victim's tenant (WP-SEC1).
	// Recovering a dormant tenant after a full cancellation is a passkey-authenticated
	// action, never an automatic webhook path. Email is a non-unique contact attribute.
	acc, err := s.CreateAccount(now, email, "active", validUntil)
	if err != nil {
		return Account{}, false, err
	}
	if err := s.insertSubscriptionRow(now, subID, acc.AccountNumber, customerID, sessionID); err != nil {
		return Account{}, false, err
	}
	return acc, true, nil
}

// insertSubscriptionRow records the new subscription and decides its initial
// provisioning state ATOMICALLY: if the account still has a tenant in the
// reactivation grace ('dormant'), the new subscription ADOPTS it — inserted as
// 'resume' (poller scales the existing pod back to 1, data + DEK intact) and the
// dormant row retired to 'superseded' — instead of 'pending', which would
// re-provision a fresh pod and overwrite the live DEK. All in one transaction so
// no poller tick can ever observe the new row as 'pending' against a live tenant.
func (s *Store) insertSubscriptionRow(now time.Time, subID, account, customerID, sessionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("controlplane: map subscription: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var dormantSub string
	derr := tx.QueryRow(
		`SELECT stripe_sub_id FROM stripe_subscriptions WHERE account_number=? AND provision_state='dormant' ORDER BY dormant_at DESC LIMIT 1`,
		account).Scan(&dormantSub)
	if derr != nil && !errors.Is(derr, sql.ErrNoRows) {
		return fmt.Errorf("controlplane: lookup dormant: %w", derr)
	}

	state := "pending"
	if dormantSub != "" {
		state = "resume"
	}
	if _, ierr := tx.Exec(
		`INSERT INTO stripe_subscriptions(stripe_sub_id,account_number,customer_id,checkout_session_id,provision_state,created_at) VALUES(?,?,?,?,?,?)`,
		subID, account, customerID, sessionID, state, now.UTC().Format(rfc),
	); ierr != nil {
		return fmt.Errorf("controlplane: map subscription: %w", ierr)
	}
	if dormantSub != "" {
		if _, uerr := tx.Exec(
			`UPDATE stripe_subscriptions SET provision_state='superseded', dormant_at=NULL WHERE stripe_sub_id=? AND provision_state='dormant'`,
			dormantSub); uerr != nil {
			return fmt.Errorf("controlplane: retire dormant: %w", uerr)
		}
	}
	return tx.Commit()
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

// RequeueFailed moves a 'failed' subscription back to 'pending' so the poller
// retries provisioning on its next pass. Guarded to failed→pending only, so it
// can never re-provision a ready/suspended/reaped tenant. Returns ErrNotFound if
// the subscription is not in 'failed' (already pending/ready, or unknown).
func (s *Store) RequeueFailed(subID string) error {
	res, err := s.db.Exec(
		`UPDATE stripe_subscriptions SET provision_state='pending' WHERE stripe_sub_id=? AND provision_state='failed'`,
		subID)
	if err != nil {
		return fmt.Errorf("controlplane: requeue: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no failed subscription %q (already pending/ready, or unknown)", ErrNotFound, subID)
	}
	return nil
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

// EnterDormant moves a canceled subscription into the 30-day reactivation grace:
// provision_state='dormant' + dormant_at stamped (the erasure clock). The poller
// scales the pod to 0 but keeps the namespace, DEK, PV and vhost so a late
// renewal can resume it with its data intact. Guarded so it never disturbs an
// already-erased tenant ('gone'/'purged') or an adopted one ('superseded').
func (s *Store) EnterDormant(subID string, now time.Time) error {
	if subID == "" {
		return nil
	}
	_, err := s.db.Exec(
		`UPDATE stripe_subscriptions SET provision_state='dormant', dormant_at=? WHERE stripe_sub_id=? AND provision_state NOT IN ('gone','purged','superseded')`,
		now.UTC().Format(rfc), subID)
	return err
}

// ListDormantExpired returns dormant tenants whose grace window has elapsed
// (dormant_at older than `retention` before now) — the erasure work queue. At
// this point the poller crypto-shreds the tenant (delete namespace → DEK gone)
// and purges its PV + backups. The 30-day grace IS the retention, so erasure is
// done in one step (no second post-reap window on this path).
func (s *Store) ListDormantExpired(now time.Time, retention time.Duration) ([]ProvisionRow, error) {
	cutoff := now.Add(-retention).UTC().Format(rfc)
	rows, err := s.db.Query(`
		SELECT ss.stripe_sub_id, ss.account_number, a.tenant_id, ss.checkout_session_id, ss.provision_state
		FROM stripe_subscriptions ss JOIN accounts a ON a.account_number = ss.account_number
		WHERE ss.provision_state='dormant' AND ss.dormant_at IS NOT NULL AND ss.dormant_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	return scanProvisionRows(rows)
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

// SubscriptionRowBySession resolves a checkout session to its subscription id,
// account number, Stripe customer id, and provision state — the raw row behind the
// success page (SubscriptionBySession returns the joined Account instead). Used to
// resolve the fresh stub subscription during passkey reactivation and to read the
// customer id for discovery. Unknown session → ErrNotFound.
func (s *Store) SubscriptionRowBySession(sessionID string) (subID, account, customerID, state string, err error) {
	if sessionID == "" {
		return "", "", "", "", ErrNotFound
	}
	err = s.db.QueryRow(
		`SELECT stripe_sub_id, account_number, customer_id, provision_state FROM stripe_subscriptions WHERE checkout_session_id=?`,
		sessionID).Scan(&subID, &account, &customerID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", ErrNotFound
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("controlplane: lookup subscription by session: %w", err)
	}
	return subID, account, customerID, state, nil
}

// HasDormantInGrace reports whether the account still owns a dormant subscription
// within its reactivation grace (dormant AND dormant_at >= now-retention). Gate for
// offering passkey recovery: nothing to recover once the grace has elapsed.
func (s *Store) HasDormantInGrace(account string, now time.Time, retention time.Duration) (bool, error) {
	if account == "" {
		return false, nil
	}
	cutoff := now.Add(-retention).UTC().Format(rfc)
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM stripe_subscriptions WHERE account_number=? AND provision_state='dormant' AND dormant_at IS NOT NULL AND dormant_at >= ?`,
		account, cutoff).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("controlplane: dormant-in-grace: %w", err)
	}
	return n > 0, nil
}

// FindDormantByCustomer resolves a Stripe customer id to its single dormant tenant
// still within the reactivation grace (provision_state='dormant' AND dormant_at >=
// now-retention). DISCOVERY ONLY: it merely surfaces the "recover with your passkey"
// offer on the success page and never changes state — the passkey assertion is the
// sole security gate. Fails CLOSED (ok=false) on an empty customer id or on 0 / >1
// matches, so an ambiguous or absent match never auto-links anything.
func (s *Store) FindDormantByCustomer(customerID string, now time.Time, retention time.Duration) (account, tenant string, ok bool, err error) {
	if customerID == "" {
		return "", "", false, nil
	}
	cutoff := now.Add(-retention).UTC().Format(rfc)
	rows, err := s.db.Query(`
		SELECT ss.account_number, a.tenant_id
		FROM stripe_subscriptions ss JOIN accounts a ON a.account_number = ss.account_number
		WHERE ss.customer_id=? AND ss.provision_state='dormant' AND ss.dormant_at IS NOT NULL AND ss.dormant_at >= ?`,
		customerID, cutoff)
	if err != nil {
		return "", "", false, fmt.Errorf("controlplane: find dormant by customer: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var a, t string
		if serr := rows.Scan(&a, &t); serr != nil {
			return "", "", false, serr
		}
		if n == 1 {
			account, tenant = a, t
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return "", "", false, rerr
	}
	if n != 1 { // 0 = no match; >1 = ambiguous → fail closed, offer nothing.
		return "", "", false, nil
	}
	return account, tenant, true, nil
}

// ReattachSubscriptionToDormant re-attaches a freshly-paid stub subscription to a
// dormant tenant after a passkey assertion has PROVEN ownership (the caller has
// already verified the assertion — this method only flips DB state; the off-repo
// poller does the k8s work). TRANSACTIONAL + guarded: it re-checks INSIDE the tx
// that the dormant tenant is still dormant AND in-grace, returning ErrGraceExpired
// otherwise, so a race with the erasure reaper can never resurrect a crypto-shredded
// tenant. Steps: (a) repoint the new sub → dormant account, provision_state='resume'
// (poller scales the preserved pod 0→1, DEK intact); (b) retire the old dormant sub
// → 'superseded'; (c) reactivate the dormant account; (d) retire the stub account +
// tear down any stub tenant still pointing at it. Idempotent: a repeat call once the
// new sub already resumes the dormant account is a no-op. Mirrors insertSubscriptionRow's
// atomic dormant-adoption pattern.
func (s *Store) ReattachSubscriptionToDormant(newSubID, dormantAccount, stubAccount string, now time.Time, retention time.Duration) error {
	if newSubID == "" || dormantAccount == "" {
		return errors.New("controlplane: reattach requires a new subscription + dormant account")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("controlplane: reattach: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Idempotency: the new sub already drives the dormant account (a replayed finish,
	// a double-tap) → nothing to do.
	var curAccount, curState string
	err = tx.QueryRow(`SELECT account_number, provision_state FROM stripe_subscriptions WHERE stripe_sub_id=?`, newSubID).
		Scan(&curAccount, &curState)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: unknown subscription %q", ErrNotFound, newSubID)
	}
	if err != nil {
		return fmt.Errorf("controlplane: reattach lookup: %w", err)
	}
	if curAccount == dormantAccount && curState == "resume" {
		return tx.Commit() // already reattached — no-op.
	}

	// Guard (fail closed): the dormant tenant MUST still be dormant AND within grace.
	cutoff := now.Add(-retention).UTC().Format(rfc)
	var dormantSub string
	err = tx.QueryRow(
		`SELECT stripe_sub_id FROM stripe_subscriptions WHERE account_number=? AND provision_state='dormant' AND dormant_at IS NOT NULL AND dormant_at >= ? ORDER BY dormant_at DESC LIMIT 1`,
		dormantAccount, cutoff).Scan(&dormantSub)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGraceExpired
	}
	if err != nil {
		return fmt.Errorf("controlplane: reattach dormant lookup: %w", err)
	}

	// (a) The paid subscription now drives the dormant tenant (poller scales 0→1).
	res, err := tx.Exec(
		`UPDATE stripe_subscriptions SET account_number=?, provision_state='resume' WHERE stripe_sub_id=?`,
		dormantAccount, newSubID)
	if err != nil {
		return fmt.Errorf("controlplane: reattach repoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: subscription %q", ErrNotFound, newSubID)
	}
	// (b) The old dormant subscription is retired (its tenant is now driven by newSubID).
	if _, err := tx.Exec(
		`UPDATE stripe_subscriptions SET provision_state='superseded', dormant_at=NULL WHERE stripe_sub_id=? AND provision_state='dormant'`,
		dormantSub); err != nil {
		return fmt.Errorf("controlplane: reattach supersede: %w", err)
	}
	// (c) The recovered account is active again. valid_until is a near-term placeholder
	// (one grace window) that the next invoice.paid on newSubID — now attached here —
	// refreshes to the real Stripe period end.
	validUntil := now.Add(retention).UTC().Format(rfc)
	if _, err := tx.Exec(
		`UPDATE accounts SET status='active', valid_until=? WHERE account_number=?`,
		validUntil, dormantAccount); err != nil {
		return fmt.Errorf("controlplane: reattach reactivate: %w", err)
	}
	// (d) Retire the stub: cancel its account and tear down any stub tenant still
	// pointing at it. In the common case the stub sub was newSubID (just repointed in
	// step a while still 'pending'), so its fresh empty pod is never provisioned; this
	// deprovision only fires if the poller had raced ahead and created it.
	if stubAccount != "" && stubAccount != dormantAccount {
		if _, err := tx.Exec(
			`UPDATE stripe_subscriptions SET provision_state='deprovision' WHERE account_number=? AND provision_state NOT IN ('gone','purged','superseded','deprovision')`,
			stubAccount); err != nil {
			return fmt.Errorf("controlplane: reattach retire stub subs: %w", err)
		}
		if _, err := tx.Exec(
			`UPDATE accounts SET status='canceled' WHERE account_number=?`,
			stubAccount); err != nil {
			return fmt.Errorf("controlplane: reattach cancel stub account: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) getAccountByEmail(email string) (Account, error) {
	// Emails are NOT unique identities (a new subscription always creates a new
	// account — WP-SEC1), so several accounts may share one address. Return the most
	// recent — an operator support convenience only, never an authorization decision.
	row := s.db.QueryRow(
		`SELECT account_number,email,tenant_id,status,valid_until,created_at FROM accounts WHERE email=? ORDER BY created_at DESC LIMIT 1`, email)
	return scanAccount(row)
}
