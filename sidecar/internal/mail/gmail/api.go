// Package gmail provides a MailConnector implementation for Gmail
// using the Gmail API with OAuth2 authentication.
package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"sync"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// GmailConnector implements MailConnector for Gmail using the Gmail API.
// It uses OAuth2 for authentication and supports the standard Gmail API operations.
// This connector is safe for concurrent use.
type GmailConnector struct {
	config    *oauth2.Config
	token     *oauth2.Token // never logged
	service   *gmail.Service
	mu        sync.RWMutex
	connected bool
}

// NewGmailConnector creates a new Gmail connector with the provided OAuth2 credentials.
// The connector must have a token set via SetToken before Connect can be called.
func NewGmailConnector(clientID, clientSecret, redirectURL string) *GmailConnector {
	return &GmailConnector{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{gmail.GmailReadonlyScope}, // MINIMAL: read-only access
			Endpoint:     google.Endpoint,
		},
	}
}

// SetToken configures the OAuth2 token for authentication.
// The token is stored securely and never logged.
// This implements the TokenSetter-like behavior for OAuth2 tokens.
func (c *GmailConnector) SetToken(token *oauth2.Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// GetAuthURL returns the OAuth2 authorization URL that the user should visit
// to authorize the application. The state parameter should be a random string
// used to prevent CSRF attacks.
func (c *GmailConnector) GetAuthURL(state string) string {
	return c.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// ExchangeCode exchanges an authorization code for an OAuth2 token
// and sets the token on the connector. This implements mail.OAuthConnector.
func (c *GmailConnector) ExchangeCode(ctx context.Context, code string) error {
	token, err := c.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange authorization code: %w", err)
	}
	c.SetToken(token)
	return nil
}

// SetOAuthCredentials configures the OAuth client credentials.
// This implements mail.OAuthConfigurer and allows reconfiguring credentials
// after the connector is created.
func (c *GmailConnector) SetOAuthCredentials(clientID, clientSecret, redirectURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{gmail.GmailReadonlyScope},
		Endpoint:     google.Endpoint,
	}
}

// GetRefreshToken returns the current OAuth refresh token.
// This implements mail.RefreshTokenGetter for credential persistence.
func (c *GmailConnector) GetRefreshToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.token == nil {
		return ""
	}
	return c.token.RefreshToken
}

// GetOAuthConfig returns the client ID and client secret.
// This implements mail.OAuthConfigGetter for credential persistence.
func (c *GmailConnector) GetOAuthConfig() (clientID, clientSecret string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config == nil {
		return "", ""
	}
	return c.config.ClientID, c.config.ClientSecret
}

// SetRefreshToken sets the OAuth refresh token for restoring a session.
// This implements mail.RefreshTokenSetter for credential restoration.
func (c *GmailConnector) SetRefreshToken(refreshToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == nil {
		c.token = &oauth2.Token{}
	}
	c.token.RefreshToken = refreshToken
}

// Connect establishes a connection to the Gmail API using the configured token.
// SetToken must be called before Connect.
func (c *GmailConnector) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.connectLocked(ctx)
}

// connectLocked performs the actual connection. Caller must hold c.mu lock.
func (c *GmailConnector) connectLocked(ctx context.Context) error {
	if c.token == nil {
		return mailpkg.ErrInvalidCredentials
	}

	// If we only have a refresh token (no access token), create a minimal token
	// that will force oauth2 to use the refresh token
	if c.token.AccessToken == "" && c.token.RefreshToken != "" {
		c.token.Expiry = time.Now().Add(-time.Hour) // Mark as expired to force refresh
	}

	// Create an HTTP client with the OAuth2 token.
	// This client will automatically refresh the token if it has a RefreshToken.
	// Use context.Background() so the client outlives the HTTP request context.
	client := c.config.Client(context.Background(), c.token)

	// Create the Gmail service with background context so it persists
	service, err := gmail.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("%w: failed to create Gmail service: %v", mailpkg.ErrAuthFailed, err)
	}

	// Verify the connection by getting the user's profile
	_, err = service.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return c.mapErrorLocked(err)
	}

	c.service = service
	c.connected = true
	return nil
}

// Reconnect attempts to re-establish a connection using the stored refresh token.
// This implements mail.Reconnector for automatic reconnection on token expiry.
func (c *GmailConnector) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we have credentials to reconnect with
	if c.token == nil || c.token.RefreshToken == "" {
		return mailpkg.ErrInvalidCredentials
	}

	// Mark current connection as disconnected
	c.service = nil
	c.connected = false

	// Force token refresh by marking it as expired
	c.token.AccessToken = ""
	c.token.Expiry = time.Now().Add(-time.Hour)

	// Attempt to reconnect
	return c.connectLocked(ctx)
}

