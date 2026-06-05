package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
