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
	"time"

	"github.com/hygur/sidecar/internal/edge"
)

// runEdge is the edge run-mode (C7-E3): runs device-local connectors (Files for
// now), extracts text locally, and pushes it to a central server with a device
// token. No KB/LLM/embeddings here. CLI-launched; Tauri spawns it later (E4).
//
//	hygur edge --server https://<tenant>.hygur.ai --token-file ~/.hygur-edge/token \
//	           --folder ~/Documents [--interval 15m]
func runEdge(args []string) {
	fs := flag.NewFlagSet("edge", flag.ExitOnError)
	server := fs.String("server", os.Getenv("HYGUR_EDGE_SERVER"), "central server URL (required)")
	tokenFile := fs.String("token-file", "", "device token file (or env HYGUR_EDGE_TOKEN)")
	folder := fs.String("folder", os.Getenv("HYGUR_EDGE_FOLDER"), "folder to sync (required)")
	state := fs.String("state", defaultEdgeState(), "watermark state file")
	interval := fs.Duration("interval", 0, "sync loop interval; 0 = run once and exit")
	_ = fs.Parse(args)

	if *server == "" || *folder == "" {
		edgeFatal("edge: --server and --folder are required")
	}
	token := strings.TrimSpace(os.Getenv("HYGUR_EDGE_TOKEN"))
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			edgeFatal(fmt.Sprintf("edge: read token file: %v", err))
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		edgeFatal("edge: device token required (--token-file or HYGUR_EDGE_TOKEN)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := edge.NewClient(*server, token)
	syncer := edge.NewFileSync(client, edge.TextParsers())

	runOnce := func() {
		// Offline → skip this cycle (a spool/retry queue is E6); don't advance the
		// watermark so the next online cycle re-scans.
		if err := client.Health(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "edge: server unreachable, skipping cycle: %v\n", err)
			return
		}
		since := readWatermark(*state)
		st, err := syncer.Run(ctx, *folder, since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "edge: sync error: %v\n", err)
		}
		if st.Newest.After(since) {
			writeWatermark(*state, st.Newest)
		}
		fmt.Printf("edge sync: pushed=%d skipped=%d errors=%d\n", st.Pushed, st.Skipped, st.Errors)
	}

	runOnce()
	if *interval <= 0 {
		return
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func edgeFatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func defaultEdgeState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hygur-edge-watermark"
	}
	return filepath.Join(home, ".hygur-edge", "files.watermark")
}

// readWatermark returns the persisted last-sync time, or zero if absent/invalid.
func readWatermark(path string) time.Time {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	// RFC3339Nano: second-precision would truncate the watermark below file
	// mtimes (sub-second), making unchanged files look newer → re-pushed forever.
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeWatermark(path string, t time.Time) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "edge: cannot create state dir: %v\n", err)
		return
	}
	if err := os.WriteFile(path, []byte(t.UTC().Format(time.RFC3339Nano)), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "edge: cannot write watermark: %v\n", err)
	}
}
