package store

import (
	"context"
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
