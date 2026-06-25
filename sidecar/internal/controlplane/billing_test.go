package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const whSecret = "whsec_test_123"

// postWebhook signs `payload` like Stripe and posts it to the billing webhook.
func postWebhook(t *testing.T, b *Billing, payload string, ts time.Time) *httptest.ResponseRecorder {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(whSecret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts.Unix(), payload)))
	sig := hex.EncodeToString(mac.Sum(nil))
	r := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	r.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", ts.Unix(), sig))
	rec := httptest.NewRecorder()
	b.Routes().ServeHTTP(rec, r)
	return rec
}

func paidEvent(sub, email string) string {
	return `{"type":"checkout.session.completed","data":{"object":{` +
		`"id":"cs_test_1","mode":"subscription","payment_status":"paid",` +
		`"customer":"cus_1","subscription":"` + sub + `","customer_details":{"email":"` + email + `"}}}}`
}

// A finalized paid checkout records ONE account + ONE 'pending' provisioning row,
// even when the same event is delivered repeatedly (Stripe retry / reload).
func TestBilling_IdempotentPending(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if rec := postWebhook(t, b, paidEvent("sub_1", "a@b.com"), now); rec.Code != http.StatusOK {
			t.Fatalf("delivery %d: status %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	pend, err := store.ListProvisions("pending")
	if err != nil {
		t.Fatalf("ListProvisions: %v", err)
	}
	if len(pend) != 1 {
		t.Errorf("pending provisions = %d, want exactly 1", len(pend))
	}
	if _, err := store.getAccountByEmail("a@b.com"); err != nil {
		t.Errorf("expected one account for a@b.com: %v", err)
	}
}

// A non-finalized payment provisions nothing.
func TestBilling_IgnoresUnpaid(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	unpaid := strings.Replace(paidEvent("sub_x", "u@b.com"), `"payment_status":"paid"`, `"payment_status":"unpaid"`, 1)
	if rec := postWebhook(t, b, unpaid, now); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if pend, _ := store.ListProvisions("pending"); len(pend) != 0 {
		t.Errorf("unpaid queued %d provisions, want 0", len(pend))
	}
	if _, err := store.getAccountByEmail("u@b.com"); err == nil {
		t.Error("unpaid should not create an account")
	}
}

// Lifecycle: past_due / canceled suspend the account (IsActive false), invoice.paid
// re-activates, and a deletion queues the pod for reaping (state 'deprovision').
func TestBilling_LifecycleSuspends(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	if rec := postWebhook(t, b, paidEvent("sub_1", "a@b.com"), now); rec.Code != http.StatusOK {
		t.Fatalf("paid: %d", rec.Code)
	}
	acc, err := store.getAccountByEmail("a@b.com")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if !acc.IsActive(now) {
		t.Fatal("active after payment")
	}

	checkActive := func(payload, label string, want bool) {
		if rec := postWebhook(t, b, payload, now); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", label, rec.Code, rec.Body.String())
		}
		a, _ := store.GetAccount(acc.AccountNumber)
		if a.IsActive(now) != want {
			t.Errorf("%s: IsActive=%v, want %v", label, a.IsActive(now), want)
		}
	}
	checkActive(`{"type":"invoice.payment_failed","data":{"object":{"subscription":"sub_1"}}}`, "payment_failed", false)
	checkActive(`{"type":"invoice.paid","data":{"object":{"subscription":"sub_1"}}}`, "invoice.paid", true)
	checkActive(`{"type":"customer.subscription.deleted","data":{"object":{"id":"sub_1"}}}`, "deleted", false)

	// Deletion queued the pod for reaping.
	rep, _ := store.ListProvisions("deprovision")
	if len(rep) != 1 {
		t.Errorf("deprovision queue = %d, want 1", len(rep))
	}
}

// Dunning lifecycle: once a pod is live ('ready'), a failed payment queues it for
// scale-to-0 ('suspend'); a recovered payment queues it back ('resume'). Neither
// touches a not-yet-provisioned ('pending') tenant.
func TestBilling_SuspendResume(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	if rec := postWebhook(t, b, paidEvent("sub_1", "a@b.com"), now); rec.Code != http.StatusOK {
		t.Fatalf("paid: %d", rec.Code)
	}
	state := func() string {
		_, st, err := store.SubscriptionBySession("cs_test_1")
		if err != nil {
			t.Fatalf("state lookup: %v", err)
		}
		return st
	}

	// payment_failed before provisioning ('pending') must NOT queue a suspend.
	failed := `{"type":"invoice.payment_failed","data":{"object":{"subscription":"sub_1"}}}`
	if rec := postWebhook(t, b, failed, now); rec.Code != http.StatusOK {
		t.Fatalf("payment_failed(pending): %d", rec.Code)
	}
	if got := state(); got != "pending" {
		t.Fatalf("after failed-while-pending: state=%q, want pending", got)
	}

	// Poller provisions the pod → 'ready'. Now a failed payment queues 'suspend'.
	if err := store.SetProvisionState("sub_1", "ready"); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	if rec := postWebhook(t, b, failed, now); rec.Code != http.StatusOK {
		t.Fatalf("payment_failed(ready): %d", rec.Code)
	}
	if got := state(); got != "suspend" {
		t.Fatalf("after failed-while-ready: state=%q, want suspend", got)
	}

	// Poller scaled it to 0 → 'suspended'. A successful payment queues 'resume'.
	if err := store.SetProvisionState("sub_1", "suspended"); err != nil {
		t.Fatalf("mark suspended: %v", err)
	}
	paid := `{"type":"invoice.paid","data":{"object":{"subscription":"sub_1"}}}`
	if rec := postWebhook(t, b, paid, now); rec.Code != http.StatusOK {
		t.Fatalf("invoice.paid: %d", rec.Code)
	}
	if got := state(); got != "resume" {
		t.Fatalf("after paid-while-suspended: state=%q, want resume", got)
	}
}

// A reaped ('gone') tenant becomes purgeable only once its retention window has
// elapsed; a still-live or just-reaped tenant is never returned.
func TestStore_Purgeable(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	if rec := postWebhook(t, b, paidEvent("sub_1", "a@b.com"), now); rec.Code != http.StatusOK {
		t.Fatalf("paid: %d", rec.Code)
	}
	// Not reaped yet → never purgeable.
	if rows, _ := store.ListPurgeable(time.Now(), 0); len(rows) != 0 {
		t.Fatalf("pending tenant purgeable = %d, want 0", len(rows))
	}
	// Reap it (stamps reaped_at ≈ now-real).
	if err := store.SetProvisionState("sub_1", "gone"); err != nil {
		t.Fatalf("mark gone: %v", err)
	}
	real := time.Now()
	// 30-day window not elapsed → not purgeable.
	if rows, _ := store.ListPurgeable(real, 30*24*time.Hour); len(rows) != 0 {
		t.Errorf("just-reaped tenant purgeable at 30d = %d, want 0", len(rows))
	}
	// Looking from far enough ahead with a short window → purgeable.
	if rows, _ := store.ListPurgeable(real.Add(time.Hour), 30*time.Minute); len(rows) != 1 {
		t.Errorf("reaped tenant past window = %d, want 1", len(rows))
	}
}

// A bad signature is rejected before any side effect.
func TestBilling_RejectsBadSignature(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	b := NewBilling(store, whSecret)
	b.now = func() time.Time { return now }

	payload := paidEvent("sub_2", "c@b.com")
	r := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	r.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=deadbeef", now.Unix()))
	rec := httptest.NewRecorder()
	b.Routes().ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad signature status = %d, want 400", rec.Code)
	}
	if pend, _ := store.ListProvisions("pending"); len(pend) != 0 {
		t.Error("bad signature must not queue provisioning")
	}
}

