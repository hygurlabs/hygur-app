// Command hygur-reindex re-runs entity extraction on already-indexed emails
// without re-fetching from Gmail or recomputing embeddings.
//
// Usage:
//
//	hygur-reindex                       # default: tier1 only
//	hygur-reindex --tier=tier1          # explicit
//	hygur-reindex --batch-size=200
//	hygur-reindex --data-dir=/custom/path
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
	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func main() {
	tier := flag.String("tier", "tier1", "extraction tier(s) to run: tier1 (more tiers will be added later)")
	batchSize := flag.Int("batch-size", 100, "items processed per batch")
	dataDir := flag.String("data-dir", "", "override HYGUR_DATA_DIR; defaults to ~/.hygur")
	flag.Parse()

	if *tier != "tier1" {
		fmt.Fprintf(os.Stderr, "tier %q not yet supported (only tier1)\n", *tier)
		os.Exit(2)
	}

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	dir := *dataDir
	if dir == "" {
		dir = os.Getenv("HYGUR_DATA_DIR")
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			logger.Fatal().Err(err).Msg("home dir lookup failed")
		}
		dir = filepath.Join(home, ".hygur")
	}

	cfg, err := config.LoadWithOptions(&config.LoadOptions{ConfigPath: filepath.Join(dir, "config.yaml")})
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	db, err := store.NewDB(cfg.Store.Path)
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfg.Store.Path).Msg("open db")
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info().Str("tier", *tier).Int("batch_size", *batchSize).Str("db", cfg.Store.Path).Msg("reindex starting")

	start := time.Now()
	stats, err := mail.ReindexEntitiesTier1(ctx, db, logger, *batchSize)
	dur := time.Since(start)
	if err != nil {
		logger.Error().Err(err).Msg("reindex failed")
		printStats(stats, dur)
		os.Exit(1)
	}

	printStats(stats, dur)
}

func printStats(s *mail.ReindexStats, dur time.Duration) {
	fmt.Printf("--- reindex done in %s ---\n", dur.Round(time.Millisecond))
	fmt.Printf("  total emails processed : %d\n", s.Total)
	fmt.Printf("  updated                : %d\n", s.Updated)
	fmt.Printf("  unchanged (skipped)    : %d\n", s.Skipped)
	fmt.Printf("  errors                 : %d\n", s.Errors)
	fmt.Printf("  high_priority flagged  : %d\n", s.HighPrio)
	fmt.Printf("  with IBAN              : %d\n", s.WithIBAN)
	fmt.Printf("  with amount            : %d\n", s.WithAmount)
	fmt.Printf("  with structured comm   : %d\n", s.WithComm)
}
