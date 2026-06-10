package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hygur/sidecar/internal/secret"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// runUsage handles `hygur usage dump`: emit this tenant's per-day token usage +
// pricing as JSON on stdout — read-only, so safe to run in-pod alongside the
// live server. The off-box admin cost poll invokes it via `kubectl exec` (like
// backup-db) and ingests the JSON into the control-plane DB. DB path + key are
// resolved like the server (HYGUR_DATA_DIR / HYGUR_DB_KEY, keychain fallback).
func runUsage(args []string) {
	if len(args) == 0 || args[0] != "dump" {
		fmt.Fprintln(os.Stderr, "usage: hygur usage dump [--since YYYY-MM-DD] [--db PATH]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("usage dump", flag.ExitOnError)
	since := fs.String("since", "", "include days on/after this date (default: start of the current month)")
	dbPath := fs.String("db", "", "source DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args[1:])

	start := *since
	if start == "" {
		now := time.Now()
		start = fmt.Sprintf("%04d-%02d-01", now.Year(), int(now.Month()))
	}

	src := *dbPath
	if src == "" {
		dataDir, err := resolveDataDir(zerolog.Nop())
		if err != nil {
			fmt.Fprintln(os.Stderr, "usage:", err)
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

	days, pricing, err := store.DumpTokenUsage(context.Background(), src, key, start)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage dump:", err)
		os.Exit(1)
	}

	out := struct {
		Since   string                   `json:"since"`
		Pricing store.Pricing            `json:"pricing"`
		Days    []store.DayCategoryUsage `json:"days"`
	}{Since: start, Pricing: pricing, Days: days}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "usage dump encode:", err)
		os.Exit(1)
	}
}
