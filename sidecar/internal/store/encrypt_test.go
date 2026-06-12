package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "github.com/mutecomm/go-sqlcipher/v4"
)

// TestNewDBWithKey_EncryptsAndReopens: a keyed DB is encrypted at rest, the
// wrong key can't open it, and the right key round-trips data.
func TestNewDBWithKey_EncryptsAndReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.db")
	const key = "test-db-key"
	ctx := context.Background()

	db, err := NewDBWithKey(path, key)
	if err != nil {
		t.Fatalf("NewDBWithKey: %v", err)
	}
	now := time.Now()
	if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
		ContentID: "k1", SourceType: "note", Title: "secret", NormalizedText: "corps",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	if enc, err := sqlite3.IsEncrypted(path); err != nil {
		t.Fatalf("IsEncrypted: %v", err)
	} else if !enc {
		t.Fatalf("DB is not encrypted at rest")
	}

	// Wrong key must fail (the first migration read can't decrypt).
	if bad, err := NewDBWithKey(path, "wrong-key"); err == nil {
		bad.Close()
		t.Errorf("expected open with wrong key to fail")
	}

	// Right key reopens; data intact through the cipher.
	re, err := NewDBWithKey(path, key)
	if err != nil {
		t.Fatalf("reopen with right key: %v", err)
	}
	defer re.Close()
	item, err := re.GetKnowledgeItem(ctx, "k1")
	if err != nil || item == nil {
		t.Fatalf("get after reopen: err=%v item=%v", err, item)
	}
	if item.Title != "secret" {
		t.Errorf("title after reopen = %q, want %q", item.Title, "secret")
	}
}

// TestMigratePlaintextToEncrypted: an existing plaintext DB with the FULL schema
// (migrations, incl. FTS5) migrates to encrypted via sqlcipher_export without
// data loss, and the plaintext backup is preserved.
func TestMigratePlaintextToEncrypted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hygur.db")
	const key = "migrate-key"
	ctx := context.Background()

	// Plaintext DB with the real schema + a row.
	db, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	now := time.Now()
	if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
		ContentID: "m1", SourceType: "note", Title: "avant-migration", NormalizedText: "x",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()

	if e, _ := sqlite3.IsEncrypted(path); e {
		t.Fatalf("source DB should be plaintext before migration")
	}

	if err := MigratePlaintextToEncrypted(path, key); err != nil {
		t.Fatalf("MigratePlaintextToEncrypted: %v", err)
	}

	// In place: now encrypted; the backup is kept and stays plaintext.
	if e, err := sqlite3.IsEncrypted(path); err != nil || !e {
		t.Fatalf("migrated DB should be encrypted (enc=%v err=%v)", e, err)
	}
	if e, _ := sqlite3.IsEncrypted(path + ".plaintext.bak"); e {
		t.Errorf("plaintext backup should not be encrypted")
	}

	// Reopen encrypted; data survived the export.
	re, err := NewDBWithKey(path, key)
	if err != nil {
		t.Fatalf("reopen encrypted: %v", err)
	}
	defer re.Close()
	item, err := re.GetKnowledgeItem(ctx, "m1")
	if err != nil || item == nil || item.Title != "avant-migration" {
		t.Fatalf("data lost in migration: err=%v item=%v", err, item)
	}
}

