package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSnapshotTo_EncryptedRoundTrip proves the off-box backup primitive: a
// snapshot taken while the source is open is (a) encrypted under the same key
// and (b) byte-for-byte recoverable.
func TestSnapshotTo_EncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	const key = "snapshot-test-key-123"

	db, err := NewDBWithKey(src, key)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer db.Close()
	if _, err := db.SQLDB().Exec(`CREATE TABLE _snaptest (v INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.SQLDB().Exec(`INSERT INTO _snaptest (v) VALUES (4242)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Snapshot while src is still open — the concurrent-reader path the backup
	// job relies on (the server holds the live DB open).
	dest := filepath.Join(dir, "snap.db")
	if err := SnapshotTo(context.Background(), src, dest, key); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}

	// The snapshot must preserve at-rest encryption: a keyless probe fails, a
	// keyed probe succeeds.
	if err := quickProbe(context.Background(), dest, ""); err == nil {
		t.Fatal("snapshot opened without a key — expected it to be encrypted")
	}
	if err := QuickCheck(context.Background(), dest, key); err != nil {
		t.Fatalf("snapshot not usable with the key: %v", err)
	}

	// Data survived the snapshot.
	snap, err := NewDBWithKey(dest, key)
	if err != nil {
		t.Fatalf("reopen snapshot: %v", err)
	}
	defer snap.Close()
	var v int
	if err := snap.SQLDB().QueryRow(`SELECT v FROM _snaptest`).Scan(&v); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if v != 4242 {
		t.Fatalf("got %d, want 4242", v)
	}
}

// TestSnapshotTo_Plaintext covers the keyless (VACUUM INTO) path.
func TestSnapshotTo_Plaintext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	db, err := NewDB(src)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer db.Close()
	if _, err := db.SQLDB().Exec(`CREATE TABLE _snaptest (v INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.SQLDB().Exec(`INSERT INTO _snaptest (v) VALUES (7)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	dest := filepath.Join(dir, "snap.db")
	if err := SnapshotTo(context.Background(), src, dest, ""); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	// A plaintext snapshot opens with no key.
	if err := quickProbe(context.Background(), dest, ""); err != nil {
		t.Fatalf("plaintext snapshot probe: %v", err)
	}
}

// TestRekeyTo proves a DEK rotation: a DB keyed with OLD becomes openable only
// with NEW after RekeyTo, and the data survives.
func TestRekeyTo(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	const oldKey, newKey = "old-dek-aaa", "new-dek-bbb"

	db, err := NewDBWithKey(src, oldKey)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	if _, err := db.SQLDB().Exec(`CREATE TABLE _rk (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQLDB().Exec(`INSERT INTO _rk (v) VALUES (99)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	dest := filepath.Join(dir, "rekeyed.db")
	if err := RekeyTo(context.Background(), src, dest, oldKey, newKey); err != nil {
		t.Fatalf("RekeyTo: %v", err)
	}
	// Opens with NEW, not with OLD.
	if err := QuickCheck(context.Background(), dest, newKey); err != nil {
		t.Fatalf("rekeyed DB should open with the new key: %v", err)
	}
	if err := quickProbe(context.Background(), dest, oldKey); err == nil {
		t.Fatal("rekeyed DB should NOT open with the old key")
	}
	// Data survived the rotation.
	snap, err := NewDBWithKey(dest, newKey)
	if err != nil {
		t.Fatalf("reopen rekeyed: %v", err)
	}
	defer snap.Close()
	var v int
	if err := snap.SQLDB().QueryRow(`SELECT v FROM _rk`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 99 {
		t.Fatalf("got %d, want 99", v)
	}
}
