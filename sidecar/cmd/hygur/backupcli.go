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

// runBackupDB handles `hygur backup-db --out <path>`: write a consistent snapshot
// of the live tenant store to a destination file, preserving at-rest encryption.
// It resolves the DB path and key exactly as the server does (HYGUR_DATA_DIR /
// HYGUR_DB_KEY, with the OS-keychain fallback), so the snapshot matches the
// running store. Safe to run concurrently with the server (own read connection);
// this is what the off-box backup job invokes via `kubectl exec`.
func runBackupDB(args []string) {
	fs := flag.NewFlagSet("backup-db", flag.ExitOnError)
	out := fs.String("out", "", "destination snapshot path (required; must not exist)")
	dbPath := fs.String("db", "", "source DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args)

	if *out == "" {
		fmt.Fprintln(os.Stderr, "backup-db: --out is required")
		os.Exit(2)
	}

	src := *dbPath
	if src == "" {
		dataDir, err := resolveDataDir(zerolog.Nop())
		if err != nil {
			fmt.Fprintln(os.Stderr, "backup-db:", err)
			os.Exit(1)
		}
		src = filepath.Join(dataDir, "hygur.db")
	}

	key := os.Getenv("HYGUR_DB_KEY")
	if key == "" {
		if k, ok := (secret.Keychain{}).DBKey(); ok {
			key = k
		}
	}

	_ = os.Remove(*out) // VACUUM INTO / sqlcipher_export require a fresh target
	if err := store.SnapshotTo(context.Background(), src, *out, key); err != nil {
		fmt.Fprintln(os.Stderr, "backup-db:", err)
		os.Exit(1)
	}
	fmt.Printf("snapshot written: %s (encrypted=%t)\n", *out, key != "")
}
