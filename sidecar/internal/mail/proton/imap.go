// Package proton provides a MailConnector implementation for Proton Mail
// via Proton Bridge using IMAP.
package proton

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/hygur/sidecar/internal/mail"
)

// Default configuration values for Proton Bridge IMAP connection.
const (
	DefaultProtonBridgeHost = "127.0.0.1"
	DefaultProtonBridgePort = 1143
)

// IMAPConnector implements mail.MailConnector and mail.CredentialSetter
// for connecting to Proton Mail via Proton Bridge IMAP.
//
// This connector is safe for concurrent use but operations are serialized
// to avoid issues with IMAP protocol state.
type IMAPConnector struct {
	host     string
	port     int
	username string // never logged
	password string // never logged

	client    *imapclient.Client
	mu        sync.RWMutex
	connected bool
}

// Compile-time verification that IMAPConnector implements the required interfaces.
var (
	_ mail.MailConnector    = (*IMAPConnector)(nil)
	_ mail.CredentialSetter = (*IMAPConnector)(nil)
)

// NewIMAPConnector creates a new IMAP connector for Proton Bridge.
// The connector is not connected until Connect() is called.
func NewIMAPConnector(host string, port int) *IMAPConnector {
	return &IMAPConnector{
		host: host,
		port: port,
	}
}

// NewDefaultIMAPConnector creates a new IMAP connector with default
// Proton Bridge settings (localhost:1143).
func NewDefaultIMAPConnector() *IMAPConnector {
	return NewIMAPConnector(DefaultProtonBridgeHost, DefaultProtonBridgePort)
}

// SetCredentials configures the username and password for IMAP authentication.
// Credentials are stored in memory only and never logged or persisted.
// This must be called before Connect().
func (c *IMAPConnector) SetCredentials(username, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.username = username
	c.password = password
}

// Connect establishes a connection to Proton Bridge using STARTTLS and authenticates.
// Credentials must be set via SetCredentials before calling Connect.
// Port 1143 uses STARTTLS (plain connection upgraded to TLS).
// Port 993 uses direct TLS.
func (c *IMAPConnector) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	if c.username == "" || c.password == "" {
		return mail.ErrInvalidCredentials
	}

	// Proton Bridge uses self-signed certificates, so we need to skip verification
	// for localhost connections. This is safe because we're connecting to a local
	// service that we control.
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.host == "127.0.0.1" || c.host == "localhost",
		ServerName:         c.host,
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))

	// Create a dialer with timeout from context
	var dialer net.Dialer
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Timeout = time.Until(deadline)
	}

	var client *imapclient.Client
	var err error

	options := &imapclient.Options{
		TLSConfig: tlsConfig,
	}

	// Port 1143 = STARTTLS (plain then upgrade), Port 993 = direct TLS
	if c.port == 1143 {
		// Connect with plain TCP first, then upgrade via STARTTLS
		conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			if errors.Is(dialErr, context.DeadlineExceeded) || isTimeoutError(dialErr) {
				return mail.ErrTimeout
			}
			return fmt.Errorf("TCP connect to %s failed: %w", addr, dialErr)
		}

		// NewStartTLS handles the STARTTLS command and TLS upgrade
		client, err = imapclient.NewStartTLS(conn, options)
		if err != nil {
			conn.Close()
			return fmt.Errorf("STARTTLS upgrade on %s failed: %w", addr, err)
		}
	} else {
		// Direct TLS connection (port 993)
		conn, dialErr := tls.DialWithDialer(&dialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			if errors.Is(dialErr, context.DeadlineExceeded) || isTimeoutError(dialErr) {
				return mail.ErrTimeout
			}
			return fmt.Errorf("failed to connect to IMAP server: %w", dialErr)
		}

		client = imapclient.New(conn, options)
	}

	// Authenticate using LOGIN command
	if err := client.Login(c.username, c.password).Wait(); err != nil {
		client.Close()
		if isAuthError(err) {
			return mail.ErrAuthFailed
		}
		return fmt.Errorf("authentication failed: %w", err)
	}

	c.client = client
	c.connected = true
	return nil
}

