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

// runUsage dispatches the `hygur usage <dump|reset>` operator subcommands. Both resolve
// the DB path + key like the server (HYGUR_DATA_DIR / HYGUR_DB_KEY, keychain fallback)
// and run in-pod via `kubectl exec` — never over the tenant API.
func runUsage(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hygur usage <dump|reset> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "dump":
		runUsageDump(args[1:])
	case "reset":
		runUsageReset(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: hygur usage <dump|reset> [flags]")
		os.Exit(2)
	}
}

// resolveUsageDB returns the DB source path + key, honoring an explicit --db override.
func resolveUsageDB(dbPath string) (string, string) {
	src := dbPath
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
	return src, key
}

// runUsageDump handles `hygur usage dump`: emit this tenant's per-day token usage +
// pricing as JSON on stdout — read-only, so safe to run in-pod alongside the live
// server. The off-box admin cost poll invokes it via `kubectl exec` (like backup-db)
// and ingests the JSON into the control-plane DB.
func runUsageDump(args []string) {
	fs := flag.NewFlagSet("usage dump", flag.ExitOnError)
	since := fs.String("since", "", "include days on/after this date (default: start of the current month)")
	dbPath := fs.String("db", "", "source DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args)

	start := *since
	if start == "" {
		now := time.Now()
		start = fmt.Sprintf("%04d-%02d-01", now.Year(), int(now.Month()))
	}

	src, key := resolveUsageDB(*dbPath)
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

// runUsageReset handles `hygur usage reset`: clear this tenant's running token-usage
// totals (daily/monthly). The OPERATOR reset — run in-pod via `kubectl exec`, never
// exposed on the tenant API, so a tenant can't lift the operator's cap itself.
func runUsageReset(args []string) {
	fs := flag.NewFlagSet("usage reset", flag.ExitOnError)
	dbPath := fs.String("db", "", "DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args)

	src, key := resolveUsageDB(*dbPath)
	n, err := store.ResetTokenUsageAt(context.Background(), src, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "usage reset:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "token usage reset: %d row(s) cleared\n", n)
}
