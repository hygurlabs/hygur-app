// Command relevance-probe runs a single query against the production
// retrieval pipeline plus alternative strategies, side-by-side, so you can
// compare what each approach surfaces for the same input on your real corpus.
//
// Usage:
//
//	relevance-probe -q "quel est le numéro national d'Jean ?"
//	relevance-probe -q "..." --strategies baseline,judge
//	relevance-probe --queries queries.txt --json > out.json
//
// Read-only with respect to your data: the probe opens the existing DB and
// runs UnifiedSearcher.Search without writing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func main() {
	q := flag.String("q", "", "single query to probe")
	queriesPath := flag.String("queries", "", "path to a text file with one query per line (lines starting with # are ignored)")
	stratList := flag.String("strategies", "baseline,judge", "comma-separated list of strategies to run (baseline, judge, intent)")
	topK := flag.Int("top-k", 5, "number of results to display per strategy")
	jsonOut := flag.Bool("json", false, "emit JSON instead of markdown")
	dataDir := flag.String("data-dir", "", "override HYGUR_DATA_DIR; defaults to ~/.hygur")
	flag.Parse()

	if *q == "" && *queriesPath == "" {
		fmt.Fprintln(os.Stderr, "error: provide -q <query> or --queries <file>")
		flag.Usage()
		os.Exit(2)
	}

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	dir := resolveDataDir(*dataDir, logger)
	cfg, err := config.LoadWithOptions(&config.LoadOptions{ConfigPath: filepath.Join(dir, "config.yaml")})
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	db, err := store.NewDB(cfg.Store.Path)
	if err != nil {
		logger.Fatal().Err(err).Str("path", cfg.Store.Path).Msg("open db")
	}
	defer db.Close()

	llmClient := llm.NewClient(&cfg.LMStudio)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	strategies, err := parseStrategies(*stratList)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse strategies")
	}

	queries, err := loadQueries(*q, *queriesPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("load queries")
	}

	searcher := retrieval.NewUnifiedSearcher(db, llmClient)
	authoritySearcher := retrieval.NewUnifiedSearcher(db, llmClient)
	authoritySearcher.SetAuthorityRerank(true)
	r := &runner{
		searcher:          searcher,
		authoritySearcher: authoritySearcher,
		llm:               llmClient,
		db:                db,
		topK:              *topK,
		strategies:        strategies,
	}

	reports := make([]queryReport, 0, len(queries))
	for _, query := range queries {
		report := r.run(ctx, query)
		reports = append(reports, report)
	}

	if *jsonOut {
		emitJSON(os.Stdout, reports)
	} else {
		emitMarkdown(os.Stdout, reports)
	}
}

func resolveDataDir(override string, logger zerolog.Logger) string {
	if override != "" {
		return override
	}
	if d := os.Getenv("HYGUR_DATA_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Fatal().Err(err).Msg("home dir lookup failed")
	}
	return filepath.Join(home, ".hygur")
}

func parseStrategies(list string) ([]string, error) {
	if list == "" {
		return nil, fmt.Errorf("empty strategy list")
	}
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		switch p {
		case "baseline", "judge", "intent", "authority":
			out = append(out, p)
		default:
			return nil, fmt.Errorf("unknown strategy %q (supported: baseline, judge, intent, authority)", p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid strategy in %q", list)
	}
	return out, nil
}

func loadQueries(single, path string) ([]string, error) {
	if single != "" {
		return []string{single}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var qs []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		qs = append(qs, line)
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("no queries in %s", path)
	}
	return qs, nil
}
