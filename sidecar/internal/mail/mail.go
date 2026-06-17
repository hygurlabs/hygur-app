// Package mail provides abstractions for email integration with various providers
// such as Proton Bridge (IMAP) and Gmail API.
package mail

import (
	"context"
	"strings"
	"time"
)

// MailConnector defines the interface for email providers.
// Implementations must be safe for concurrent use.
type MailConnector interface {
	// Connect establishes a connection to the mail server.
	// Credentials must be set via SetCredentials or SetToken before calling Connect.
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the mail server.
	// It is safe to call Disconnect on an already disconnected connector.
	Disconnect() error

	// IsConnected returns true if the connector has an active connection.
	IsConnected() bool

	// ListThreads retrieves threads matching the given options.
	// Returns an empty slice if no threads match.
	ListThreads(ctx context.Context, opts ListOptions) ([]Thread, error)

	// GetThread retrieves a single thread by its ID.
	// Returns ErrThreadNotFound if the thread does not exist.
	GetThread(ctx context.Context, threadID string) (*Thread, error)

	// GetMessages retrieves all messages within a thread.
	// Returns ErrThreadNotFound if the thread does not exist.
	GetMessages(ctx context.Context, threadID string) ([]Message, error)

	// GetMessagesByThread retrieves all messages using thread metadata.
	// This is more efficient than GetMessages as it uses UIDs directly
	// when available instead of searching by Message-ID.
	GetMessagesByThread(ctx context.Context, thread *Thread) ([]Message, error)
}

// ListOptions configures thread listing behavior.
type ListOptions struct {
	// Limit specifies the maximum number of threads to return.
	// A value of 0 means no limit (use with caution).
	Limit int

	// Offset specifies the number of threads to skip.
	// Used for pagination.
	Offset int

	// Since filters threads to those with messages on or after this time.
	// Nil means no lower bound.
	Since *time.Time

	// Before filters threads to those with messages before this time.
	// Nil means no upper bound.
	Before *time.Time

	// MailboxID specifies the mailbox to list threads from.
	// Common values: "INBOX", "Sent", "Drafts", "Trash", "Archive".
	// Empty string typically means all mailboxes or provider default.
	MailboxID string

	// LabelIDs filters threads by Gmail label IDs. When non-empty, the Gmail
	// connector uses the LabelIds() API parameter instead of buildQuery.
	// For IMAP/Proton, this field is ignored (use MailboxID).
	LabelIDs []string
}

// Thread represents an email conversation thread.
type Thread struct {
	// ID is the unique identifier for this thread.
	ID string

	// Subject is the subject line of the thread.
	Subject string

	// Participants contains email addresses of all participants.
	Participants []string

	// DateRange contains the timestamps of the oldest and newest messages.
	// DateRange[0] is the oldest, DateRange[1] is the newest.
	DateRange [2]time.Time

	// MessageCount is the total number of messages in the thread.
	MessageCount int

	// HasAttachments indicates if any message in the thread has attachments.
	HasAttachments bool

	// Labels contains provider-specific labels or tags applied to the thread.
	// For Gmail: system labels like "INBOX", "SENT", or user labels.
	// For IMAP: mailbox names or flags.
	Labels []string

	// MessageUIDs contains IMAP UIDs for messages in this thread.
	// Used for efficient message fetching without needing to search.
	// Only populated for IMAP connectors.
	MessageUIDs []uint32

	// Mailbox is the mailbox this thread was listed from.
	// Used to select the correct mailbox when fetching messages.
	Mailbox string

	// Messages contains the full message data when fetched with content.
	// Only populated when using ListThreadsWithMessages or similar.
	Messages []Message
}

// Message represents a single email message within a thread.
type Message struct {
	// ID is the unique identifier for this message.
	ID string

	// ThreadID is the identifier of the thread this message belongs to.
	ThreadID string

	// From is the email address of the sender.
	From string

	// To contains the email addresses of direct recipients.
	To []string

	// Cc contains the email addresses of carbon copy recipients.
	Cc []string

	// Date is the timestamp when the message was sent.
	Date time.Time

	// Subject is the subject line of the message.
	Subject string

	// Body contains the plain text content of the message.
	// This is the preferred format for processing.
	Body string

	// HTMLBody contains the HTML content of the message, if available.
	// May be empty if the message has no HTML part.
	HTMLBody string

	// Attachments lists the attachments in this message.
	Attachments []Attachment
}