// Disconnect closes the connection to the IMAP server.
// It is safe to call Disconnect on an already disconnected connector.
func (c *IMAPConnector) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		c.connected = false
		return nil
	}

	// Try to logout gracefully, but don't fail if it doesn't work
	_ = c.client.Logout().Wait()

	if err := c.client.Close(); err != nil {
		c.client = nil
		c.connected = false
		return fmt.Errorf("failed to close connection: %w", err)
	}

	c.client = nil
	c.connected = false
	return nil
}

// IsConnected returns true if the connector has an active connection.
func (c *IMAPConnector) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// ListMailboxes returns all available mailboxes (for debugging).
func (c *IMAPConnector) ListMailboxes(ctx context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, mail.ErrNotConnected
	}

	listCmd := c.client.List("", "*", nil)
	mailboxes := []string{}

	for {
		data := listCmd.Next()
		if data == nil {
			break
		}
		mailboxes = append(mailboxes, data.Mailbox)
	}

	if err := listCmd.Close(); err != nil {
		return mailboxes, fmt.Errorf("list mailboxes failed: %w", err)
	}

	return mailboxes, nil
}

// ListLabels returns all available mailboxes as labels.
// For IMAP, mailboxes are treated as labels with type "system" for standard
// mailboxes and "user" for custom folders.
func (c *IMAPConnector) ListLabels(ctx context.Context) ([]mail.Label, error) {
	mailboxes, err := c.ListMailboxes(ctx)
	if err != nil {
		return nil, err
	}

	// Standard IMAP mailboxes that should be marked as system
	systemMailboxes := map[string]bool{
		"INBOX":     true,
		"Sent":      true,
		"Drafts":    true,
		"Trash":     true,
		"Spam":      true,
		"Archive":   true,
		"All Mail":  true,
		"Starred":   true,
		"Important": true,
	}

	labels := make([]mail.Label, 0, len(mailboxes))
	for _, mb := range mailboxes {
		labelType := "user"
		if systemMailboxes[mb] {
			labelType = "system"
		}
		labels = append(labels, mail.Label{
			ID:   mb,
			Name: mb,
			Type: labelType,
		})
	}

	return labels, nil
}

