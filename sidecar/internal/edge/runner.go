package edge

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// Sync pushes Files + Proton once per cfg, persisting per-source watermarks under
// stateDir. Shared by the UI runner and the headless CLI. Never panics; the first
// hard error is returned in lastErr. (The blocking part is the network push; the
// caller decides about backgrounding.)
func Sync(ctx context.Context, cfg *Config, stateDir string) (files, mail, errs int, lastErr string) {
	client := NewClient(cfg.Server, cfg.Token)
	if err := client.Health(ctx); err != nil {
		return 0, 0, 1, "server unreachable: " + err.Error()
	}
	if cfg.Folder != "" {
		wm := filepath.Join(stateDir, "files.watermark")
		st, _ := NewFileSync(client, TextParsers()).Run(ctx, cfg.Folder, ReadWatermark(wm))
		if st.Newest.After(ReadWatermark(wm)) {
			_ = WriteWatermark(wm, st.Newest)
		}
		files = st.Pushed
		errs += st.Errors
	}
	if cfg.ProtonUser != "" && cfg.ProtonPassword != "" {
		wm := filepath.Join(stateDir, "proton.watermark")
		conn := proton.NewDefaultIMAPConnector()
		conn.SetCredentials(cfg.ProtonUser, cfg.ProtonPassword)
		if cerr := conn.Connect(ctx); cerr != nil {
			errs++
			lastErr = "proton (is Proton Bridge running?): " + cerr.Error()
		} else {
			st, _ := NewMailSync(client, "proton").Run(ctx, conn, splitMailboxes(cfg.ProtonMailbox), ReadWatermark(wm))
			_ = conn.Disconnect()
			if st.Newest.After(ReadWatermark(wm)) {
				_ = WriteWatermark(wm, st.Newest)
			}
			mail = st.Pushed
			errs += st.Errors
		}
	}
	return files, mail, errs, lastErr
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

func splitMailboxes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
