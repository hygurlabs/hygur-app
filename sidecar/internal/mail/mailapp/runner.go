package mailapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	mailpkg "github.com/hygur/sidecar/internal/mail"
)

// osascriptPath is the canonical path to the macOS scripting CLI. It is always
// present on darwin systems and not user-relocatable.
const osascriptPath = "/usr/bin/osascript"

// runner abstracts the underlying osascript invocation so tests can inject a
// fake without spawning real processes.
type runner interface {
	run(ctx context.Context, script string, args map[string]any) ([]byte, error)
}

// osascriptRunner is the production implementation that shells out to
// /usr/bin/osascript.
type osascriptRunner struct{}

func newOsascriptRunner() *osascriptRunner { return &osascriptRunner{} }

// run executes the given JXA script with args serialised into the
// HYGUR_JXA_ARGS env var. stdout (which carries the script's last expression)
// is returned as a JSON-encoded byte slice. stderr is parsed for known
// AppleEvents error codes and mapped onto mailpkg sentinels.
func (r *osascriptRunner) run(ctx context.Context, script string, args map[string]any) ([]byte, error) {
	var argsJSON string
	if args != nil {
		buf, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("encoding jxa args: %w", err)
		}
		argsJSON = string(buf)
	} else {
		argsJSON = "{}"
	}

	cmd := exec.CommandContext(ctx, osascriptPath, "-l", "JavaScript")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(cmd.Environ(), "HYGUR_JXA_ARGS="+argsJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, classifyOsascriptError(err, stderr.String())
	}
	return bytes.TrimRight(stdout.Bytes(), "\n"), nil
}

// classifyOsascriptError maps stderr messages from osascript onto our
// sentinel errors. macOS reports AppleEvents failures via numeric codes:
//
//	-1743 : not authorised to send Apple events to <app>
//	-600  : application is not running
//	-1712 : Apple event timed out
//
// The codes are stable across macOS versions; the surrounding text is
// localised, so we match on the numeric code rather than the message string.
func classifyOsascriptError(err error, stderr string) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %s", mailpkg.ErrTimeout, strings.TrimSpace(stderr))
	}
	switch {
	case strings.Contains(stderr, "(-1743)"):
		return fmt.Errorf("%w: %s", mailpkg.ErrAutomationDenied, strings.TrimSpace(stderr))
	case strings.Contains(stderr, "(-600)"), strings.Contains(stderr, "(-609)"):
		return fmt.Errorf("%w: %s", mailpkg.ErrMailAppNotRunning, strings.TrimSpace(stderr))
	case strings.Contains(stderr, "(-1712)"):
		return fmt.Errorf("%w: %s", mailpkg.ErrTimeout, strings.TrimSpace(stderr))
	}
	if stderr != "" {
		return fmt.Errorf("osascript: %s: %w", strings.TrimSpace(stderr), err)
	}
	return fmt.Errorf("osascript: %w", err)
}

// runJSON is a convenience that runs the script and decodes stdout into v.
func runJSON[T any](ctx context.Context, r runner, script string, args map[string]any, v *T) error {
	out, err := r.run(ctx, script, args)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return fmt.Errorf("osascript: empty output")
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("decoding jxa output: %w (raw=%q)", err, truncate(out, 200))
	}
	return nil
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
