package mail

import "errors"

// Common errors returned by mail connectors.
// These errors can be checked using errors.Is().
var (
	// ErrNotConnected is returned when an operation requires an active connection
	// but the connector is not connected.
	ErrNotConnected = errors.New("mail connector not connected")

	// ErrAuthFailed is returned when authentication to the mail server fails.
	// This may be due to invalid credentials, expired tokens, or server issues.
	ErrAuthFailed = errors.New("authentication failed")

	// ErrThreadNotFound is returned when the requested thread does not exist
	// or is not accessible.
	ErrThreadNotFound = errors.New("thread not found")

	// ErrMessageNotFound is returned when the requested message does not exist
	// or is not accessible.
	ErrMessageNotFound = errors.New("message not found")

	// ErrConnectionLost is returned when the connection to the mail server
	// is unexpectedly terminated during an operation.
	ErrConnectionLost = errors.New("connection lost")

	// ErrInvalidCredentials is returned when the provided credentials
	// are malformed or incomplete.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrMailboxNotFound is returned when the requested mailbox does not exist.
	ErrMailboxNotFound = errors.New("mailbox not found")

	// ErrRateLimited is returned when the mail provider rate limits requests.
	ErrRateLimited = errors.New("rate limited by mail provider")

	// ErrTimeout is returned when an operation times out.
	ErrTimeout = errors.New("operation timed out")

	// ErrTokenExpired is returned when the OAuth token has expired
	// and needs to be refreshed. This is a specific form of auth failure.
	ErrTokenExpired = errors.New("token expired")

	// ErrEmbeddingFailed is returned by IndexThread when embedding generation fails.
	// Callers should treat this as a transient infrastructure error, not a data error.
	ErrEmbeddingFailed = errors.New("embedding failed")

	// ErrAutomationDenied is returned when macOS denies the host process the
	// right to send Apple Events to the target application (typically Mail.app).
	// This corresponds to AppleScript error -1743.
	ErrAutomationDenied = errors.New("automation permission denied")

	// ErrMailAppNotRunning is returned when an Apple Events call requires
	// Mail.app to be running but it is not, and the connector chose not to
	// auto-launch it. Corresponds to AppleScript error -600.
	ErrMailAppNotRunning = errors.New("mail.app not running")
)
