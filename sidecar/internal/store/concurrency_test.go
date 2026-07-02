package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// isLockErr reports whether err is a SQLite contention error — SQLITE_BUSY
// ("database is locked") or SQLITE_LOCKED ("database table is locked"). These
// are exactly the failures WP19 (WAL + _busy_timeout) is meant to eliminate for
// the file-backed live DB.
func isLockErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "locked") || strings.Contains(msg, "busy")
}

func mkConcItem(id string) *KnowledgeItem {
	now := time.Now()
	return &KnowledgeItem{
		ContentID:      id,
		SourceType:     "note",
		Title:          "concurrent",
		NormalizedText: "concurrent write body " + id,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// TestConcurrentReadWrite_NoDatabaseLocked runs a writer goroutine looping
// INSERTs alongside a second goroutine doing interleaved reads and writes
// against a FILE-BACKED DB for ~1.5 s. It asserts ZERO "database is locked" /
// SQLITE_BUSY errors. With the default rollback journal, no busy_timeout, and an
// unbounded pool (the pre-WP19 behaviour) this flakes almost immediately when
// the two writers collide; with WAL + _busy_timeout=5000 + bounded pool it is
// clean.
func TestConcurrentReadWrite_NoDatabaseLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db, err := NewDB(path) // file-backed, NOT :memory:
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	deadline := time.Now().Add(1500 * time.Millisecond)

	var mu sync.Mutex
	var lockErrs []error
	var otherErrs []error
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if isLockErr(err) {
			lockErrs = append(lockErrs, err)
		} else {
			otherErrs = append(otherErrs, err)
		}
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: a tight INSERT loop.
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(deadline); i++ {
			record(db.InsertKnowledgeItem(ctx, mkConcItem(fmt.Sprintf("w-%d", i))))
		}
	}()

	// Reader + writer: interleaves counts/gets (reads) with its own INSERTs.
	go func() {
		defer wg.Done()
		for i := 0; time.Now().Before(deadline); i++ {
			record(db.InsertKnowledgeItem(ctx, mkConcItem(fmt.Sprintf("r-%d", i))))
			if _, err := db.CountKnowledgeItems(ctx); err != nil {
				record(err)
			}
			if _, err := db.GetKnowledgeItem(ctx, fmt.Sprintf("w-%d", i)); err != nil {
				record(err)
			}
		}
	}()

	wg.Wait()

	if len(lockErrs) > 0 {
		t.Fatalf("got %d SQLITE_BUSY/locked errors under concurrency (want 0); first: %v", len(lockErrs), lockErrs[0])
	}
	if len(otherErrs) > 0 {
		t.Fatalf("got %d unexpected errors under concurrency; first: %v", len(otherErrs), otherErrs[0])
	}

	// Sanity: both goroutines actually committed work.
	n, err := db.CountKnowledgeItems(ctx)
	if err != nil {
		t.Fatalf("final count: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected rows written, got 0")
	}
}

// TestLiveDSN_WALForFileBackedNotMemory pins the WP19 DSN policy: the
// file-backed live DSN carries WAL + busy_timeout + synchronous=NORMAL (and, when
// keyed, still the SQLCipher params), while the :memory: DSN gets none of the
// concurrency pragmas.
func TestLiveDSN_WALForFileBackedNotMemory(t *testing.T) {
	file := liveDSN("/var/lib/hygur/tenant.db", "")
	for _, want := range []string{"_journal_mode=WAL", "_busy_timeout=5000", "_synchronous=NORMAL", "_foreign_keys=on"} {
		if !strings.Contains(file, want) {
			t.Errorf("file-backed DSN missing %q: %s", want, file)
		}
	}

	mem := liveDSN(":memory:", "")
	if strings.Contains(mem, "_journal_mode=WAL") {
		t.Errorf(":memory: DSN must NOT get WAL: %s", mem)
	}
	if strings.Contains(mem, "_busy_timeout") {
		t.Errorf(":memory: DSN must NOT get busy_timeout: %s", mem)
	}
	if !strings.Contains(mem, "mode=memory") {
		t.Errorf(":memory: DSN lost its in-memory form: %s", mem)
	}

	// A keyed file-backed DSN keeps BOTH the concurrency pragmas and the cipher params.
	keyed := liveDSN("/var/lib/hygur/tenant.db", "s3cr3t")
	for _, want := range []string{"_journal_mode=WAL", "_busy_timeout=5000", "_pragma_key=", "_pragma_cipher_page_size=4096"} {
		if !strings.Contains(keyed, want) {
			t.Errorf("keyed file DSN missing %q: %s", want, keyed)
		}
	}
}

// TestFileBackedDB_JournalModeIsWAL proves the pragma actually takes effect: a
// file-backed DB reports journal_mode=wal, while a :memory: DB does not.
func TestFileBackedDB_JournalModeIsWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	db, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.SQLDB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Errorf("file-backed journal_mode = %q, want wal", mode)
	}

	mem, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB(:memory:): %v", err)
	}
	defer mem.Close()
	var memMode string
	if err := mem.SQLDB().QueryRow("PRAGMA journal_mode").Scan(&memMode); err != nil {
		t.Fatalf("PRAGMA journal_mode (:memory:): %v", err)
	}
	if strings.ToLower(memMode) == "wal" {
		t.Errorf(":memory: journal_mode = %q, must not be wal", memMode)
	}
}
