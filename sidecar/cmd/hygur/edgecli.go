package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/hygur/sidecar/internal/edge"
)

// runEdge is the edge run-mode (C7). Default (no --server): launch the local
// config UI (single binary, friendly setup). With --server: headless one-shot/
// loop for scripting or the Tauri auto-spawn.
//
//	hygur edge                          # config UI on 127.0.0.1, opens the browser
//	hygur edge --server https://… --token-file … --folder … --proton --proton-user …
func runEdge(args []string) {
	fs := flag.NewFlagSet("edge", flag.ExitOnError)
	server := fs.String("server", os.Getenv("HYGUR_EDGE_SERVER"), "central server URL (empty = config UI)")
	tokenFile := fs.String("token-file", "", "device token file (or env HYGUR_EDGE_TOKEN)")
	folder := fs.String("folder", os.Getenv("HYGUR_EDGE_FOLDER"), "folder to sync (Files source)")
	useProton := fs.Bool("proton", false, "sync Proton Bridge mail")
	protonUser := fs.String("proton-user", os.Getenv("HYGUR_PROTON_USER"), "Proton Bridge username/email")
	protonMbox := fs.String("proton-mailbox", "All Mail", "Proton mailbox(es), comma-separated")
	interval := fs.Duration("interval", 0, "headless loop interval; 0 = run once")
	uiAddr := fs.String("ui-addr", "127.0.0.1:7777", "config UI bind address")
	configPath := fs.String("config", edge.DefaultConfigPath(), "config file (UI mode)")
	_ = fs.Parse(args)

	if *server == "" {
		runEdgeUI(*uiAddr, *configPath)
		return
	}
	runEdgeHeadless(*server, *tokenFile, *folder, *useProton, *protonUser, *protonMbox, *interval, *configPath)
}

// runEdgeUI serves the local config UI + a background sync loop (interval from the
// saved config). The single-binary, no-flags path.
func runEdgeUI(addr, cfgPath string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runner := edge.NewRunner(cfgPath)

	// Background sync loop: honors the config's interval, re-reading it each cycle
	// so UI edits take effect. Interval 0 → idle (manual "Sync now" only). Same
	// loop the in-process server uses in cloud mode.
	go runner.RunLoop(ctx)

	url := "http://" + addr
	fmt.Printf("Hygur edge — open the config UI: %s\n", url)
	openBrowser(url)
	srv := &http.Server{Addr: addr, Handler: edge.UIHandler(runner, cfgPath), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		edgeFatal("edge ui: " + err.Error())
	}
}

// runEdgeHeadless is the flag-driven path (scripting / Tauri spawn).
func runEdgeHeadless(server, tokenFile, folder string, useProton bool, protonUser, protonMbox string, interval time.Duration, cfgPath string) {
	if folder == "" && !useProton {
		edgeFatal("edge: enable at least one source (--folder and/or --proton)")
	}
	token := strings.TrimSpace(os.Getenv("HYGUR_EDGE_TOKEN"))
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			edgeFatal(fmt.Sprintf("edge: read token file: %v", err))
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		edgeFatal("edge: device token required (--token-file or HYGUR_EDGE_TOKEN)")
	}
	cfg := &edge.Config{Server: server, Token: token, Folder: folder, ProtonMailbox: protonMbox}
	if useProton {
		cfg.ProtonUser = protonUser
		cfg.ProtonPassword = os.Getenv("HYGUR_PROTON_PASSWORD")
	}
	stateDir := filepath.Dir(cfgPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runOnce := func() {
		files, mail, errs, lastErr := edge.Sync(ctx, cfg, stateDir)
		fmt.Printf("edge sync: files=%d mail=%d errors=%d\n", files, mail, errs)
		if lastErr != "" {
			fmt.Fprintln(os.Stderr, "edge: "+lastErr)
		}
	}
	runOnce()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
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

// openBrowser best-effort opens url in the default browser (UI mode convenience).
func openBrowser(url string) {
	if os.Getenv("HYGUR_EDGE_NO_OPEN") != "" {
		return
	}
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	default:
		return
	}
	_ = exec.Command(cmd, url).Start()
}

func edgeFatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
