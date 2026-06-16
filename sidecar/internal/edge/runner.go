package edge

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/proton"
)

// Status is the last-run summary surfaced by the config UI.
type Status struct {
	Running     bool      `json:"running"`
	LastSyncAt  time.Time `json:"last_sync_at,omitempty"`
	FilesPushed int       `json:"files_pushed"`
	MailPushed  int       `json:"mail_pushed"`
	Errors      int       `json:"errors"`
	LastError   string    `json:"last_error,omitempty"`
}

// Runner executes the edge sync (Files + Proton) from the persisted config and
// tracks the latest status for the UI. Reloads the config each run so UI edits
// take effect immediately. Safe for concurrent status reads.
type Runner struct {
	cfgPath string
	mu      sync.Mutex
	status  Status
}

// NewRunner builds a Runner over a config path (~/.hygur-edge/config.json).
func NewRunner(cfgPath string) *Runner { return &Runner{cfgPath: cfgPath} }

// Status returns the latest run summary.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// RunOnce pushes Files + Proton once using the persisted config, updating Status.
func (r *Runner) RunOnce(ctx context.Context) Status {
	cfg, err := LoadConfig(r.cfgPath)
	if err != nil {
		return r.fail("config: " + err.Error())
	}
	if cfg.Server == "" || cfg.Token == "" {
		return r.fail("server URL and device token are required")
	}
	r.mu.Lock()
	r.status.Running = true
	r.status.LastError = ""
	r.mu.Unlock()

	files, mail, errs, lastErr := Sync(ctx, cfg, StateDir(r.cfgPath))
	return r.finish(files, mail, errs, lastErr)
}

// RunLoop runs the edge sync in a loop until ctx is cancelled, reloading the
// config each cycle so UI edits take effect. A configured interval (>0) sets the
// cadence; otherwise it idles (manual "Sync now" only) while still watching for
// config changes. RunOnce runs first, before the first sleep, so a configured
// loop pushes immediately. Shared by the config-UI background loop AND the
// in-process server (cloud mode) — one binary pushes local sources without a
// separate `hygur edge` process.
func (r *Runner) RunLoop(ctx context.Context) {
	for {
		cfg, _ := LoadConfig(r.cfgPath)
		if cfg.Server != "" && cfg.Token != "" && cfg.IntervalSecs > 0 {
			r.RunOnce(ctx)
			if !sleepCtx(ctx, time.Duration(cfg.IntervalSecs)*time.Second) {
				return
			}
			continue
		}
		if !sleepCtx(ctx, 10*time.Second) {
			return
		}
	}
}

// sleepCtx waits d, or returns false immediately when ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// Sync pushes Files + Proton once per cfg, persisting per-source watermarks under
// stateDir. Shared by the UI runner and the headless CLI. Never panics; the first
// hard error is returned in lastErr. (The blocking part is the network push; the
// caller decides about backgrounding.)
func Sync(ctx context.Context, cfg *Config, stateDir string) (files, mailPushed, errs int, lastErr string) {
	client := NewClient(cfg.Server, cfg.Token)
	if err := client.Health(ctx); err != nil {
		return 0, 0, 1, "server unreachable: " + err.Error()
	}
	if cfg.Folder != "" {
		wm := filepath.Join(stateDir, watermarkName("files", cfg.Server))
		st, _ := NewFileSync(client, TextParsers()).Run(ctx, cfg.Folder, ReadWatermark(wm))
		if st.Newest.After(ReadWatermark(wm)) {
			_ = WriteWatermark(wm, st.Newest)
		}
		files = st.Pushed
		errs += st.Errors
	}
	if cfg.ProtonUser != "" && cfg.ProtonPassword != "" {
		statePath := filepath.Join(stateDir, folderStateName("proton", cfg.Server))
		conn := proton.NewDefaultIMAPConnector()
		conn.SetCredentials(cfg.ProtonUser, cfg.ProtonPassword)
		if cerr := conn.Connect(ctx); cerr != nil {
			errs++
			lastErr = "proton (is Proton Bridge running?): " + cerr.Error()
		} else {
			fs := ReadFolderState(statePath)
			st, fs, _ := NewMailSync(client, "proton").Run(ctx, conn, splitMailboxes(cfg.ProtonMailbox), fs, cfg.Backfill())
			mailPushed = st.Pushed
			errs += st.Errors
			// Deletion reconciliation (cadence-gated), while the connection is open.
			if rerr := reconcileProton(ctx, client, conn, cfg, stateDir); rerr != "" {
				errs++
				if lastErr == "" {
					lastErr = rerr
				}
			}
			_ = conn.Disconnect()
			_ = WriteFolderState(statePath, fs)
		}
	}
	return files, mailPushed, errs, lastErr
}