// ListThreads retrieves email threads matching the given options.
// For Proton Bridge, threads are reconstructed from messages with matching
// References/In-Reply-To headers.
//
// Important: Use "All Mail" as MailboxID for comprehensive search results.
func (c *IMAPConnector) ListThreads(ctx context.Context, opts mail.ListOptions) ([]mail.Thread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, mail.ErrNotConnected
	}

	// Select the mailbox (default to INBOX if not specified)
	mailbox := opts.MailboxID
	if mailbox == "" {
		mailbox = "INBOX"
	}

	selectCmd := c.client.Select(mailbox, nil)
	selectData, err := selectCmd.Wait()
	if err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
		if isMailboxNotFoundError(err) {
			return nil, mail.ErrMailboxNotFound
		}
		return nil, fmt.Errorf("failed to select mailbox %q: %w", mailbox, err)
	}

	if selectData.NumMessages == 0 {
		return []mail.Thread{}, nil
	}

	var uids []imap.UID

	// If no date filters, fetch recent messages directly instead of searching
	if shouldFetchAll(opts) {
		// Fetch the most recent messages (up to limit)
		numToFetch := uint32(opts.Limit)
		if numToFetch == 0 || numToFetch > selectData.NumMessages {
			numToFetch = selectData.NumMessages
		}

		// Calculate the range: fetch from (total - numToFetch + 1) to total
		start := selectData.NumMessages - numToFetch + 1
		if start < 1 {
			start = 1
		}

		// Use sequence numbers to get UIDs
		seqSet := new(imap.SeqSet)
		seqSet.AddRange(start, selectData.NumMessages)

		fetchCmd := c.client.Fetch(*seqSet, &imap.FetchOptions{UID: true})
		for {
			if checkContext(ctx) {
				_ = fetchCmd.Close()
				return nil, ctx.Err()
			}
			msg := fetchCmd.Next()
			if msg == nil {
				break
			}
			msgData, err := msg.Collect()
			if err != nil {
				continue
			}
			uids = append(uids, msgData.UID)
		}
		if err := fetchCmd.Close(); err != nil {
			return nil, fmt.Errorf("fetch UIDs failed: %w", err)
		}
	} else {
		// Build search criteria based on options
		searchCriteria := buildSearchCriteria(opts)

		// Search for messages
		searchCmd := c.client.Search(searchCriteria, nil)
		searchData, err := searchCmd.Wait()
		if err != nil {
			if c.isConnectionLost(err) {
				c.markDisconnected()
				return nil, mail.ErrConnectionLost
			}
			return nil, fmt.Errorf("search failed: %w", err)
		}
		uids = searchData.AllUIDs()
	}

	if len(uids) == 0 {
		return []mail.Thread{}, nil
	}

	// Fetch envelope and flags for each message
	seqSet := imap.UIDSetNum(uids...)
	fetchOptions := &imap.FetchOptions{
		Envelope:    true,
		Flags:       true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{},
	}

	fetchCmd := c.client.Fetch(seqSet, fetchOptions)

	// Collect messages and group into threads
	threadMap := make(map[string]*threadBuilder)

	for {
		if checkContext(ctx) {
			_ = fetchCmd.Close()
			return nil, ctx.Err()
		}
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		msgData, err := msg.Collect()
		if err != nil {
			continue
		}

		env := msgData.Envelope
		if env == nil {
			continue
		}

		// Determine thread ID from Message-ID and References/In-Reply-To
		threadID := extractThreadID(env)

		tb, exists := threadMap[threadID]
		if !exists {
			tb = &threadBuilder{
				id:      threadID,
				subject: env.Subject,
			}
			threadMap[threadID] = tb
		}

		// Add message info to thread
		tb.addMessage(msgData)
	}

	if err := fetchCmd.Close(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
	}

	// Convert thread builders to Thread structs
	threads := make([]mail.Thread, 0, len(threadMap))
	for _, tb := range threadMap {
		threads = append(threads, tb.build(mailbox))
	}

	// Sort by newest message date (descending)
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].DateRange[1].After(threads[j].DateRange[1])
	})

	// Apply offset and limit
	if opts.Offset > 0 {
		if opts.Offset >= len(threads) {
			return []mail.Thread{}, nil
		}
		threads = threads[opts.Offset:]
	}

	if opts.Limit > 0 && opts.Limit < len(threads) {
		threads = threads[:opts.Limit]
	}

	return threads, nil
}

// GetThread retrieves a single thread by its ID.
// The thread ID is derived from the Message-ID of the first message in the thread.
func (c *IMAPConnector) GetThread(ctx context.Context, threadID string) (*mail.Thread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, mail.ErrNotConnected
	}

	// Select All Mail to find all related messages
	selectCmd := c.client.Select("All Mail", nil)
	if _, err := selectCmd.Wait(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
		// Fallback to INBOX if All Mail doesn't exist
		selectCmd = c.client.Select("INBOX", nil)
		if _, err := selectCmd.Wait(); err != nil {
			if c.isConnectionLost(err) {
				c.markDisconnected()
				return nil, mail.ErrConnectionLost
			}
			return nil, fmt.Errorf("failed to select mailbox: %w", err)
		}
	}

	// Search for the message by Message-ID
	searchCriteria := &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{
			{Key: "Message-ID", Value: threadID},
		},
	}

	searchCmd := c.client.Search(searchCriteria, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
		return nil, fmt.Errorf("search failed: %w", err)
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		// Also try searching in References headers
		searchCriteria = &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "References", Value: threadID},
			},
		}
		searchCmd = c.client.Search(searchCriteria, nil)
		searchData, err = searchCmd.Wait()
		if err != nil {
			if c.isConnectionLost(err) {
				c.markDisconnected()
				return nil, mail.ErrConnectionLost
			}
			return nil, fmt.Errorf("search failed: %w", err)
		}
		uids = searchData.AllUIDs()
	}

	if len(uids) == 0 {
		return nil, mail.ErrThreadNotFound
	}

	// Fetch messages
	seqSet := imap.UIDSetNum(uids...)
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
	}

	fetchCmd := c.client.Fetch(seqSet, fetchOptions)

	tb := &threadBuilder{
		id: threadID,
	}

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		msgData, err := msg.Collect()
		if err != nil {
			continue
		}

		if msgData.Envelope != nil {
			if tb.subject == "" {
				tb.subject = msgData.Envelope.Subject
			}
			tb.addMessage(msgData)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
	}

	if tb.messageCount == 0 {
		return nil, mail.ErrThreadNotFound
	}

	thread := tb.build("INBOX") // Default to INBOX for GetThread
	return &thread, nil
}

