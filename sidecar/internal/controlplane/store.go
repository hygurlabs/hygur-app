// Package controlplane is the Hygur Cloud control plane (operator-owned, NOT
// per-tenant): accounts, subscriptions, devices, and the enrollment/refresh
// flow that replaces the manual `issue-token`. This file is the admin store —
// a SQLCipher-encrypted SQLite DB with its own schema (distinct from a tenant's
// knowledge-base store). C8.1a: data layer only; the HTTP service + automated
// provisioning build on top.
package controlplane

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/mutecomm/go-sqlcipher/v4" // SQLCipher driver, registered as "sqlite3"
)

var (
	// ErrNotFound is returned when an account/device/code doesn't exist.
	ErrNotFound = errors.New("not found")
	// ErrCodeInvalid covers an enrollment code that's unknown, expired, or used.
	ErrCodeInvalid = errors.New("enrollment code invalid, expired, or already used")
	// ErrRefreshInvalid covers an unknown/revoked refresh token.
	ErrRefreshInvalid = errors.New("refresh token invalid or revoked")
)

// Account is one paying customer. tenant_id is the pod namespace suffix
// (instance-personal-<account_number>). valid_until carries the subscription
// validity (the "validate_end_date").
type Account struct {
	AccountNumber string
	Email         string
	TenantID      string
	Status        string // trialing | active | past_due | canceled
	ValidUntil    *time.Time
	CreatedAt     time.Time
}

// Device is an enrolled client (web/desktop/mobile) of an account.
type Device struct {
	DeviceID      string
	AccountNumber string
	Label         string
	JTI           string
	CreatedAt     time.Time
	RevokedAt     *time.Time
}