// reconcileProton runs deletion reconciliation for Proton when it is due. It
// enumerates the messages still present on the server (envelope-only) and asks the
// center to recycle KB items that vanished. Cadence-gated by a per-destination
// stamp so it runs ~daily, not every push. An incomplete enumeration is a SILENT
// skip — never a prune on partial data; only a real transport failure returns an
// error string.
func reconcileProton(ctx context.Context, client *Client, conn mail.MailConnector, cfg *Config, stateDir string) string {
	interval, enabled := cfg.ReconcileInterval()
	if !enabled {
		return ""
	}
	stamp := filepath.Join(stateDir, reconcileStampName("proton", cfg.Server))
	if time.Since(ReadWatermark(stamp)) < interval {
		return "" // not due yet
	}
	refs, complete := NewMailSync(client, "proton").Reconcile(ctx, conn, splitMailboxes(cfg.ProtonMailbox))
	if !complete {
		return "" // partial/unsupported enumeration → skip, never infer absence
	}
	if _, err := client.Reconcile(ctx, "proton", refs, true, cfg.ReconcileGrace()); err != nil {
		return "reconcile: " + err.Error()
	}
	_ = WriteWatermark(stamp, time.Now())
	return ""
}

func (r *Runner) finish(files, mail, errs int, lastErr string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = Status{
		Running:     false,
		LastSyncAt:  time.Now(),
		FilesPushed: files,
		MailPushed:  mail,
		Errors:      errs,
		LastError:   lastErr,
	}
	return r.status
}

func (r *Runner) fail(msg string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Running = false
	r.status.LastError = msg
	return r.status
}

// watermarkName scopes the per-source watermark to the destination server, so
// re-pointing the edge at a different/fresh tenant triggers a full re-sync instead
// of being silenced by progress made against the previous instance (regression:
// switching cloud→home left the watermark past all mail, so nothing was pushed).
func watermarkName(source, server string) string {
	if host := destHost(server); host != "" {
		return source + "." + host + ".watermark"
	}
	return source + ".watermark"
}

// folderStateName is the per-folder watermark map for a source+destination
// (mail uses this instead of a single watermark, so each folder backfills and
// syncs independently).
func folderStateName(source, server string) string {
	if host := destHost(server); host != "" {
		return source + "." + host + ".folders.json"
	}
	return source + ".folders.json"
}

// reconcileStampName is the per-source+destination stamp recording when deletion
// reconciliation last ran, so the cadence survives restarts and is scoped per
// destination tenant (re-pointing the edge resets it).
func reconcileStampName(source, server string) string {
	if host := destHost(server); host != "" {
		return source + "." + host + ".reconcile"
	}
	return source + ".reconcile"
}

// destHost derives a filesystem-safe host token from the server URL.
func destHost(server string) string {
	host := strings.TrimSpace(server)
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		host = u.Host
	}
	return strings.NewReplacer(":", "_", "/", "_", "@", "_").Replace(host)
}

// Mailboxes lists the Proton mailboxes/folders available for selection in the UI
// ("Load folders"). It connects to the LOCAL Proton Bridge with the configured
// credentials — the cloud pod can't reach the Bridge, so this MUST run on-device.
func (r *Runner) Mailboxes(ctx context.Context) ([]string, error) {
	cfg, err := LoadConfig(r.cfgPath)
	if err != nil {
		return nil, err
	}
	if cfg.ProtonUser == "" || cfg.ProtonPassword == "" {
		return nil, fmt.Errorf("proton credentials not configured")
	}
	conn := proton.NewDefaultIMAPConnector()
	conn.SetCredentials(cfg.ProtonUser, cfg.ProtonPassword)
	if err := conn.Connect(ctx); err != nil {
		return nil, fmt.Errorf("proton (is Proton Bridge running?): %w", err)
	}
	defer func() { _ = conn.Disconnect() }()
	return conn.ListMailboxes(ctx)
}

func splitMailboxes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