// GetMessages retrieves all messages within a thread.
// Returns messages sorted by date (oldest first).
func (c *IMAPConnector) GetMessages(ctx context.Context, threadID string) ([]mail.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, mail.ErrNotConnected
	}

	// Select INBOX first (most common case), then try All Mail if no results
	mailboxes := []string{"INBOX", "All Mail"}
	var selectedMailbox string

	for _, mailbox := range mailboxes {
		selectCmd := c.client.Select(mailbox, nil)
		if _, err := selectCmd.Wait(); err != nil {
			if c.isConnectionLost(err) {
				c.markDisconnected()
				return nil, mail.ErrConnectionLost
			}
			continue // Try next mailbox
		}
		selectedMailbox = mailbox
		break
	}

	if selectedMailbox == "" {
		return nil, fmt.Errorf("failed to select any mailbox")
	}

	// Search for messages in this thread
	uids, err := c.findThreadMessages(threadID)
	if err != nil {
		return nil, err
	}

	if len(uids) == 0 {
		return nil, mail.ErrThreadNotFound
	}

	// Fetch full message bodies
	seqSet := imap.UIDSetNum(uids...)
	bodySection := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierText,
	}
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			bodySection,
			{}, // Full body
		},
	}

	fetchCmd := c.client.Fetch(seqSet, fetchOptions)

	var messages []mail.Message

	for {
		if checkContext(ctx) {
			_ = fetchCmd.Close()
			return nil, ctx.Err()
		}
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		msgData, err := msg.Collect()
		if err != nil {
			continue
		}

		mailMsg := convertToMailMessage(msgData, threadID)
		if mailMsg != nil {
			messages = append(messages, *mailMsg)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
	}

	// Sort by date (oldest first)
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Date.Before(messages[j].Date)
	})

	return messages, nil
}

// findThreadMessages searches for all messages belonging to a thread.
func (c *IMAPConnector) findThreadMessages(threadID string) ([]imap.UID, error) {
	var uids []imap.UID

	// Thread IDs are stored without angle brackets, but IMAP headers include them.
	// Search for both versions to maximize matches.
	searchValues := []string{threadID}
	if !strings.HasPrefix(threadID, "<") {
		searchValues = append(searchValues, "<"+threadID+">")
	}

	// Search by Message-ID (both with and without brackets)
	for _, searchValue := range searchValues {
		searchCriteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "Message-ID", Value: searchValue},
			},
		}

		searchCmd := c.client.Search(searchCriteria, nil)
		searchData, err := searchCmd.Wait()
		if err != nil {
			if c.isConnectionLost(err) {
				c.markDisconnected()
				return nil, mail.ErrConnectionLost
			}
			continue // Try next search value
		}

		for _, uid := range searchData.AllUIDs() {
			if !containsUID(uids, uid) {
				uids = append(uids, uid)
			}
		}
	}

	// Also search in References
	for _, searchValue := range searchValues {
		searchCriteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "References", Value: searchValue},
			},
		}

		searchCmd := c.client.Search(searchCriteria, nil)
		searchData, err := searchCmd.Wait()
		if err == nil {
			for _, uid := range searchData.AllUIDs() {
				if !containsUID(uids, uid) {
					uids = append(uids, uid)
				}
			}
		}
	}

	// Also search in In-Reply-To
	for _, searchValue := range searchValues {
		searchCriteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "In-Reply-To", Value: searchValue},
			},
		}

		searchCmd := c.client.Search(searchCriteria, nil)
		searchData, err := searchCmd.Wait()
		if err == nil {
			for _, uid := range searchData.AllUIDs() {
				if !containsUID(uids, uid) {
					uids = append(uids, uid)
				}
			}
		}
	}

	return uids, nil
}

