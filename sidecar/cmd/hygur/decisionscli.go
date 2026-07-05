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

// runDecisions dispatches the `hygur decisions <dedup>` operator subcommands.
// They resolve the DB path + key like the server (HYGUR_DATA_DIR / HYGUR_DB_KEY,
// keychain fallback) and run in-pod via `kubectl exec` — never over the tenant API.
func runDecisions(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hygur decisions <dedup> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "dedup":
		runDecisionsDedup(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: hygur decisions <dedup> [flags]")
		os.Exit(2)
	}
}

// runDecisionsDedup handles `hygur decisions dedup [--apply]`: find scan-created
// decisions that collapsed onto identical content (same normalized statement AND
// source items sharing a content_hash — the duplicate-attachment bug) and, unless
// --apply is given, only REPORT them (fail-closed dry-run: nothing is deleted).
// With --apply, keep the earliest-created of each group and delete the later
// duplicates (cascade via knowledge_items FK). PII-safe output: statement hashes
// and content ids, never statement text.
func runDecisionsDedup(args []string) {
	fs := flag.NewFlagSet("decisions dedup", flag.ExitOnError)
	apply := fs.Bool("apply", false, "delete the duplicates (default: dry-run, report only)")
	dbPath := fs.String("db", "", "DB path (default: <data dir>/hygur.db)")
	_ = fs.Parse(args)

	src := *dbPath
	if src == "" {
		dataDir, err := resolveDataDir(zerolog.Nop())
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions dedup:", err)
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

	db, err := store.OpenKeyedForCLI(src, key, !*apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decisions dedup open:", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	if all, err := db.ListDecisions(ctx, "", ""); err == nil {
		fmt.Printf("total decisions: %d\n", len(all))
	}
	groups, err := db.FindDuplicateDecisions(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decisions dedup scan:", err)
		os.Exit(1)
	}

	total := 0
	for _, g := range groups {
		total += len(g.Delete)
		fmt.Printf("group stmt=%s… content_hash=%s… keep=%s delete=%v\n",
			g.StatementHash[:12], truncHash(g.ContentHash), g.Keep, g.Delete)
	}
	fmt.Printf("%d duplicate group(s), %d decision(s) to delete\n", len(groups), total)

	if !*apply {
		fmt.Println("dry-run: nothing deleted (pass --apply to delete)")
		return
	}

	var toDelete []string
	for _, g := range groups {
		toDelete = append(toDelete, g.Delete...)
	}
	n, err := db.DeleteDecisions(ctx, toDelete)
	if err != nil {
		fmt.Fprintln(os.Stderr, "decisions dedup delete:", err)
		os.Exit(1)
	}
	fmt.Printf("deleted %d duplicate decision(s)\n", n)
}

func truncHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
