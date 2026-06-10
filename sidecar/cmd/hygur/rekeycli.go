package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hygur/sidecar/internal/secret"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// runRekey handles `hygur rekey`: rotate a SQLCipher DEK by re-encrypting the DB
// under a new key (reuses the backup-db snapshot machinery).
//
//	--out <path>   write a rekeyed COPY there and stop (non-destructive — for
//	               testing the rotation against real data with zero risk).
//	(no --out)     swap in place: write a temp copy, verify it opens with the new
//	               key, then atomically replace the live DB. The server MUST be
//	               quiesced first (e.g. scale the tenant to 0) — stale -wal/-shm
//	               are dropped, mirroring store.ApplyPendingRestore.
//
// Old key: --old-key or HYGUR_DB_KEY (keychain fallback). New key: --new-key or
// HYGUR_NEW_DB_KEY (env preferred, so the secret never lands in shell history).
// Rollback if anything goes wrong: restore the off-box backup taken just before
// (it carries the OLD-key DB + the OLD DEK).
func runRekey(args []string) {
	fs := flag.NewFlagSet("rekey", flag.ExitOnError)
	dbPath := fs.String("db", "", "DB path (default: <data dir>/hygur.db)")
	out := fs.String("out", "", "write the rekeyed copy here (non-destructive test); omit to swap in place")
	oldFlag := fs.String("old-key", "", "current key (default: HYGUR_DB_KEY env / keychain)")
	newFlag := fs.String("new-key", "", "new key (default: HYGUR_NEW_DB_KEY env)")
	_ = fs.Parse(args)

	oldKey := *oldFlag
	if oldKey == "" {
		oldKey = os.Getenv("HYGUR_DB_KEY")
		if oldKey == "" {
			if k, ok := (secret.Keychain{}).DBKey(); ok {
				oldKey = k
			}
		}
	}
	newKey := *newFlag
	if newKey == "" {
		newKey = os.Getenv("HYGUR_NEW_DB_KEY")
	}
	if newKey == "" {
		fmt.Fprintln(os.Stderr, "rekey: --new-key or HYGUR_NEW_DB_KEY is required")
		os.Exit(2)
	}
	if newKey == oldKey {
		fmt.Fprintln(os.Stderr, "rekey: new key equals old key — nothing to do")
		os.Exit(2)
	}

	src := *dbPath
	if src == "" {
		dataDir, err := resolveDataDir(zerolog.Nop())
		if err != nil {
			fmt.Fprintln(os.Stderr, "rekey:", err)
			os.Exit(1)
		}
		src = filepath.Join(dataDir, "hygur.db")
	}
	ctx := context.Background()

	// Non-destructive: write a rekeyed copy, verify it, stop.
	if *out != "" {
		_ = os.Remove(*out)
		if err := store.RekeyTo(ctx, src, *out, oldKey, newKey); err != nil {
			fmt.Fprintln(os.Stderr, "rekey:", err)
			os.Exit(1)
		}
		if err := store.QuickCheck(ctx, *out, newKey); err != nil {
			fmt.Fprintln(os.Stderr, "rekey: copy does not open with the new key:", err)
			os.Exit(1)
		}
		fmt.Printf("rekeyed copy written + verified: %s\n", *out)
		return
	}

	// In-place swap. Write → verify-with-new-key → atomic replace.
	tmp := src + ".rekey-tmp"
	_ = os.Remove(tmp)
	if err := store.RekeyTo(ctx, src, tmp, oldKey, newKey); err != nil {
		fmt.Fprintln(os.Stderr, "rekey:", err)
		os.Exit(1)
	}
	if err := store.QuickCheck(ctx, tmp, newKey); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "rekey: rekeyed DB does not open with the new key — aborting swap:", err)
		os.Exit(1)
	}
	_ = os.Remove(src + "-wal")
	_ = os.Remove(src + "-shm")
	if err := os.Rename(tmp, src); err != nil {
		fmt.Fprintln(os.Stderr, "rekey: swap failed:", err)
		os.Exit(1)
	}
	fmt.Printf("rekeyed in place: %s — now set HYGUR_DB_KEY to the new key and restart\n", src)
}