// GetMessagesByThread retrieves messages using thread UIDs directly.
// This is more reliable than searching by Message-ID.
func (c *IMAPConnector) GetMessagesByThread(ctx context.Context, thread *mail.Thread) ([]mail.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.client == nil {
		return nil, mail.ErrNotConnected
	}

	// If thread has UIDs, use them directly (most reliable)
	if len(thread.MessageUIDs) > 0 && thread.Mailbox != "" {
		return c.fetchMessagesByUIDs(ctx, thread.Mailbox, thread.MessageUIDs, thread.ID)
	}

	// Fall back to search by Message-ID
	c.mu.Unlock()
	messages, err := c.GetMessages(ctx, thread.ID)
	c.mu.Lock()
	return messages, err
}

// fetchMessagesByUIDs fetches messages by their UIDs from a specific mailbox.
func (c *IMAPConnector) fetchMessagesByUIDs(ctx context.Context, mailbox string, uids []uint32, threadID string) ([]mail.Message, error) {
	// Select the mailbox
	selectCmd := c.client.Select(mailbox, nil)
	if _, err := selectCmd.Wait(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
		return nil, fmt.Errorf("failed to select mailbox %q: %w", mailbox, err)
	}

	// Convert uint32 UIDs to imap.UID
	imapUIDs := make([]imap.UID, len(uids))
	for i, uid := range uids {
		imapUIDs[i] = imap.UID(uid)
	}

	// Fetch full message bodies
	seqSet := imap.UIDSetNum(imapUIDs...)
	bodySection := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierText,
	}
	fetchOptions := &imap.FetchOptions{
		Envelope: true,
		Flags:    true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			bodySection,
			{}, // Full body
		},
	}

	fetchCmd := c.client.Fetch(seqSet, fetchOptions)

	var messages []mail.Message

	for {
		if checkContext(ctx) {
			_ = fetchCmd.Close()
			return nil, ctx.Err()
		}
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		msgData, err := msg.Collect()
		if err != nil {
			continue
		}

		mailMsg := convertToMailMessage(msgData, threadID)
		if mailMsg != nil {
			messages = append(messages, *mailMsg)
		}
	}

	if err := fetchCmd.Close(); err != nil {
		if c.isConnectionLost(err) {
			c.markDisconnected()
			return nil, mail.ErrConnectionLost
		}
	}

	// Sort by date (oldest first)
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Date.Before(messages[j].Date)
	})

	return messages, nil
}

// markDisconnected marks the connector as disconnected (must be called with lock held).
func (c *IMAPConnector) markDisconnected() {
	c.connected = false
	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
}