// Disconnect closes the connection to the Gmail API.
// It is safe to call Disconnect on an already disconnected connector.
func (c *GmailConnector) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.service = nil
	c.connected = false
	return nil
}

// IsConnected returns true if the connector has an active connection.
func (c *GmailConnector) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// GetProfileEmail returns the authenticated user's email address by issuing
// a single users.getProfile call. Connect() must have succeeded first.
func (c *GmailConnector) GetProfileEmail(ctx context.Context) (string, error) {
	c.mu.RLock()
	if !c.connected || c.service == nil {
		c.mu.RUnlock()
		return "", mailpkg.ErrNotConnected
	}
	service := c.service
	c.mu.RUnlock()

	profile, err := service.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return "", c.mapError(err)
	}
	return profile.EmailAddress, nil
}

// ListThreads retrieves threads matching the given options.
// When opts.LabelIDs is non-empty, one API call is made per label ID using the
// LabelIds() parameter (correct for custom Gmail labels) and results are
// deduplicated by thread ID. When opts.LabelIDs is empty, the existing
// MailboxID-based query string fallback is used.
// Returns an empty slice if no threads match.
func (c *GmailConnector) ListThreads(ctx context.Context, opts mailpkg.ListOptions) ([]mailpkg.Thread, error) {
	c.mu.RLock()
	if !c.connected || c.service == nil {
		c.mu.RUnlock()
		return nil, mailpkg.ErrNotConnected
	}
	service := c.service
	c.mu.RUnlock()

	if len(opts.LabelIDs) > 0 {
		// Label-based path: one paginated fetch per label, deduplicated by ID.
		dateQuery := c.buildDateQuery(opts)
		seen := make(map[string]struct{})
		var all []mailpkg.Thread
		for _, labelID := range opts.LabelIDs {
			labelID = strings.TrimSpace(labelID)
			if labelID == "" {
				continue
			}
			fetched, err := c.listThreadsByLabel(ctx, service, labelID, dateQuery, opts.Limit)
			if err != nil {
				return nil, err
			}
			for _, t := range fetched {
				if _, dup := seen[t.ID]; !dup {
					seen[t.ID] = struct{}{}
					all = append(all, t)
				}
				if opts.Limit > 0 && len(all) >= opts.Limit {
					break
				}
			}
			if opts.Limit > 0 && len(all) >= opts.Limit {
				break
			}
		}
		return all, nil
	}

	// Fallback path: MailboxID string query.
	query := c.buildQuery(opts)

	const pageSize = int64(100) // Gmail maximum per page
	var threads []mailpkg.Thread
	var pageToken string

	for {
		if opts.Limit > 0 && len(threads) >= opts.Limit {
			break
		}

		fetch := pageSize
		if opts.Limit > 0 {
			remaining := int64(opts.Limit - len(threads))
			if remaining < fetch {
				fetch = remaining
			}
		}

		req := service.Users.Threads.List("me").Context(ctx).MaxResults(fetch)
		if query != "" {
			req = req.Q(query)
		}
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		resp, err := req.Do()
		if err != nil {
			return nil, c.mapError(err)
		}

		for _, t := range resp.Threads {
			thread, err := c.getThreadDetails(ctx, service, t.Id)
			if err != nil {
				log.Printf("[gmail] getThreadDetails skipped thread %s: %v", t.Id, err)
				continue
			}
			threads = append(threads, *thread)
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return threads, nil
}

// listThreadsByLabel fetches all threads for a single Gmail label ID, applying
// an optional date-range query string. Results are paginated up to limit
// (0 = unlimited).
func (c *GmailConnector) listThreadsByLabel(ctx context.Context, service *gmail.Service, labelID string, dateQuery string, limit int) ([]mailpkg.Thread, error) {
	const pageSize = int64(100)
	var threads []mailpkg.Thread
	var pageToken string

	for {
		if limit > 0 && len(threads) >= limit {
			break
		}

		fetch := pageSize
		if limit > 0 {
			remaining := int64(limit - len(threads))
			if remaining < fetch {
				fetch = remaining
			}
		}

		req := service.Users.Threads.List("me").
			Context(ctx).
			MaxResults(fetch).
			LabelIds(labelID)
		if dateQuery != "" {
			req = req.Q(dateQuery)
		}
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}

		resp, err := req.Do()
		if err != nil {
			return nil, c.mapError(err)
		}

		for _, t := range resp.Threads {
			thread, err := c.getThreadDetails(ctx, service, t.Id)
			if err != nil {
				log.Printf("[gmail] getThreadDetails skipped thread %s: %v", t.Id, err)
				continue
			}
			threads = append(threads, *thread)
		}

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return threads, nil
}

// GetThread retrieves a single thread by its ID.
// Returns ErrThreadNotFound if the thread does not exist.
func (c *GmailConnector) GetThread(ctx context.Context, threadID string) (*mailpkg.Thread, error) {
	c.mu.RLock()
	if !c.connected || c.service == nil {
		c.mu.RUnlock()
		return nil, mailpkg.ErrNotConnected
	}
	service := c.service
	c.mu.RUnlock()

	return c.getThreadDetails(ctx, service, threadID)
}

// GetMessages retrieves all messages within a thread.
// Returns ErrThreadNotFound if the thread does not exist.
func (c *GmailConnector) GetMessages(ctx context.Context, threadID string) ([]mailpkg.Message, error) {
	c.mu.RLock()
	if !c.connected || c.service == nil {
		c.mu.RUnlock()
		return nil, mailpkg.ErrNotConnected
	}
	service := c.service
	c.mu.RUnlock()

	// Get the full thread with all messages
	thread, err := service.Users.Threads.Get("me", threadID).
		Format("full").
		Context(ctx).
		Do()
	if err != nil {
		return nil, c.mapError(err)
	}

	// Convert messages
	messages := make([]mailpkg.Message, 0, len(thread.Messages))
	for _, msg := range thread.Messages {
		message := c.convertMessage(msg, threadID)
		messages = append(messages, message)
	}

	return messages, nil
}

// GetMessagesByThread retrieves all messages using thread metadata.
// For Gmail, this delegates to GetMessages since Gmail uses thread IDs directly
// without the IMAP Message-ID search issues.
func (c *GmailConnector) GetMessagesByThread(ctx context.Context, thread *mailpkg.Thread) ([]mailpkg.Message, error) {
	if thread == nil {
		return nil, mailpkg.ErrThreadNotFound
	}
	return c.GetMessages(ctx, thread.ID)
}

// buildDateQuery constructs a Gmail date-range query string from ListOptions.
// Label filtering is handled via LabelIds() API parameter, not q=.
func (c *GmailConnector) buildDateQuery(opts mailpkg.ListOptions) string {
	var parts []string
	if opts.Since != nil {
		parts = append(parts, fmt.Sprintf("after:%d", opts.Since.Unix()))
	}
	if opts.Before != nil {
		parts = append(parts, fmt.Sprintf("before:%d", opts.Before.Unix()))
	}
	return strings.Join(parts, " ")
}

// buildQuery constructs a Gmail query string from ListOptions for the
// MailboxID-based fallback path (when LabelIDs is empty). It combines date
// filters with an in:<label> clause derived from MailboxID.
func (c *GmailConnector) buildQuery(opts mailpkg.ListOptions) string {
	dateQ := c.buildDateQuery(opts)
	if opts.MailboxID != "" {
		label := c.mapMailboxToLabel(opts.MailboxID)
		if dateQ != "" {
			return dateQ + " in:" + label
		}
		return "in:" + label
	}
	return dateQ
}

// mapMailboxToLabel maps common mailbox names to Gmail labels.
func (c *GmailConnector) mapMailboxToLabel(mailbox string) string {
	switch strings.ToLower(mailbox) {
	case "inbox":
		return "inbox"
	case "sent":
		return "sent"
	case "drafts":
		return "drafts"
	case "trash":
		return "trash"
	case "spam":
		return "spam"
	case "starred":
		return "starred"
	case "important":
		return "important"
	case "archive", "all mail":
		return "all"
	default:
		return mailbox
	}
}

// getThreadDetails fetches and converts a thread to our format.
func (c *GmailConnector) getThreadDetails(ctx context.Context, service *gmail.Service, threadID string) (*mailpkg.Thread, error) {
	thread, err := service.Users.Threads.Get("me", threadID).
		Format("metadata").
		MetadataHeaders("Subject", "From", "To", "Cc", "Date").
		Context(ctx).
		Do()
	if err != nil {
		return nil, c.mapError(err)
	}

	return c.convertThread(thread), nil
}

// convertThread converts a Gmail thread to our Thread type.
func (c *GmailConnector) convertThread(thread *gmail.Thread) *mailpkg.Thread {
	result := &mailpkg.Thread{
		ID:           thread.Id,
		MessageCount: len(thread.Messages),
		Labels:       []string{},
	}

	if len(thread.Messages) == 0 {
		return result
	}

	// Collect participants and find date range
	participants := make(map[string]struct{})
	var oldest, newest time.Time
	hasAttachments := false

	for i, msg := range thread.Messages {
		// Parse date
		msgDate := time.UnixMilli(msg.InternalDate)
		if i == 0 || msgDate.Before(oldest) {
			oldest = msgDate
		}
		if i == 0 || msgDate.After(newest) {
			newest = msgDate
		}

		// Extract headers
		for _, header := range msg.Payload.Headers {
			switch strings.ToLower(header.Name) {
			case "subject":
				if result.Subject == "" {
					result.Subject = header.Value
				}
			case "from":
				addr := extractEmailAddress(header.Value)
				if addr != "" {
					participants[addr] = struct{}{}
				}
			case "to", "cc":
				for _, addr := range extractEmailAddresses(header.Value) {
					participants[addr] = struct{}{}
				}
			}
		}

		// Check for attachments
		if !hasAttachments && hasAttachmentsInPart(msg.Payload) {
			hasAttachments = true
		}

		// Collect labels from first message
		if i == 0 && msg.LabelIds != nil {
			result.Labels = msg.LabelIds
		}
	}

	result.DateRange = [2]time.Time{oldest, newest}
	result.HasAttachments = hasAttachments

	// Convert participants map to slice
	for addr := range participants {
		result.Participants = append(result.Participants, addr)
	}

	return result
}

// convertMessage converts a Gmail message to our Message type.
func (c *GmailConnector) convertMessage(msg *gmail.Message, threadID string) mailpkg.Message {
	result := mailpkg.Message{
		ID:       msg.Id,
		ThreadID: threadID,
		Date:     time.UnixMilli(msg.InternalDate),
	}

	// Extract headers
	for _, header := range msg.Payload.Headers {
		switch strings.ToLower(header.Name) {
		case "subject":
			result.Subject = header.Value
		case "from":
			result.From = extractEmailAddress(header.Value)
		case "to":
			result.To = extractEmailAddresses(header.Value)
		case "cc":
			result.Cc = extractEmailAddresses(header.Value)
		}
	}

	// Extract body and attachments
	result.Body, result.HTMLBody = c.extractBody(msg.Payload)
	result.Attachments = c.extractAttachments(msg.Payload)

	return result
}

// extractBody extracts plain text and HTML body from a message payload.
func (c *GmailConnector) extractBody(payload *gmail.MessagePart) (plainText, htmlText string) {
	if payload == nil {
		return "", ""
	}

	// Handle simple message
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		plainText = decodeBase64URL(payload.Body.Data)
		return plainText, ""
	}
	if payload.MimeType == "text/html" && payload.Body != nil && payload.Body.Data != "" {
		htmlText = decodeBase64URL(payload.Body.Data)
		return "", htmlText
	}

	// Handle multipart message
	for _, part := range payload.Parts {
		if part.MimeType == "text/plain" && part.Body != nil && part.Body.Data != "" {
			plainText = decodeBase64URL(part.Body.Data)
		} else if part.MimeType == "text/html" && part.Body != nil && part.Body.Data != "" {
			htmlText = decodeBase64URL(part.Body.Data)
		} else if strings.HasPrefix(part.MimeType, "multipart/") {
			// Recursively search nested multipart
			pt, ht := c.extractBody(part)
			if plainText == "" {
				plainText = pt
			}
			if htmlText == "" {
				htmlText = ht
			}
		}
	}

	return plainText, htmlText
}

// extractAttachments extracts attachment metadata from a message payload.
func (c *GmailConnector) extractAttachments(payload *gmail.MessagePart) []mailpkg.Attachment {
	if payload == nil {
		return nil
	}

	var attachments []mailpkg.Attachment

	// Check if this part is an attachment
	if payload.Filename != "" && payload.Body != nil && payload.Body.AttachmentId != "" {
		attachments = append(attachments, mailpkg.Attachment{
			ID:       payload.Body.AttachmentId,
			Filename: payload.Filename,
			MimeType: payload.MimeType,
			Size:     payload.Body.Size,
		})
	}

	// Recursively check nested parts
	for _, part := range payload.Parts {
		attachments = append(attachments, c.extractAttachments(part)...)
	}

	return attachments
}

// mapError maps Google API errors to our error types.
// This is the public version that acquires the lock.
func (c *GmailConnector) mapError(err error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mapErrorLocked(err)
}

// mapErrorLocked maps Google API errors to our error types.
// Caller must hold c.mu lock (read or write).
func (c *GmailConnector) mapErrorLocked(err error) error {
	if err == nil {
		return nil
	}

	// Check for Google API errors
	if apiErr, ok := err.(*googleapi.Error); ok {
		switch apiErr.Code {
		case 401:
			// Check if this is a token expiration error
			if isTokenExpiredError(apiErr) {
				return fmt.Errorf("%w: %v", mailpkg.ErrTokenExpired, apiErr.Message)
			}
			return fmt.Errorf("%w: %v", mailpkg.ErrAuthFailed, apiErr.Message)
		case 403:
			// 403 can also indicate token issues (insufficient permissions after token change)
			if isTokenExpiredError(apiErr) {
				return fmt.Errorf("%w: %v", mailpkg.ErrTokenExpired, apiErr.Message)
			}
			return fmt.Errorf("%w: %v", mailpkg.ErrAuthFailed, apiErr.Message)
		case 404:
			return mailpkg.ErrThreadNotFound
		case 429:
			return mailpkg.ErrRateLimited
		case 503, 500:
			return fmt.Errorf("%w: %v", mailpkg.ErrConnectionLost, apiErr.Message)
		}
	}

	// Check for OAuth2 token errors in the error message
	errStr := err.Error()
	if strings.Contains(errStr, "token") &&
		(strings.Contains(errStr, "expired") || strings.Contains(errStr, "invalid") || strings.Contains(errStr, "revoked")) {
		return fmt.Errorf("%w: %v", mailpkg.ErrTokenExpired, err)
	}

	// Check for context errors
	if err == context.DeadlineExceeded {
		return mailpkg.ErrTimeout
	}
	if err == context.Canceled {
		return err
	}

	return err
}

// isTokenExpiredError checks if a Google API error indicates token expiration.
func isTokenExpiredError(apiErr *googleapi.Error) bool {
	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "token") &&
		(strings.Contains(msg, "expired") ||
			strings.Contains(msg, "invalid") ||
			strings.Contains(msg, "revoked"))
}

