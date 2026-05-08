// Package diag classifies low-level mail errors into stable, UI-friendly
// reason codes. The codes are intentionally short and product-facing so the
// macOS app can map them to localized strings without parsing error messages.
package diag

import (
	"errors"
	"net"
	"strings"
	"syscall"

	mailpkg "github.com/hygur/sidecar/internal/mail"
)

// BriefReason is a short, stable reason code for the connection state of a
// mail account. It is part of the public API contract — do not rename values
// without updating the macOS BriefReason enum in lockstep.
type BriefReason string

const (
	ReasonHealthy       BriefReason = "ok"
	ReasonAuthIssue     BriefReason = "auth_issue"
	ReasonNetworkIssue  BriefReason = "network_issue"
	ReasonRateLimit     BriefReason = "rate_limited"
	ReasonNotConfigured BriefReason = "not_configured"
	ReasonUnknown       BriefReason = "unknown_issue"

	// ReasonEmbeddingDown is set when the embedding service is unavailable or
	// returning dimension-mismatched vectors, causing > 50 % of a sync batch to fail.
	ReasonEmbeddingDown BriefReason = "embedding_down"

	// ReasonAutomationDenied is set when the user has not granted (or has
	// revoked) Apple Events permission for the host process to control Mail.app.
	// The macOS app surfaces this with a button to open
	// System Settings → Privacy & Security → Automation.
	ReasonAutomationDenied BriefReason = "automation_denied"

	// ReasonAppNotRunning is set when the target application (Mail.app for the
	// mailapp provider) is not running and could not be launched.
	ReasonAppNotRunning BriefReason = "app_not_running"
)

// Classify maps an error returned by a mail connector into a BriefReason.
// A nil error returns ReasonHealthy.
func Classify(err error) BriefReason {
	if err == nil {
		return ReasonHealthy
	}

	switch {
	case errors.Is(err, mailpkg.ErrAutomationDenied):
		return ReasonAutomationDenied

	case errors.Is(err, mailpkg.ErrMailAppNotRunning):
		return ReasonAppNotRunning

	case errors.Is(err, mailpkg.ErrAuthFailed),
		errors.Is(err, mailpkg.ErrTokenExpired),
		errors.Is(err, mailpkg.ErrInvalidCredentials):
		return ReasonAuthIssue

	case errors.Is(err, mailpkg.ErrRateLimited):
		return ReasonRateLimit

	case errors.Is(err, mailpkg.ErrConnectionLost),
		errors.Is(err, mailpkg.ErrTimeout),
		errors.Is(err, mailpkg.ErrNotConnected):
		return ReasonNetworkIssue
	}

	if isNetworkError(err) {
		return ReasonNetworkIssue
	}

	if looksLikeAuthError(err) {
		return ReasonAuthIssue
	}

	return ReasonUnknown
}

// isNetworkError detects transport-layer failures (DNS, TCP, TLS, timeouts)
// that the sentinel errors do not cover — typically errors bubbling up from
// the http or imap libraries.
func isNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	return false
}

// looksLikeAuthError catches OAuth/HTTP 401/403 and IMAP "AUTHENTICATE" errors
// that arrive as plain text from upstream libraries (Gmail SDK, go-imap) and
// therefore do not wrap our sentinels.
func looksLikeAuthError(err error) bool {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401"),
		strings.Contains(msg, "403"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "invalid_grant"),
		strings.Contains(msg, "invalid_token"),
		strings.Contains(msg, "authenticate failed"),
		strings.Contains(msg, "authentication failed"):
		return true
	}
	return false
}
