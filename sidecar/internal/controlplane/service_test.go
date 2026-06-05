package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/auth"
)

// TestService_EnrollRefresh exercises the full device flow through HTTP: seed an
// account + code, enroll → a verifiable tenant-scoped JWT + refresh, then refresh
// → a fresh (rotated) JWT. Also: used code rejected, inactive account blocked.
func TestService_EnrollRefresh(t *testing.T) {
	pubPEM, privPEM, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pub, _ := auth.ParseEd25519PublicKeyPEM(pubPEM)

	store := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	acc, _ := store.CreateAccount(now, "u@x.com", "active", nil)
	code, _ := store.CreateEnrollCode(now, acc.AccountNumber, "web", 10*time.Minute)

	svc, err := NewService(store, privPEM, "hygur.ai", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.now = func() time.Time { return now }
	srv := httptest.NewServer(svc.Routes())
	defer srv.Close()

	post := func(path, body string) (*http.Response, map[string]any) {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		return resp, m
	}

	// Enroll → tokens + endpoint.
	resp, m := post("/enroll", `{"code":"`+code+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll status = %d (%v)", resp.StatusCode, m)
	}
	access, _ := m["access_token"].(string)
	refresh, _ := m["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing tokens: %v", m)
	}
	if got := m["endpoint"]; got != "https://"+acc.TenantID+".hygur.ai" {
		t.Errorf("endpoint = %v, want https://%s.hygur.ai", got, acc.TenantID)
	}

	// The access token is a valid tenant-scoped device JWT.
	claims, err := auth.VerifyDeviceToken(pub, access, now)
	if err != nil {
		t.Fatalf("VerifyDeviceToken: %v", err)
	}
	if claims.Acc != acc.TenantID {
		t.Errorf("Acc = %q, want %q (tenant-pin target)", claims.Acc, acc.TenantID)
	}

	// Reused code → rejected.
	if resp, _ := post("/enroll", `{"code":"`+code+`"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("reused code status = %d, want 401", resp.StatusCode)
	}

	// Refresh → fresh access (rotated jti) + new refresh.
	resp, m = post("/token/refresh", `{"refresh_token":"`+refresh+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d (%v)", resp.StatusCode, m)
	}
	access2, _ := m["access_token"].(string)
	claims2, err := auth.VerifyDeviceToken(pub, access2, now)
	if err != nil {
		t.Fatalf("verify refreshed: %v", err)
	}
	if claims2.Jti == claims.Jti {
		t.Error("refresh should rotate the jti")
	}

	// Inactive account → enroll blocked (403).
	past := now.Add(-time.Hour)
	_ = store.SetSubscription(acc.AccountNumber, "past_due", &past)
	code2, _ := store.CreateEnrollCode(now, acc.AccountNumber, "web2", 10*time.Minute)
	if resp, _ := post("/enroll", `{"code":"`+code2+`"}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("inactive enroll status = %d, want 403", resp.StatusCode)
	}
}
