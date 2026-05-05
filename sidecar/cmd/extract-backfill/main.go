// Command extract-backfill re-runs Tier 1 (regex) and Tier 2 (LLM NER)
// extraction on every knowledge item already in the store, writing the
// resulting structured entities into the metadata column. The chunk text and
// embeddings are untouched.
//
// The Tier 2 pass is skipped for items that already carry the current
// extracted_v2_version stamp, so re-running is cheap and idempotent.
//
// Usage:
//
//	extract-backfill                      # tier1 + tier2, default settings
//	extract-backfill --skip-tier2         # tier1 only (fast pre-pass)
//	extract-backfill --dry-run            # estimate without writing
//	extract-backfill --batch-size=200
//	extract-backfill --data-dir=/custom/path
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/extract"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func main() {
	batchSize := flag.Int("batch-size", 100, "items processed per batch")
	dryRun := flag.Bool("dry-run", false, "run extraction without persisting")
	skipTier2 := flag.Bool("skip-tier2", false, "skip Tier 2 LLM extraction")
	dataDir := flag.String("data-dir", "", "override HYGUR_DATA_DIR; defaults to ~/.hygur")
	progressEvery := flag.Int("progress-every", 25, "emit progress every N items processed (0 to disable)")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = os.Getenv("HYGUR_DATA_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fatal("home dir lookup failed: %v", err)
		}
		dir = filepath.Join(home, ".hygur")
	}

	cfg, err := config.LoadWithOptions(&config.LoadOptions{ConfigPath: filepath.Join(dir, "config.yaml")})
	if err != nil {
		fatal("load config: %v", err)
	}

	db, err := store.NewDB(cfg.Store.Path)
	if err != nil {
		fatal("open db at %s: %v", cfg.Store.Path, err)
	}
	defer db.Close()

	var llmClient *llm.Client
	if !*skipTier2 {
		llmClient = llm.NewClient(&cfg.LMStudio)
		ok, err := llmClient.Ping(context.Background())
		if err != nil || !ok {
			fmt.Fprintf(os.Stderr, "warning: LM Studio at %s not reachable (err=%v) — falling back to --skip-tier2\n",
				cfg.LMStudio.URL, err)
			*skipTier2 = true
			llmClient = nil
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("backfill starting: db=%s skip_tier2=%v dry_run=%v batch_size=%d\n",
		cfg.Store.Path, *skipTier2, *dryRun, *batchSize)

	start := time.Now()
	stats, err := extract.Backfill(ctx, db, llmClient, extract.BackfillOptions{
		BatchSize:     *batchSize,
		DryRun:        *dryRun,
		SkipTier2:     *skipTier2,
		ProgressEvery: *progressEvery,
		ProgressFn: func(processed int, s extract.BackfillStats) {
			fmt.Printf("  progress: processed=%d updated_t1=%d updated_t2=%d skipped_v2=%d errors=%d\n",
				processed, s.UpdatedTier1, s.UpdatedTier2, s.SkippedV2, s.Errors)
		},
	})
	dur := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill failed: %v\n", err)
		printStats(stats, dur)
		os.Exit(1)
	}
	printStats(stats, dur)
}

func printStats(s *extract.BackfillStats, dur time.Duration) {
	if s == nil {
		fmt.Println("(no stats)")
		return
	}
	fmt.Printf("--- backfill done in %s ---\n", dur.Round(time.Millisecond))
	fmt.Printf("  total processed   : %d\n", s.Total)
	fmt.Printf("  tier1 updates     : %d\n", s.UpdatedTier1)
	fmt.Printf("  tier2 updates     : %d\n", s.UpdatedTier2)
	fmt.Printf("  tier2 skipped     : %d (already at current version)\n", s.SkippedV2)
	fmt.Printf("  errors            : %d\n", s.Errors)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
