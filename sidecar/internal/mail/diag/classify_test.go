package diag

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
)

func TestClassify_Nil(t *testing.T) {
	if got := Classify(nil); got != ReasonHealthy {
		t.Fatalf("nil error = %q, want %q", got, ReasonHealthy)
	}
}

func TestClassify_Sentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want BriefReason
	}{
		{"auth failed", mailpkg.ErrAuthFailed, ReasonAuthIssue},
		{"token expired", mailpkg.ErrTokenExpired, ReasonAuthIssue},
		{"invalid credentials", mailpkg.ErrInvalidCredentials, ReasonAuthIssue},
		{"rate limited", mailpkg.ErrRateLimited, ReasonRateLimit},
		{"connection lost", mailpkg.ErrConnectionLost, ReasonNetworkIssue},
		{"timeout", mailpkg.ErrTimeout, ReasonNetworkIssue},
		{"not connected", mailpkg.ErrNotConnected, ReasonNetworkIssue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestClassify_WrappedSentinels(t *testing.T) {
	wrapped := fmt.Errorf("during sync: %w", mailpkg.ErrAuthFailed)
	if got := Classify(wrapped); got != ReasonAuthIssue {
		t.Fatalf("wrapped auth = %q, want %q", got, ReasonAuthIssue)
	}
}

type fakeNetErr struct{}

func (fakeNetErr) Error() string   { return "network unreachable" }
func (fakeNetErr) Timeout() bool   { return false }
func (fakeNetErr) Temporary() bool { return false }

func TestClassify_NetworkError(t *testing.T) {
	if got := Classify(fakeNetErr{}); got != ReasonNetworkIssue {
		t.Fatalf("net.Error = %q, want %q", got, ReasonNetworkIssue)
	}

	// Real net.OpError wrapping ECONNREFUSED.
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	if got := Classify(opErr); got != ReasonNetworkIssue {
		t.Fatalf("ECONNREFUSED = %q, want %q", got, ReasonNetworkIssue)
	}
}

func TestClassify_TimeoutNetError(t *testing.T) {
	// net.Error with Timeout()=true should still classify as network.
	timeoutErr := &timeoutNetError{}
	if got := Classify(timeoutErr); got != ReasonNetworkIssue {
		t.Fatalf("timeout net err = %q, want %q", got, ReasonNetworkIssue)
	}
}

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestClassify_HeuristicAuth(t *testing.T) {
	cases := []string{
		"server returned 401 Unauthorized",
		"OAuth2: invalid_grant: token revoked",
		"http 403 forbidden",
		"AUTHENTICATE failed",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if got := Classify(errors.New(msg)); got != ReasonAuthIssue {
				t.Errorf("Classify(%q) = %q, want %q", msg, got, ReasonAuthIssue)
			}
		})
	}
}

func TestClassify_Unknown(t *testing.T) {
	if got := Classify(errors.New("boom")); got != ReasonUnknown {
		t.Fatalf("unknown = %q, want %q", got, ReasonUnknown)
	}
}

// Sanity check: ensure mailpkg sentinels are still importable / not renamed.
func TestSentinelImportSanity(t *testing.T) {
	_ = time.Now()
	_ = mailpkg.ErrAuthFailed
}
