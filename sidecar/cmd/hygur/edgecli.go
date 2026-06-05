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
	"github.com/hygur/sidecar/internal/mail/proton"
)

// runEdge is the edge run-mode (C7 E3+E5): runs DEVICE-local sources (Files +
// Proton Bridge), extracts text locally, and pushes it to a central server with a
// device token. No KB/LLM/embeddings here. CLI-launched; Tauri spawns it (E4).
//
//	hygur edge --server https://<tenant>.hygur.ai --token-file ~/.hygur-edge/token \
//	           --folder ~/Documents \
//	           --proton --proton-user me@proton.me   (Bridge password in HYGUR_PROTON_PASSWORD)
//	           [--interval 15m]
func runEdge(args []string) {
	fs := flag.NewFlagSet("edge", flag.ExitOnError)
	server := fs.String("server", os.Getenv("HYGUR_EDGE_SERVER"), "central server URL (required)")
	tokenFile := fs.String("token-file", "", "device token file (or env HYGUR_EDGE_TOKEN)")
	stateDir := fs.String("state", defaultEdgeStateDir(), "watermark state directory")
	interval := fs.Duration("interval", 0, "sync loop interval; 0 = run once and exit")
	folder := fs.String("folder", os.Getenv("HYGUR_EDGE_FOLDER"), "folder to sync (Files source)")
	useProton := fs.Bool("proton", false, "sync Proton Bridge mail (device)")
	protonUser := fs.String("proton-user", os.Getenv("HYGUR_PROTON_USER"), "Proton Bridge username/email")
	protonMbox := fs.String("proton-mailbox", "All Mail", "Proton mailbox(es), comma-separated")
	_ = fs.Parse(args)

	if *server == "" {
		edgeFatal("edge: --server is required")
	}
	if *folder == "" && !*useProton {
		edgeFatal("edge: enable at least one source (--folder and/or --proton)")
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

	runOnce := func() {
		if err := client.Health(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "edge: server unreachable, skipping cycle: %v\n", err)
			return
		}
		if *folder != "" {
			syncFiles(ctx, client, *folder, filepath.Join(*stateDir, "files.watermark"))
		}
		if *useProton {
			syncProton(ctx, client, *protonUser, splitCSV(*protonMbox), filepath.Join(*stateDir, "proton.watermark"))
		}
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

func syncFiles(ctx context.Context, client *edge.Client, folder, wmPath string) {
	since := readWatermark(wmPath)
	st, err := edge.NewFileSync(client, edge.TextParsers()).Run(ctx, folder, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "edge files: %v\n", err)
	}
	if st.Newest.After(since) {
		writeWatermark(wmPath, st.Newest)
	}
	fmt.Printf("edge files: pushed=%d skipped=%d errors=%d\n", st.Pushed, st.Skipped, st.Errors)
}

func syncProton(ctx context.Context, client *edge.Client, user string, mailboxes []string, wmPath string) {
	pass := os.Getenv("HYGUR_PROTON_PASSWORD")
	if user == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "edge proton: --proton-user + HYGUR_PROTON_PASSWORD required, skipping")
		return
	}
	conn := proton.NewDefaultIMAPConnector() // honors PROTON_BRIDGE_HOST/PORT
	conn.SetCredentials(user, pass)
	if err := conn.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "edge proton: connect (is Proton Bridge running?): %v\n", err)
		return
	}
	defer conn.Disconnect()

	since := readWatermark(wmPath)
	st, err := edge.NewMailSync(client, "proton").Run(ctx, conn, mailboxes, since)
	if err != nil {
		fmt.Fprintf(os.Stderr, "edge proton: %v\n", err)
	}
	if st.Newest.After(since) {
		writeWatermark(wmPath, st.Newest)
	}
	fmt.Printf("edge proton: pushed=%d threads=%d errors=%d\n", st.Pushed, st.Threads, st.Errors)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func edgeFatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func defaultEdgeStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hygur-edge"
	}
	return filepath.Join(home, ".hygur-edge")
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