// checkContext checks if the context has been cancelled or timed out.
// Returns true if the context is done, false otherwise.
func checkContext(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// isConnectionLost checks if an error indicates the connection was lost.
func (c *IMAPConnector) isConnectionLost(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "eof") ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

// threadBuilder accumulates message data to build a Thread.
type threadBuilder struct {
	id             string
	subject        string
	participants   map[string]struct{}
	oldestDate     time.Time
	newestDate     time.Time
	messageCount   int
	hasAttachments bool
	labels         map[string]struct{}
	messageUIDs    []uint32
}

func (tb *threadBuilder) addMessage(msg *imapclient.FetchMessageBuffer) {
	if tb.participants == nil {
		tb.participants = make(map[string]struct{})
	}
	if tb.labels == nil {
		tb.labels = make(map[string]struct{})
	}

	// Store the UID for later use
	tb.messageUIDs = append(tb.messageUIDs, uint32(msg.UID))

	env := msg.Envelope
	if env == nil {
		return
	}

	// Add participants
	for _, addr := range env.From {
		if addr.Addr() != "" {
			tb.participants[addr.Addr()] = struct{}{}
		}
	}
	for _, addr := range env.To {
		if addr.Addr() != "" {
			tb.participants[addr.Addr()] = struct{}{}
		}
	}
	for _, addr := range env.Cc {
		if addr.Addr() != "" {
			tb.participants[addr.Addr()] = struct{}{}
		}
	}

	// Update date range
	if !env.Date.IsZero() {
		if tb.oldestDate.IsZero() || env.Date.Before(tb.oldestDate) {
			tb.oldestDate = env.Date
		}
		if tb.newestDate.IsZero() || env.Date.After(tb.newestDate) {
			tb.newestDate = env.Date
		}
	}

	tb.messageCount++

	// Check flags for labels
	for _, flag := range msg.Flags {
		tb.labels[string(flag)] = struct{}{}
	}
}

func (tb *threadBuilder) build(mailbox string) mail.Thread {
	participants := make([]string, 0, len(tb.participants))
	for p := range tb.participants {
		participants = append(participants, p)
	}
	sort.Strings(participants)

	labels := make([]string, 0, len(tb.labels))
	for l := range tb.labels {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	return mail.Thread{
		ID:             tb.id,
		Subject:        tb.subject,
		Participants:   participants,
		DateRange:      [2]time.Time{tb.oldestDate, tb.newestDate},
		MessageCount:   tb.messageCount,
		HasAttachments: tb.hasAttachments,
		Labels:         labels,
		MessageUIDs:    tb.messageUIDs,
		Mailbox:        mailbox,
	}
}

// convertToMailMessage converts IMAP message data to a mail.Message.
func convertToMailMessage(msg *imapclient.FetchMessageBuffer, threadID string) *mail.Message {
	env := msg.Envelope
	if env == nil {
		return nil
	}

	var from string
	if len(env.From) > 0 {
		from = env.From[0].Addr()
	}

	to := make([]string, 0, len(env.To))
	for _, addr := range env.To {
		to = append(to, addr.Addr())
	}

	cc := make([]string, 0, len(env.Cc))
	for _, addr := range env.Cc {
		cc = append(cc, addr.Addr())
	}

	// Extract body: prefer the full raw message (BODY[]) for proper MIME parsing.
	// BODY[TEXT] contains raw multipart MIME for multi-part messages, which the
	// normalizer would discard as MIME garbage without setting HTMLBody.
	var plainBody, htmlBody string
	var textFallback string
	for _, section := range msg.BodySection {
		if len(section.Bytes) == 0 {
			continue
		}
		isFullBody := section.Section == nil ||
			(section.Section.Specifier == "" && len(section.Section.Part) == 0)
		if isFullBody {
			plainBody, htmlBody = parseRawMIMEMessage(section.Bytes)
			break
		}
		if textFallback == "" {
			textFallback = string(section.Bytes)
		}
	}
	// If full-body parse yielded nothing, use raw BODY[TEXT] as plain text.
	if plainBody == "" && htmlBody == "" {
		plainBody = textFallback
	}

	return &mail.Message{
		ID:       env.MessageID,
		ThreadID: threadID,
		From:     from,
		To:       to,
		Cc:       cc,
		Date:     env.Date,
		Subject:  env.Subject,
		Body:     plainBody,
		HTMLBody: htmlBody,
	}
}

// buildSearchCriteria converts ListOptions to IMAP search criteria.
func buildSearchCriteria(opts mail.ListOptions) *imap.SearchCriteria {
	criteria := &imap.SearchCriteria{}

	if opts.Since != nil {
		criteria.Since = *opts.Since
	}

	if opts.Before != nil {
		criteria.Before = *opts.Before
	}

	// If no criteria specified, search for all messages
	// by not setting any filter (empty criteria = ALL in IMAP)
	return criteria
}

// shouldFetchAll returns true if we should fetch all messages instead of searching.
func shouldFetchAll(opts mail.ListOptions) bool {
	return opts.Since == nil && opts.Before == nil
}

// extractThreadID extracts a thread identifier from the envelope.
// Uses the Message-ID of the first message in the thread chain.
func extractThreadID(env *imap.Envelope) string {
	// If there are references, use the first one as the thread root
	// Otherwise, use this message's ID
	if len(env.InReplyTo) > 0 && env.InReplyTo[0] != "" {
		return normalizeMessageID(env.InReplyTo[0])
	}
	return normalizeMessageID(env.MessageID)
}

// normalizeMessageID cleans up a Message-ID for use as a thread identifier.
func normalizeMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}

// containsUID checks if a UID is present in a slice.
func containsUID(uids []imap.UID, uid imap.UID) bool {
	for _, u := range uids {
		if u == uid {
			return true
		}
	}
	return false
}

// isTimeoutError checks if an error is a timeout error.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// isAuthError checks if an error is an authentication error.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "authentication failed") ||
		strings.Contains(errStr, "invalid credentials") ||
		strings.Contains(errStr, "login failed") ||
		strings.Contains(errStr, "no auth") ||
		strings.Contains(errStr, "bad") && strings.Contains(errStr, "login")
}