// TestBackupTo: a plaintext source yields a plaintext snapshot with the data;
// an encrypted source yields a snapshot encrypted with the same key.
func TestBackupTo(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	seed := func(db *DB) {
		if err := db.InsertKnowledgeItem(ctx, &KnowledgeItem{
			ContentID: "b1", SourceType: "note", Title: "snap", NormalizedText: "x",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("plaintext", func(t *testing.T) {
		dir := t.TempDir()
		db, err := NewDB(filepath.Join(dir, "src.db"))
		if err != nil {
			t.Fatalf("NewDB: %v", err)
		}
		seed(db)
		dest := filepath.Join(dir, "snap.db")
		if err := db.BackupTo(ctx, dest, ""); err != nil {
			t.Fatalf("BackupTo: %v", err)
		}
		db.Close()
		if e, _ := sqlite3.IsEncrypted(dest); e {
			t.Errorf("plaintext backup should not be encrypted")
		}
		re, err := NewDB(dest)
		if err != nil {
			t.Fatalf("open snapshot: %v", err)
		}
		defer re.Close()
		if item, err := re.GetKnowledgeItem(ctx, "b1"); err != nil || item == nil {
			t.Fatalf("snapshot missing data: err=%v item=%v", err, item)
		}
	})

	t.Run("encrypted", func(t *testing.T) {
		dir := t.TempDir()
		const key = "snap-key"
		db, err := NewDBWithKey(filepath.Join(dir, "src.db"), key)
		if err != nil {
			t.Fatalf("NewDBWithKey: %v", err)
		}
		seed(db)
		dest := filepath.Join(dir, "snap.db")
		if err := db.BackupTo(ctx, dest, key); err != nil {
			t.Fatalf("BackupTo: %v", err)
		}
		db.Close()
		if e, err := sqlite3.IsEncrypted(dest); err != nil || !e {
			t.Fatalf("encrypted backup should be encrypted (enc=%v err=%v)", e, err)
		}
		re, err := NewDBWithKey(dest, key)
		if err != nil {
			t.Fatalf("open encrypted snapshot: %v", err)
		}
		defer re.Close()
		if item, err := re.GetKnowledgeItem(ctx, "b1"); err != nil || item == nil {
			t.Fatalf("snapshot missing data: err=%v item=%v", err, item)
		}
	})
}

// TestQuickCheck: validates the restore gate — plaintext accepted (even with a
// key, since boot auto-migrates), encrypted accepted only with the right key,
// garbage rejected.
func TestQuickCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.db")
	if db, err := NewDB(plain); err != nil {
		t.Fatalf("NewDB: %v", err)
	} else {
		db.Close()
	}
	if err := QuickCheck(ctx, plain, ""); err != nil {
		t.Errorf("plaintext should pass with no key: %v", err)
	}
	if err := QuickCheck(ctx, plain, "any-key"); err != nil {
		t.Errorf("plaintext should pass even with a key (boot auto-migrates): %v", err)
	}

	enc := filepath.Join(dir, "enc.db")
	if db, err := NewDBWithKey(enc, "k"); err != nil {
		t.Fatalf("NewDBWithKey: %v", err)
	} else {
		db.Close()
	}
	if err := QuickCheck(ctx, enc, "k"); err != nil {
		t.Errorf("encrypted should pass with the right key: %v", err)
	}
	if err := QuickCheck(ctx, enc, "wrong"); err == nil {
		t.Errorf("encrypted should fail with the wrong key")
	}
	if err := QuickCheck(ctx, enc, ""); err == nil {
		t.Errorf("encrypted should fail with no key")
	}

	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not a database at all"), 0600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if err := QuickCheck(ctx, garbage, "k"); err == nil {
		t.Errorf("garbage should be rejected")
	}
}

// TestApplyPendingRestore: a staged snapshot is swapped in at boot, the previous
// DB is kept as .pre-restore.bak, and the live DB now holds the staged data.
func TestApplyPendingRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "hygur.db")

	// Live DB with item A.
	live, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	now := time.Now()
	_ = live.InsertKnowledgeItem(ctx, &KnowledgeItem{ContentID: "A", SourceType: "note", Title: "live", NormalizedText: "x", CreatedAt: now, UpdatedAt: now})
	// Staged snapshot with item B.
	staged := path + ".restore-pending"
	if err := live.BackupTo(ctx, staged, ""); err != nil {
		t.Fatalf("BackupTo (staged base): %v", err)
	}
	live.Close()
	// Make the staged snapshot actually differ (add B, remove A) so we can tell them apart.
	sdb, err := NewDB(staged)
	if err != nil {
		t.Fatalf("open staged: %v", err)
	}
	_, _ = sdb.SQLDB().ExecContext(ctx, "DELETE FROM knowledge_items WHERE content_id='A'")
	_ = sdb.InsertKnowledgeItem(ctx, &KnowledgeItem{ContentID: "B", SourceType: "note", Title: "staged", NormalizedText: "y", CreatedAt: now, UpdatedAt: now})
	sdb.Close()

	applied, err := ApplyPendingRestore(path)
	if err != nil || !applied {
		t.Fatalf("ApplyPendingRestore: applied=%v err=%v", applied, err)
	}
	if _, err := os.Stat(path + ".pre-restore.bak"); err != nil {
		t.Errorf("expected pre-restore backup: %v", err)
	}
	if _, err := os.Stat(staged); err == nil {
		t.Errorf("staged file should be consumed")
	}

	re, err := NewDB(path)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer re.Close()
	if item, _ := re.GetKnowledgeItem(ctx, "B"); item == nil {
		t.Errorf("restored DB should contain staged item B")
	}
	if item, _ := re.GetKnowledgeItem(ctx, "A"); item != nil {
		t.Errorf("restored DB should NOT contain the old item A")
	}

	// No pending → no-op.
	if applied, err := ApplyPendingRestore(path); err != nil || applied {
		t.Errorf("expected no-op when nothing staged: applied=%v err=%v", applied, err)
	}
}

// TestOpen_PlaintextWhenNoKey: Open with an empty key behaves exactly like NewDB
// (the default — existing installs are unaffected by the swap).
func TestOpen_PlaintextWhenNoKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.db")
	db, err := Open(path, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()
	if e, _ := sqlite3.IsEncrypted(path); e {
		t.Errorf("Open with empty key should be plaintext")
	}
}

// TestOpen_AutoMigratesExistingPlaintext: the boot path — an existing plaintext
// DB is transparently migrated to encrypted on the first run with a key, data
// intact, with a backup kept.
func TestOpen_AutoMigratesExistingPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hygur.db")
	const key = "boot-key"
	ctx := context.Background()

	// Pre-existing plaintext DB with a row.
	seed, err := NewDB(path)
	if err != nil {
		t.Fatalf("seed NewDB: %v", err)
	}
	now := time.Now()
	if err := seed.InsertKnowledgeItem(ctx, &KnowledgeItem{
		ContentID: "o1", SourceType: "note", Title: "garde-moi", NormalizedText: "x",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	seed.Close()

	// Boot with a key → auto-migrate + open encrypted.
	db, err := Open(path, key)
	if err != nil {
		t.Fatalf("Open (auto-migrate): %v", err)
	}
	defer db.Close()

	if e, err := sqlite3.IsEncrypted(path); err != nil || !e {
		t.Fatalf("DB should be encrypted after Open (enc=%v err=%v)", e, err)
	}
	if _, err := os.Stat(path + ".plaintext.bak"); err != nil {
		t.Errorf("expected plaintext backup, stat err: %v", err)
	}
	item, err := db.GetKnowledgeItem(ctx, "o1")
	if err != nil || item == nil || item.Title != "garde-moi" {
		t.Fatalf("data lost during auto-migration: err=%v item=%v", err, item)
	}

	// Idempotent: a second Open with the same key just opens (no re-migrate).
	db.Close()
	db2, err := Open(path, key)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()
	if item, err := db2.GetKnowledgeItem(ctx, "o1"); err != nil || item == nil {
		t.Fatalf("data missing on second Open: err=%v item=%v", err, item)
	}
}
