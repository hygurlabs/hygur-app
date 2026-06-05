package controlplane

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "admin.db"), "test-key-0123456789")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAccountLifecycle(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	acc, err := s.CreateAccount(now, "Owner@Example.com", "trialing", nil)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if acc.Email != "owner@example.com" {
		t.Errorf("email not normalised: %q", acc.Email)
	}
	if acc.TenantID != "instance-personal-"+acc.AccountNumber {
		t.Errorf("tenant id = %q, want instance-personal-%s", acc.TenantID, acc.AccountNumber)
	}
	if !acc.IsActive(now) {
		t.Error("trialing + no expiry should be active")
	}

	// Round-trip.
	got, err := s.GetAccount(acc.AccountNumber)
	if err != nil || got.Email != acc.Email {
		t.Fatalf("GetAccount: %v / %+v", err, got)
	}

	// Subscription expiry gates access.
	past := now.Add(-time.Hour)
	if err := s.SetSubscription(acc.AccountNumber, "active", &past); err != nil {
		t.Fatalf("SetSubscription: %v", err)
	}
	got, _ = s.GetAccount(acc.AccountNumber)
	if got.IsActive(now) {
		t.Error("expired validity should be inactive")
	}
	future := now.Add(720 * time.Hour)
	_ = s.SetSubscription(acc.AccountNumber, "active", &future)
	got, _ = s.GetAccount(acc.AccountNumber)
	if !got.IsActive(now) {
		t.Error("active + future validity should be active")
	}

	if _, err := s.GetAccount("999999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing account: want ErrNotFound, got %v", err)
	}
}

func TestEnrollAndRefresh(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	acc, _ := s.CreateAccount(now, "u@x.com", "active", nil)

	// Mint + redeem an enrollment code → a device + refresh token.
	code, err := s.CreateEnrollCode(now, acc.AccountNumber, "web", 10*time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollCode: %v", err)
	}
	dev, refresh, err := s.RedeemEnrollCode(now, code)
	if err != nil {
		t.Fatalf("RedeemEnrollCode: %v", err)
	}
	if dev.AccountNumber != acc.AccountNumber || dev.JTI == "" || refresh == "" {
		t.Fatalf("bad device/refresh: %+v / %q", dev, refresh)
	}

	// One-time: a second redeem of the same code fails.
	if _, _, err := s.RedeemEnrollCode(now, code); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("reused code: want ErrCodeInvalid, got %v", err)
	}

	// Expired code is rejected.
	expCode, _ := s.CreateEnrollCode(now, acc.AccountNumber, "x", -time.Minute)
	if _, _, err := s.RedeemEnrollCode(now, expCode); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("expired code: want ErrCodeInvalid, got %v", err)
	}

	// Refresh rotates the jti + the refresh token; the old refresh stops working.
	dev2, refresh2, err := s.Refresh(now, refresh)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if dev2.JTI == dev.JTI {
		t.Error("refresh should rotate the jti")
	}
	if refresh2 == refresh {
		t.Error("refresh should rotate the refresh token")
	}
	if _, _, err := s.Refresh(now, refresh); !errors.Is(err, ErrRefreshInvalid) {
		t.Errorf("old refresh after rotation: want ErrRefreshInvalid, got %v", err)
	}

	// Revoke → the (rotated) refresh stops working too.
	if err := s.RevokeDevice(now, dev.DeviceID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if _, _, err := s.Refresh(now, refresh2); !errors.Is(err, ErrRefreshInvalid) {
		t.Errorf("refresh after revoke: want ErrRefreshInvalid, got %v", err)
	}

	devices, err := s.ListDevices(acc.AccountNumber)
	if err != nil || len(devices) != 1 {
		t.Fatalf("ListDevices: %v / %d", err, len(devices))
	}
	if devices[0].RevokedAt == nil {
		t.Error("device should show revoked")
	}
}