// TestSuccessPage_Copy guards the §C enrollment copy: the "not ready" page tells
// the user to keep the tab open, states the TRUE security story (per-space DB +
// its own encryption key), self-refreshes, and never fabricates browser-side
// crypto (no "session key" / "derive" theater). The "ready" page shows the code
// once and does NOT auto-refresh (so reloading doesn't silently re-mint).
func TestSuccessPage_Copy(t *testing.T) {
	render := func(data map[string]any) string {
		var sb strings.Builder
		if err := successPage.Execute(&sb, data); err != nil {
			t.Fatalf("render: %v", err)
		}
		return sb.String()
	}

	notReady := render(map[string]any{"Ready": false})
	for _, want := range []string{"Keep this tab open", "encryption key", "don't send it by email", `http-equiv="refresh"`} {
		if !strings.Contains(notReady, want) {
			t.Errorf("not-ready page missing %q", want)
		}
	}
	// Integrity guard: no fabricated browser-side key derivation.
	for _, banned := range []string{"session key", "derive", "derived"} {
		if strings.Contains(strings.ToLower(notReady), banned) {
			t.Errorf("not-ready page must not fabricate crypto (found %q)", banned)
		}
	}

	ready := render(map[string]any{
		"Ready": true, "Code": "ABC-123", "Slug": "brave-azure-harbor",
		"URL":      "cloud.hygur.ai/brave-azure-harbor",
		"DeepLink": template.URL("https://cloud.hygur.ai/brave-azure-harbor?code=ABC-123"),
		"QR":       template.URL("data:image/png;base64,AAA="),
	})
	for _, want := range []string{
		"ABC-123", "brave-azure-harbor", "Open your space",
		"cloud.hygur.ai/brave-azure-harbor", "data:image/png;base64,AAA=", "add a passkey",
	} {
		if !strings.Contains(ready, want) {
			t.Errorf("ready page missing %q", want)
		}
	}
	if strings.Contains(ready, `http-equiv="refresh"`) {
		t.Error("ready page must NOT auto-refresh (would re-mint the code)")
	}
}

func TestGenerateInstanceName(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		n := GenerateInstanceName()
		// adjective-color-noun, lowercase, DNS/namespace-safe.
		if parts := strings.Split(n, "-"); len(parts) != 3 {
			t.Fatalf("name %q must be 3 hyphen-joined words", n)
		}
		if n != strings.ToLower(n) {
			t.Fatalf("name %q must be lowercase", n)
		}
		seen[n] = true
	}
	if len(seen) < 10 {
		t.Errorf("generator looks low-entropy: only %d distinct of 50", len(seen))
	}
}