// Store is the control-plane admin database.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the admin DB at path, SQLCipher-encrypted when key
// is non-empty. Mirrors the tenant store's keying so the control plane is
// encrypted at rest the same way.
func Open(path, key string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on", path)
	if key != "" {
		dsn += fmt.Sprintf("&_pragma_key=%s&_pragma_cipher_page_size=4096", url.QueryEscape(key))
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("controlplane: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("controlplane: ping: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying DB.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS accounts (
  account_number TEXT PRIMARY KEY,
  email          TEXT NOT NULL UNIQUE,
  tenant_id      TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'trialing',
  valid_until    TEXT,
  created_at     TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  device_id      TEXT PRIMARY KEY,
  account_number TEXT NOT NULL REFERENCES accounts(account_number),
  label          TEXT NOT NULL DEFAULT '',
  jti            TEXT NOT NULL,
  refresh_hash   TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  revoked_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_devices_account ON devices(account_number);
CREATE TABLE IF NOT EXISTS enroll_codes (
  code_hash      TEXT PRIMARY KEY,
  account_number TEXT NOT NULL REFERENCES accounts(account_number),
  label          TEXT NOT NULL DEFAULT '',
  expires_at     TEXT NOT NULL,
  used_at        TEXT
);
-- Stripe subscription → account mapping. The primary key on stripe_sub_id and
-- the provisioned_at claim make billing webhooks idempotent: a retried / replayed
-- / reloaded paid event maps to the SAME account and provisions the tenant pod at
-- most once (no provisioning loop).
CREATE TABLE IF NOT EXISTS stripe_subscriptions (
  stripe_sub_id       TEXT PRIMARY KEY,
  account_number      TEXT NOT NULL REFERENCES accounts(account_number),
  customer_id         TEXT NOT NULL DEFAULT '',
  checkout_session_id TEXT NOT NULL DEFAULT '',
  provision_state     TEXT NOT NULL DEFAULT 'pending', -- pending|ready|deprovision|gone
  provisioned_at      TEXT,
  created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stripe_sub_session ON stripe_subscriptions(checkout_session_id);
-- Passkeys (WebAuthn): one credential row per registered authenticator, stored as
-- an opaque JSON blob (the go-webauthn Credential) so the store stays decoupled
-- from the ceremony types. Ceremony SessionData (challenge etc.) is parked between
-- begin/finish under a one-time id with a short TTL.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
  credential_id   TEXT PRIMARY KEY,
  account_number  TEXT NOT NULL REFERENCES accounts(account_number),
  cred_json       BLOB NOT NULL,
  name            TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_webauthn_cred_account ON webauthn_credentials(account_number);
CREATE TABLE IF NOT EXISTS webauthn_sessions (
  id              TEXT PRIMARY KEY,
  account_number  TEXT NOT NULL DEFAULT '',
  purpose         TEXT NOT NULL,
  data            BLOB NOT NULL,
  expires_at      TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("controlplane: migrate: %w", err)
	}
	// Idempotent column add: DBs created before provision_state existed (CREATE
	// TABLE IF NOT EXISTS won't alter an existing table). Ignore "duplicate column"
	// on fresh DBs that already have it. Then index it.
	if _, err := s.db.Exec(`ALTER TABLE stripe_subscriptions ADD COLUMN provision_state TEXT NOT NULL DEFAULT 'pending'`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("controlplane: migrate provision_state: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_stripe_sub_state ON stripe_subscriptions(provision_state)`); err != nil {
		return fmt.Errorf("controlplane: migrate index: %w", err)
	}
	return nil
}

const rfc = time.RFC3339

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(rfc)
}

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	if t, err := time.Parse(rfc, s.String); err == nil {
		return &t
	}
	return nil
}

// CreateAccount provisions a new account with a unique 6-digit account number
// and the derived tenant id (instance-personal-<number>). validUntil may be nil
// (set later by the billing webhook).
func (s *Store) CreateAccount(now time.Time, email string, status string, validUntil *time.Time) (Account, error) {
	return s.createAccount(now, email, status, "", validUntil)
}

// CreateAccountWithTenant provisions an account pinned to an explicit tenant id —
// the operator's own instance (e.g. "home") or, later, a generated slug — rather
// than the auto instance-personal-<number>. The tenant id must be free.
func (s *Store) CreateAccountWithTenant(now time.Time, email, status, tenantID string, validUntil *time.Time) (Account, error) {
	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	if tenantID == "" {
		return Account{}, errors.New("tenant id required")
	}
	if _, err := s.getAccountByTenantID(tenantID); err == nil {
		return Account{}, fmt.Errorf("controlplane: tenant id %q already in use", tenantID)
	}
	return s.createAccount(now, email, status, tenantID, validUntil)
}

// createAccount allocates a unique account number and inserts the row. tenantID
// pins the tenant id; empty derives the default instance-personal-<number>.
func (s *Store) createAccount(now time.Time, email, status, tenantID string, validUntil *time.Time) (Account, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return Account{}, errors.New("email required")
	}
	if status == "" {
		status = "trialing"
	}
	// Retry on the (unlikely) account-number collision.
	for attempt := 0; attempt < 8; attempt++ {
		num, err := randomAccountNumber()
		if err != nil {
			return Account{}, err
		}
		tid := tenantID
		if tid == "" {
			tid = "instance-personal-" + num
		}
		acc := Account{
			AccountNumber: num,
			Email:         email,
			TenantID:      tid,
			Status:        status,
			ValidUntil:    validUntil,
			CreatedAt:     now.UTC(),
		}
		_, err = s.db.Exec(
			`INSERT INTO accounts(account_number,email,tenant_id,status,valid_until,created_at) VALUES(?,?,?,?,?,?)`,
			acc.AccountNumber, acc.Email, acc.TenantID, acc.Status, nullTime(acc.ValidUntil), acc.CreatedAt.Format(rfc),
		)
		if err == nil {
			return acc, nil
		}
		if strings.Contains(err.Error(), "account_number") {
			continue // PK collision — try another number
		}
		return Account{}, fmt.Errorf("controlplane: create account: %w", err)
	}
	return Account{}, errors.New("controlplane: could not allocate account number")
}

// GetAccount returns the account by its number.
func (s *Store) GetAccount(accountNumber string) (Account, error) {
	row := s.db.QueryRow(
		`SELECT account_number,email,tenant_id,status,valid_until,created_at FROM accounts WHERE account_number=?`,
		accountNumber)
	return scanAccount(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row rowScanner) (Account, error) {
	var a Account
	var validUntil, createdAt sql.NullString
	if err := row.Scan(&a.AccountNumber, &a.Email, &a.TenantID, &a.Status, &validUntil, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, err
	}
	a.ValidUntil = parseTime(validUntil)
	if t := parseTime(createdAt); t != nil {
		a.CreatedAt = *t
	}
	return a, nil
}

// SetSubscription updates an account's status + validity (billing webhook).
func (s *Store) SetSubscription(accountNumber, status string, validUntil *time.Time) error {
	res, err := s.db.Exec(`UPDATE accounts SET status=?, valid_until=? WHERE account_number=?`,
		status, nullTime(validUntil), accountNumber)
	if err != nil {
		return fmt.Errorf("controlplane: set subscription: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// IsActive reports whether the account may use its tenant right now: an active
// or trialing status AND (no expiry OR expiry in the future).
func (a Account) IsActive(now time.Time) bool {
	if a.Status != "active" && a.Status != "trialing" {
		return false
	}
	return a.ValidUntil == nil || a.ValidUntil.After(now)
}

// CreateEnrollCode mints a one-time enrollment code for an account+device label,
// valid for ttl. Returns the PLAINTEXT code (shown once); only its hash is stored.
func (s *Store) CreateEnrollCode(now time.Time, accountNumber, label string, ttl time.Duration) (string, error) {
	if _, err := s.GetAccount(accountNumber); err != nil {
		return "", err
	}
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO enroll_codes(code_hash,account_number,label,expires_at,used_at) VALUES(?,?,?,?,NULL)`,
		hashToken(code), accountNumber, label, now.Add(ttl).UTC().Format(rfc))
	if err != nil {
		return "", fmt.Errorf("controlplane: create enroll code: %w", err)
	}
	return code, nil
}

// RedeemEnrollCode exchanges a one-time code for a new device: it validates the
// code (exists, not expired, not used), marks it used, creates the device, and
// returns the device plus the PLAINTEXT refresh token (shown once). The caller
// mints the access JWT from device.JTI / device.AccountNumber. Atomic.
func (s *Store) RedeemEnrollCode(now time.Time, code string) (Device, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Device{}, "", err
	}
	defer func() { _ = tx.Rollback() }()

	var accountNumber, label, expiresAt string
	var usedAt sql.NullString
	err = tx.QueryRow(`SELECT account_number,label,expires_at,used_at FROM enroll_codes WHERE code_hash=?`,
		hashToken(code)).Scan(&accountNumber, &label, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, "", ErrCodeInvalid
	}
	if err != nil {
		return Device{}, "", err
	}
	if usedAt.Valid {
		return Device{}, "", ErrCodeInvalid
	}
	if exp, perr := time.Parse(rfc, expiresAt); perr != nil || !exp.After(now) {
		return Device{}, "", ErrCodeInvalid
	}

	if _, err := tx.Exec(`UPDATE enroll_codes SET used_at=? WHERE code_hash=?`,
		now.UTC().Format(rfc), hashToken(code)); err != nil {
		return Device{}, "", err
	}

	dev := Device{
		DeviceID:      newID(),
		AccountNumber: accountNumber,
		Label:         label,
		JTI:           newID(),
		CreatedAt:     now.UTC(),
	}
	refresh, err := randomToken()
	if err != nil {
		return Device{}, "", err
	}
	if _, err := tx.Exec(
		`INSERT INTO devices(device_id,account_number,label,jti,refresh_hash,created_at,revoked_at) VALUES(?,?,?,?,?,?,NULL)`,
		dev.DeviceID, dev.AccountNumber, dev.Label, dev.JTI, hashToken(refresh), dev.CreatedAt.Format(rfc),
	); err != nil {
		return Device{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, "", err
	}
	return dev, refresh, nil
}

// Refresh validates a refresh token, rotates the device's access jti (and the
// refresh token), and returns the device (with the new jti) plus the new
// PLAINTEXT refresh token. Revoked/unknown refresh tokens are rejected.
func (s *Store) Refresh(now time.Time, refreshToken string) (Device, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Device{}, "", err
	}
	defer func() { _ = tx.Rollback() }()

	var dev Device
	var revokedAt sql.NullString
	err = tx.QueryRow(
		`SELECT device_id,account_number,label,jti,created_at,revoked_at FROM devices WHERE refresh_hash=?`,
		hashToken(refreshToken)).Scan(&dev.DeviceID, &dev.AccountNumber, &dev.Label, &dev.JTI, new(string), &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, "", ErrRefreshInvalid
	}
	if err != nil {
		return Device{}, "", err
	}
	if revokedAt.Valid {
		return Device{}, "", ErrRefreshInvalid
	}

	dev.JTI = newID()
	newRefresh, err := randomToken()
	if err != nil {
		return Device{}, "", err
	}
	if _, err := tx.Exec(`UPDATE devices SET jti=?, refresh_hash=? WHERE device_id=?`,
		dev.JTI, hashToken(newRefresh), dev.DeviceID); err != nil {
		return Device{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, "", err
	}
	return dev, newRefresh, nil
}

// RevokeDevice marks a device revoked (its refresh token stops working; the
// access JWT stays valid until expiry — propagate the jti to the tenant's
// auth.revoked_jtis for immediate cutoff).
func (s *Store) RevokeDevice(now time.Time, deviceID string) error {
	res, err := s.db.Exec(`UPDATE devices SET revoked_at=? WHERE device_id=? AND revoked_at IS NULL`,
		now.UTC().Format(rfc), deviceID)
	if err != nil {
		return fmt.Errorf("controlplane: revoke device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDevices returns an account's devices (newest first).
func (s *Store) ListDevices(accountNumber string) ([]Device, error) {
	rows, err := s.db.Query(
		`SELECT device_id,account_number,label,jti,created_at,revoked_at FROM devices WHERE account_number=? ORDER BY created_at DESC`,
		accountNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var createdAt string
		var revokedAt sql.NullString
		if err := rows.Scan(&d.DeviceID, &d.AccountNumber, &d.Label, &d.JTI, &createdAt, &revokedAt); err != nil {
			return nil, err
		}
		if t, perr := time.Parse(rfc, createdAt); perr == nil {
			d.CreatedAt = t
		}
		d.RevokedAt = parseTime(revokedAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

// getAccountByTenantID resolves the account whose tenant_id (instance slug) matches.
func (s *Store) getAccountByTenantID(tenantID string) (Account, error) {
	row := s.db.QueryRow(
		`SELECT account_number,email,tenant_id,status,valid_until,created_at FROM accounts WHERE tenant_id=?`,
		tenantID)
	return scanAccount(row)
}

// CreateDeviceForAccount provisions a new device for an account (passkey login or
// direct issue) and returns it plus the PLAINTEXT refresh token (shown once).
// Mirrors the device creation in RedeemEnrollCode but without an enrollment code.
func (s *Store) CreateDeviceForAccount(now time.Time, accountNumber, label string) (Device, string, error) {
	if _, err := s.GetAccount(accountNumber); err != nil {
		return Device{}, "", err
	}
	dev := Device{
		DeviceID:      newID(),
		AccountNumber: accountNumber,
		Label:         label,
		JTI:           newID(),
		CreatedAt:     now.UTC(),
	}
	refresh, err := randomToken()
	if err != nil {
		return Device{}, "", err
	}
	if _, err := s.db.Exec(
		`INSERT INTO devices(device_id,account_number,label,jti,refresh_hash,created_at,revoked_at) VALUES(?,?,?,?,?,?,NULL)`,
		dev.DeviceID, dev.AccountNumber, dev.Label, dev.JTI, hashToken(refresh), dev.CreatedAt.Format(rfc),
	); err != nil {
		return Device{}, "", fmt.Errorf("controlplane: create device: %w", err)
	}
	return dev, refresh, nil
}

// AddWebauthnCredential stores a registered passkey (opaque JSON blob) for an
// account. credID is the base64url credential id (primary key / lookup).
func (s *Store) AddWebauthnCredential(now time.Time, accountNumber, credID string, credJSON []byte, name string) error {
	if _, err := s.GetAccount(accountNumber); err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT OR REPLACE INTO webauthn_credentials(credential_id,account_number,cred_json,name,created_at) VALUES(?,?,?,?,?)`,
		credID, accountNumber, credJSON, name, now.UTC().Format(rfc),
	); err != nil {
		return fmt.Errorf("controlplane: add webauthn credential: %w", err)
	}
	return nil
}

// WebauthnCredentialBlobs returns the stored credential JSON blobs for an account.
func (s *Store) WebauthnCredentialBlobs(accountNumber string) ([][]byte, error) {
	rows, err := s.db.Query(`SELECT cred_json FROM webauthn_credentials WHERE account_number=?`, accountNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateWebauthnCredential rewrites a credential blob (e.g. after a sign-count bump).
func (s *Store) UpdateWebauthnCredential(credID string, credJSON []byte) error {
	_, err := s.db.Exec(`UPDATE webauthn_credentials SET cred_json=? WHERE credential_id=?`, credJSON, credID)
	return err
}

// PutWebauthnSession parks ceremony SessionData (opaque blob) under a one-time id.
func (s *Store) PutWebauthnSession(id, accountNumber, purpose string, data []byte, expires time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO webauthn_sessions(id,account_number,purpose,data,expires_at) VALUES(?,?,?,?,?)`,
		id, accountNumber, purpose, data, expires.UTC().Format(rfc))
	return err
}

// TakeWebauthnSession atomically fetches and deletes a ceremony session, enforcing
// expiry. One-time: a replayed id returns ErrNotFound; an expired one ErrCodeInvalid.
func (s *Store) TakeWebauthnSession(now time.Time, id string) (accountNumber, purpose string, data []byte, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var expiresAt string
	err = tx.QueryRow(`SELECT account_number,purpose,data,expires_at FROM webauthn_sessions WHERE id=?`, id).
		Scan(&accountNumber, &purpose, &data, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil, ErrNotFound
	}
	if err != nil {
		return "", "", nil, err
	}
	if _, derr := tx.Exec(`DELETE FROM webauthn_sessions WHERE id=?`, id); derr != nil {
		return "", "", nil, derr
	}
	if err := tx.Commit(); err != nil {
		return "", "", nil, err
	}
	if exp, perr := time.Parse(rfc, expiresAt); perr != nil || !exp.After(now) {
		return "", "", nil, ErrCodeInvalid
	}
	return accountNumber, purpose, data, nil
}

// --- helpers ---

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// randomCode returns a short, human-relayable one-time code (Crockford-ish base32,
// no padding) — ~50 bits, fine for a short-lived single-use enrollment code.
func randomCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// randomToken returns a 256-bit opaque token (refresh tokens).
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is catastrophic; surface a clearly-bogus id rather than panic.
		return "id-error"
	}
	return hex.EncodeToString(b)
}

// randomAccountNumber returns a zero-padded 6-digit number (000000–999999).
func randomAccountNumber() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n), nil
}
