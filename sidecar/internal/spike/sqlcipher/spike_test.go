//go:build sqlcipher_spike

// C1 feasibility spike. Proves, in isolation, the three risky unknowns before
// swapping the production driver:
//   1. SQLCipher encrypts the DB at rest (sqlite3.IsEncrypted == true).
//   2. FTS5 works under the SQLCipher build (the lexical index Hygur relies on).
//   3. An existing plaintext DB migrates to encrypted via sqlcipher_export.
//
// Run: CGO_ENABLED=1 go test -tags 'sqlcipher_spike sqlite_fts5' ./internal/spike/sqlcipher/ -v
package sqlcipher

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	sqlite3 "github.com/mutecomm/go-sqlcipher/v4" // registers driver "sqlite3" (SQLCipher)
)

// openKeyed opens an encrypted DB, passing the passphrase via the DSN (mutecomm
// applies _pragma_key on every pooled connection — the documented mechanism).
func openKeyed(t *testing.T, path, key string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_pragma_key=%s&_pragma_cipher_page_size=4096",
		path, url.QueryEscape(key))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return db
}

func TestSQLCipher_EncryptionAndFTS5(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "enc.db")
	const key = "spike-passphrase"

	db := openKeyed(t, dbPath, key)
	// FTS5 — the unknown to prove. Fails with "no such module: fts5" if absent.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE docs USING fts5(body)`); err != nil {
		db.Close()
		t.Fatalf("CREATE fts5: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO docs(body) VALUES ('déclaration TVA trimestrielle'), ('facture EDF')`); err != nil {
		db.Close()
		t.Fatalf("insert: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM docs WHERE docs MATCH 'tva'`).Scan(&n); err != nil {
		db.Close()
		t.Fatalf("fts5 match: %v", err)
	}
	if n != 1 {
		t.Errorf("fts5 match count = %d, want 1", n)
	}
	db.Close()

	// (1) The file on disk is genuinely encrypted.
	enc, err := sqlite3.IsEncrypted(dbPath)
	if err != nil {
		t.Fatalf("IsEncrypted: %v", err)
	}
	if !enc {
		t.Errorf("DB is NOT encrypted at rest")
	}

	// Wrong key must fail to read.
	bad := openKeyed(t, dbPath, "wrong-key")
	var x int
	if err := bad.QueryRow(`SELECT count(*) FROM docs`).Scan(&x); err == nil {
		t.Errorf("expected read with wrong key to fail, got count=%d", x)
	}
	bad.Close()

	// Right key reopens fine, data + FTS5 intact.
	good := openKeyed(t, dbPath, key)
	if err := good.QueryRow(`SELECT count(*) FROM docs WHERE docs MATCH 'edf'`).Scan(&n); err != nil {
		t.Errorf("reopen with right key: %v", err)
	} else if n != 1 {
		t.Errorf("fts5 hits after reopen = %d, want 1", n)
	}
	good.Close()
}

func TestSQLCipher_ExportMigration(t *testing.T) {
	dir := t.TempDir()
	plainPath := filepath.Join(dir, "plain.db")
	encPath := filepath.Join(dir, "migrated.db")
	const key = "migration-key"

	// A plaintext DB (no key) with data + an FTS5 table — like today's hygur.db.
	plain, err := sql.Open("sqlite3", plainPath)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE items(id INTEGER PRIMARY KEY, title TEXT)`,
		`INSERT INTO items(title) VALUES ('a'),('b'),('c')`,
		`CREATE VIRTUAL TABLE docs USING fts5(body)`,
		`INSERT INTO docs(body) VALUES ('général')`,
	} {
		if _, err := plain.Exec(stmt); err != nil {
			plain.Close()
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// Migrate: ATTACH an encrypted DB and export into it.
	if _, err := plain.Exec(`ATTACH DATABASE ? AS enc KEY ?`, encPath, key); err != nil {
		plain.Close()
		t.Fatalf("attach: %v", err)
	}
	if _, err := plain.Exec(`SELECT sqlcipher_export('enc')`); err != nil {
		plain.Close()
		t.Fatalf("sqlcipher_export: %v", err)
	}
	if _, err := plain.Exec(`DETACH DATABASE enc`); err != nil {
		plain.Close()
		t.Fatalf("detach: %v", err)
	}
	plain.Close()

	// Source stayed plaintext, target is encrypted.
	if e, _ := sqlite3.IsEncrypted(plainPath); e {
		t.Errorf("source DB should be plaintext")
	}
	if e, err := sqlite3.IsEncrypted(encPath); err != nil {
		t.Fatalf("IsEncrypted(migrated): %v", err)
	} else if !e {
		t.Errorf("migrated DB should be encrypted")
	}

	// The migrated DB opens with the key, keeps the data AND the FTS5 table.
	encDB := openKeyed(t, encPath, key)
	defer encDB.Close()
	var items, hits int
	if err := encDB.QueryRow(`SELECT count(*) FROM items`).Scan(&items); err != nil {
		t.Fatalf("read items: %v", err)
	}
	if items != 3 {
		t.Errorf("items after migration = %d, want 3", items)
	}
	if err := encDB.QueryRow(`SELECT count(*) FROM docs WHERE docs MATCH 'général'`).Scan(&hits); err != nil {
		t.Fatalf("fts5 match after migration: %v", err)
	}
	if hits != 1 {
		t.Errorf("fts5 hits after migration = %d, want 1", hits)
	}
}
