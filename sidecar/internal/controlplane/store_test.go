package controlplane

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

// TestStore_MigrateDropsEmailUnique verifies the WP-SEC1 migration: a legacy DB whose
// accounts table still carries the old UNIQUE(email) is rebuilt on Open so duplicate
// emails are allowed (a new subscription = a new account), while existing rows and
// child foreign keys (a device) survive intact. Idempotent on re-open.
func TestStore_MigrateDropsEmailUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.db")
	const key = "test-key-0123456789"
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_pragma_key=%s&_pragma_cipher_page_size=4096", path, key)

	// Seed a legacy DB: accounts WITH the old UNIQUE(email) + a child device row.
	raw, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE accounts (
  account_number TEXT PRIMARY KEY,
  email          TEXT NOT NULL UNIQUE,
  tenant_id      TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'trialing',
  valid_until    TEXT,
  created_at     TEXT NOT NULL
);
CREATE TABLE devices (
  device_id      TEXT PRIMARY KEY,
  account_number TEXT NOT NULL REFERENCES accounts(account_number),
  label          TEXT NOT NULL DEFAULT '',
  jti            TEXT NOT NULL,
  refresh_hash   TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  revoked_at     TEXT
);
INSERT INTO accounts(account_number,email,tenant_id,status,valid_until,created_at)
  VALUES('000001','x@y.com','brave-azure-harbor','active',NULL,'2024-01-01T00:00:00Z');
INSERT INTO devices(device_id,account_number,label,jti,refresh_hash,created_at)
  VALUES('dev1','000001','web','jti1','hash1','2024-01-01T00:00:00Z');`); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	_ = raw.Close()

	// Open through the store → migrate() rebuilds accounts without the UNIQUE.
	s, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open (migrate): %v", err)
	}
	// Legacy account + its device survived the rebuild (rows + FK preserved).
	if _, err := s.GetAccount("000001"); err != nil {
		t.Fatalf("legacy account lost after migration: %v", err)
	}
	if devs, err := s.ListDevices("000001"); err != nil || len(devs) != 1 {
		t.Fatalf("device lost after migration: n=%d err=%v", len(devs), err)
	}
	// The UNIQUE is gone: a second account with the SAME email now succeeds.
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, err := s.CreateAccount(now, "x@y.com", "active", nil); err != nil {
		t.Fatalf("duplicate email still rejected after migration: %v", err)
	}
	_ = s.Close()

	// Idempotent: re-opening runs migrate() again with no error / no data loss.
	s2, err := Open(path, key)
	if err != nil {
		t.Fatalf("re-Open (idempotent migrate): %v", err)
	}
	defer s2.Close()
	if _, err := s2.GetAccount("000001"); err != nil {
		t.Fatalf("account lost on second migrate: %v", err)
	}
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
	// Default tenant id is now a friendly adjective-color-noun slug.
	if parts := strings.Split(acc.TenantID, "-"); len(parts) != 3 || acc.TenantID != strings.ToLower(acc.TenantID) {
		t.Errorf("tenant id = %q, want a lowercase 3-word slug", acc.TenantID)
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

	// Refresh rotates the access jti but KEEPS the refresh token stable, so a
	// lost/raced refresh response can't lock the client out.
	dev2, refresh2, err := s.Refresh(now, refresh)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if dev2.JTI == dev.JTI {
		t.Error("refresh should rotate the access jti")
	}
	if refresh2 != refresh {
		t.Error("refresh token must stay stable (not rotate)")
	}
	// The same refresh token keeps working on repeated calls (the whole point).
	if _, again, err := s.Refresh(now, refresh); err != nil || again != refresh {
		t.Errorf("stable refresh should keep working: token=%q err=%v", again, err)
	}

	// Revoke → the refresh stops working.
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

// TestCreateEnrollCode_InvalidatesPrior verifies that minting a new code for the
// same account+label invalidates the prior live code (only the latest is
// redeemable) — caps concurrent codes + fixes success-page refresh-spam. Codes
// for a different label are left untouched.
func TestCreateEnrollCode_InvalidatesPrior(t *testing.T) {
	s := testStore(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	acc, _ := s.CreateAccount(now, "u@x.com", "active", nil)

	first, err := s.CreateEnrollCode(now, acc.AccountNumber, "web", 30*time.Minute)
	if err != nil {
		t.Fatalf("first CreateEnrollCode: %v", err)
	}
	// A code for a different label must survive the next mint.
	other, _ := s.CreateEnrollCode(now, acc.AccountNumber, "mobile", 30*time.Minute)

	second, err := s.CreateEnrollCode(now, acc.AccountNumber, "web", 30*time.Minute)
	if err != nil {
		t.Fatalf("second CreateEnrollCode: %v", err)
	}

	// The first (same-label) code is now dead.
	if _, _, err := s.RedeemEnrollCode(now, first); !errors.Is(err, ErrCodeInvalid) {
		t.Errorf("prior same-label code: want ErrCodeInvalid, got %v", err)
	}
	// The latest code still redeems (single-use + atomic preserved).
	if _, _, err := s.RedeemEnrollCode(now, second); err != nil {
		t.Errorf("latest code should redeem: %v", err)
	}
	// The different-label code is untouched.
	if _, _, err := s.RedeemEnrollCode(now, other); err != nil {
		t.Errorf("different-label code should still redeem: %v", err)
	}
}