// Attachment represents a file attached to an email message.
type Attachment struct {
	// ID is the unique identifier for this attachment.
	// Used for downloading the attachment content.
	ID string

	// Filename is the original name of the attached file.
	Filename string

	// MimeType is the MIME content type of the attachment.
	// Examples: "application/pdf", "image/png", "text/plain".
	MimeType string

	// Size is the size of the attachment in bytes.
	Size int64

	// Data holds the raw attachment bytes when the connector chose to download
	// them for indexing (text-bearing types under MaxIndexableAttachmentBytes).
	// Nil for most attachments — it is populated opportunistically so the
	// EmailIndexer can extract searchable text (e.g. a recharge total that lives
	// only inside an attached PDF).
	Data []byte
}

// MaxIndexableAttachmentBytes caps the size of an attachment whose bytes are
// downloaded for text extraction. Keeps sync bandwidth/memory bounded; larger
// attachments are indexed by metadata (filename) only.
const MaxIndexableAttachmentBytes = 10 << 20 // 10 MiB

// IsPDFAttachment reports whether an attachment is a PDF (by MIME type or
// filename), i.e. a candidate for text extraction during indexing.
func IsPDFAttachment(att Attachment) bool {
	if strings.Contains(strings.ToLower(att.MimeType), "pdf") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(att.Filename), ".pdf")
}

// CredentialSetter is an optional interface for connectors that accept
// username/password credentials (e.g., IMAP via Proton Bridge).
type CredentialSetter interface {
	// SetCredentials configures the username and password for authentication.
	// Credentials are never logged or persisted by the connector.
	SetCredentials(username, password string)
}

// TokenSetter is an optional interface for connectors that use OAuth tokens
// (e.g., Gmail API).
type TokenSetter interface {
	// SetToken configures the OAuth access token for authentication.
	// The token is never logged or persisted by the connector.
	SetToken(token string)
}

// RefreshTokenSetter is an optional interface for connectors that support
// OAuth refresh tokens for long-lived sessions.
type RefreshTokenSetter interface {
	// SetRefreshToken configures the OAuth refresh token.
	// The refresh token is used to obtain new access tokens automatically.
	// The token is never logged or persisted by the connector.
	SetRefreshToken(refreshToken string)
}

// OAuthConnector is an optional interface for connectors that support
// OAuth2 authentication flows (e.g., Gmail).
type OAuthConnector interface {
	// GetAuthURL returns the OAuth2 authorization URL for the user to visit.
	// The state parameter should be a random string to prevent CSRF attacks.
	GetAuthURL(state string) string

	// ExchangeCode exchanges an authorization code for an OAuth2 token
	// and configures the connector to use it.
	ExchangeCode(ctx context.Context, code string) error
}

// OAuthConfigurer is an optional interface for connectors that need
// OAuth credentials to be configured before generating an auth URL.
type OAuthConfigurer interface {
	// SetOAuthCredentials configures the OAuth client credentials.
	SetOAuthCredentials(clientID, clientSecret, redirectURL string)
}

// RefreshTokenGetter is an optional interface for connectors that can
// return their current refresh token for persistence.
type RefreshTokenGetter interface {
	// GetRefreshToken returns the current OAuth refresh token.
	// Returns empty string if no refresh token is available.
	GetRefreshToken() string
}

// OAuthConfigGetter is an optional interface for connectors that can
// return their OAuth configuration for persistence.
type OAuthConfigGetter interface {
	// GetOAuthConfig returns the client ID and client secret.
	GetOAuthConfig() (clientID, clientSecret string)
}

// Reconnector is an optional interface for connectors that support
// reconnection using stored credentials (e.g., OAuth refresh tokens).
type Reconnector interface {
	// Reconnect attempts to re-establish a connection using stored credentials.
	// For OAuth connectors, this typically involves using the refresh token.
	// Returns ErrInvalidCredentials if no valid credentials are available.
	Reconnect(ctx context.Context) error
}

// Label represents a mail label or folder.
type Label struct {
	// ID is the unique identifier for this label.
	ID string `json:"id"`

	// Name is the display name of the label.
	Name string `json:"name"`

	// Type indicates if this is a system or user label.
	// Values: "system", "user"
	Type string `json:"type"`
}

// LabelLister is an optional interface for connectors that can list labels.
type LabelLister interface {
	// ListLabels returns all available labels/folders.
	ListLabels(ctx context.Context) ([]Label, error)
}

// MessageIDLister is an optional interface for connectors that can cheaply
// enumerate every message currently in a mailbox by Message-ID (envelope-only,
// no bodies). It is the authoritative "present" set used by deletion
// reconciliation. serverCount is the folder's reported message count; the caller
// compares it against len(ids) as an integrity check and treats any error as
// "enumeration incomplete — do not reconcile" (fail-safe: absence is never
// inferred from a partial listing).
type MessageIDLister interface {
	ListMessageIDs(ctx context.Context, mailbox string) (ids []string, serverCount int, err error)
}