// isMailboxNotFoundError checks if an error indicates a mailbox doesn't exist.
func isMailboxNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "no such mailbox") ||
		strings.Contains(errStr, "mailbox doesn't exist") ||
		strings.Contains(errStr, "mailbox not found") ||
		strings.Contains(errStr, "nonexistent")
}

// parseRawMIMEMessage parses a full raw email message (headers + body) and
// returns the text/plain and text/html parts. Handles multipart messages and
// common content-transfer-encodings (quoted-printable, base64).
func parseRawMIMEMessage(raw []byte) (plainText, htmlText string) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		b, _ := io.ReadAll(msg.Body)
		return string(b), ""
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		b, _ := io.ReadAll(msg.Body)
		return string(b), ""
	}
	cte := msg.Header.Get("Content-Transfer-Encoding")
	return extractMIMEParts(mediaType, params, cte, msg.Body)
}

// extractMIMEParts recurses into a MIME part and collects the first
// text/plain and text/html content found.
func extractMIMEParts(mediaType string, params map[string]string, cte string, body io.Reader) (plainText, htmlText string) {
	decoded := decodeMIMEPart(body, cte)

	switch {
	case mediaType == "text/plain":
		return string(decoded), ""
	case mediaType == "text/html":
		return "", string(decoded)
	case strings.HasPrefix(mediaType, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return "", ""
		}
		mr := multipart.NewReader(bytes.NewReader(decoded), boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			partCT := part.Header.Get("Content-Type")
			partCTE := part.Header.Get("Content-Transfer-Encoding")
			partMedia, partParams, err := mime.ParseMediaType(partCT)
			if err != nil {
				continue
			}
			pt, ht := extractMIMEParts(partMedia, partParams, partCTE, part)
			if plainText == "" && pt != "" {
				plainText = pt
			}
			if htmlText == "" && ht != "" {
				htmlText = ht
			}
		}
	}
	return
}

// decodeMIMEPart applies content-transfer-encoding decoding.
func decodeMIMEPart(r io.Reader, cte string) []byte {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		b, _ := io.ReadAll(quotedprintable.NewReader(r))
		return b
	case "base64":
		raw, _ := io.ReadAll(r)
		clean := strings.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
				return -1
			}
			return r
		}, string(raw))
		b, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			// Try RawStdEncoding (no padding)
			b, _ = base64.RawStdEncoding.DecodeString(clean)
		}
		return b
	default:
		b, _ := io.ReadAll(r)
		return b
	}
}
