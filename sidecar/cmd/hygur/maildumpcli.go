package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hygur/sidecar/internal/secret"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// runMailBreakdown handles `hygur mail-breakdown`: emit mail items grouped by
// source mailbox (count + text bytes) as JSON on stdout. Read-only — the dry-run
// for the differential KB purge, safe to run in-pod alongside the live server.
// DB path + key resolved like the server (HYGUR_DATA_DIR / HYGUR_DB_KEY).
func runMailBreakdown(args []string) {
	fs := flag.NewFlagSet("mail-breakdown", flag.ExitOnError)
	dbPath := fs.String("db", "", "source DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args)

	src := *dbPath
	if src == "" {
		dataDir, err := resolveDataDir(zerolog.Nop())
		if err != nil {
			fmt.Fprintln(os.Stderr, "mail-breakdown:", err)
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

	stats, err := store.DumpMailBreakdown(context.Background(), src, key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mail-breakdown:", err)
		os.Exit(1)
	}
	var totalCount int
	var totalBytes int64
	for _, s := range stats {
		totalCount += s.Count
		totalBytes += s.TextBytes
	}
	out := struct {
		ByMailbox  []store.MailboxStat `json:"by_mailbox"`
		TotalItems int                 `json:"total_items"`
		TotalBytes int64               `json:"total_text_bytes"`
	}{ByMailbox: stats, TotalItems: totalCount, TotalBytes: totalBytes}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "mail-breakdown encode:", err)
		os.Exit(1)
	}
}
