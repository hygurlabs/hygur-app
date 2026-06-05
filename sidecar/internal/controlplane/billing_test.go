package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProvisioner struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeProvisioner) Provision(_ context.Context, acc Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, acc.AccountNumber)
	return nil
}

func (f *fakeProvisioner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

const whSecret = "whsec_test_123"

// post signs `payload` like Stripe and sends it to the billing webhook.
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

// A finalized paid checkout provisions exactly once, even when the same event is
// delivered twice (Stripe retry / success-page reload) — no second account, no
// second pod.
func TestBilling_IdempotentProvision(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	prov := &fakeProvisioner{}
	b := NewBilling(store, whSecret, prov)
	b.now = func() time.Time { return now }

	for i := 0; i < 3; i++ { // deliver the SAME event 3 times
		if rec := postWebhook(t, b, paidEvent("sub_1", "a@b.com"), now); rec.Code != http.StatusOK {
			t.Fatalf("delivery %d: status %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	if n := prov.count(); n != 1 {
		t.Errorf("provisioned %d times, want exactly 1", n)
	}
	// Exactly one account exists for that email.
	if _, err := store.getAccountByEmail("a@b.com"); err != nil {
		t.Errorf("expected one account for a@b.com: %v", err)
	}
}

// A non-finalized payment (unpaid) provisions nothing — the pod is loaded only
// after the payment is confirmed.
func TestBilling_IgnoresUnpaid(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	prov := &fakeProvisioner{}
	b := NewBilling(store, whSecret, prov)
	b.now = func() time.Time { return now }

	unpaid := strings.Replace(paidEvent("sub_x", "u@b.com"), `"payment_status":"paid"`, `"payment_status":"unpaid"`, 1)
	rec := postWebhook(t, b, unpaid, now)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if n := prov.count(); n != 0 {
		t.Errorf("unpaid provisioned %d times, want 0", n)
	}
	if _, err := store.getAccountByEmail("u@b.com"); err == nil {
		t.Error("unpaid should not create an account")
	}
}

// A bad signature is rejected before any side effect.
func TestBilling_RejectsBadSignature(t *testing.T) {
	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	prov := &fakeProvisioner{}
	b := NewBilling(store, whSecret, prov)
	b.now = func() time.Time { return now }

	payload := paidEvent("sub_2", "c@b.com")
	r := httptest.NewRequest(http.MethodPost, "/stripe/webhook", strings.NewReader(payload))
	r.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=deadbeef", now.Unix()))
	rec := httptest.NewRecorder()
	b.Routes().ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad signature status = %d, want 400", rec.Code)
	}
	if prov.count() != 0 {
		t.Error("bad signature must not provision")
	}
}