// hasAttachmentsInPart checks if a message part or its children have attachments.
func hasAttachmentsInPart(part *gmail.MessagePart) bool {
	if part == nil {
		return false
	}

	// Check if this part is an attachment
	if part.Filename != "" && part.Body != nil && part.Body.AttachmentId != "" {
		return true
	}

	// Check nested parts
	for _, p := range part.Parts {
		if hasAttachmentsInPart(p) {
			return true
		}
	}

	return false
}

// extractEmailAddress extracts a single email address from a header value.
func extractEmailAddress(value string) string {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		// Try to extract raw email
		value = strings.TrimSpace(value)
		if strings.Contains(value, "<") && strings.Contains(value, ">") {
			start := strings.Index(value, "<")
			end := strings.Index(value, ">")
			if start < end {
				return strings.TrimSpace(value[start+1 : end])
			}
		}
		// Return as-is if it looks like an email
		if strings.Contains(value, "@") {
			return value
		}
		return ""
	}
	return addr.Address
}

// extractEmailAddresses extracts multiple email addresses from a header value.
func extractEmailAddresses(value string) []string {
	addrs, err := mail.ParseAddressList(value)
	if err != nil {
		// Fall back to simple splitting
		var result []string
		for _, part := range strings.Split(value, ",") {
			addr := extractEmailAddress(part)
			if addr != "" {
				result = append(result, addr)
			}
		}
		return result
	}

	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, addr.Address)
	}
	return result
}

// decodeBase64URL decodes a base64url encoded string.
func decodeBase64URL(data string) string {
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Try standard base64
		decoded, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return ""
		}
	}
	return string(decoded)
}

// ListLabels retrieves all labels from the Gmail account.
// Returns both system labels (INBOX, SENT, etc.) and user-created labels.
func (c *GmailConnector) ListLabels(ctx context.Context) ([]mailpkg.Label, error) {
	c.mu.RLock()
	if !c.connected || c.service == nil {
		c.mu.RUnlock()
		return nil, mailpkg.ErrNotConnected
	}
	service := c.service
	c.mu.RUnlock()

	// List all labels
	resp, err := service.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, c.mapError(err)
	}

	// Convert to our Label type
	labels := make([]mailpkg.Label, 0, len(resp.Labels))
	for _, l := range resp.Labels {
		labelType := "user"
		if l.Type == "system" {
			labelType = "system"
		}
		labels = append(labels, mailpkg.Label{
			ID:   l.Id,
			Name: l.Name,
			Type: labelType,
		})
	}

	return labels, nil
}
