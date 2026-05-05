package proton

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/hygur/sidecar/internal/mail"
)

// Test constants
const (
	testTimeout = 5 * time.Second
)

// Environment variables for integration tests
const (
	envProtonHost     = "PROTON_BRIDGE_HOST"
	envProtonPort     = "PROTON_BRIDGE_PORT"
	envProtonUser     = "PROTON_BRIDGE_USER"
	envProtonPassword = "PROTON_BRIDGE_PASSWORD"
)

func TestNewIMAPConnector(t *testing.T) {
	t.Run("creates connector with custom host and port", func(t *testing.T) {
		c := NewIMAPConnector("192.168.1.100", 993)

		if c.host != "192.168.1.100" {
			t.Errorf("expected host 192.168.1.100, got %s", c.host)
		}
		if c.port != 993 {
			t.Errorf("expected port 993, got %d", c.port)
		}
		if c.connected {
			t.Error("expected connector to be disconnected initially")
		}
	})

	t.Run("creates connector with default settings", func(t *testing.T) {
		c := NewDefaultIMAPConnector()

		if c.host != DefaultProtonBridgeHost {
			t.Errorf("expected host %s, got %s", DefaultProtonBridgeHost, c.host)
		}
		if c.port != DefaultProtonBridgePort {
			t.Errorf("expected port %d, got %d", DefaultProtonBridgePort, c.port)
		}
	})
}

func TestSetCredentials(t *testing.T) {
	c := NewDefaultIMAPConnector()

	c.SetCredentials("user@proton.me", "bridge-password")

	// Verify credentials are set (we can't read them directly, but we can
	// verify behavior)
	if c.username == "" || c.password == "" {
		t.Error("expected credentials to be set")
	}
}

func TestConnectWithoutCredentials(t *testing.T) {
	c := NewDefaultIMAPConnector()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	err := c.Connect(ctx)

	if !errors.Is(err, mail.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestConnectWithInvalidHost(t *testing.T) {
	c := NewIMAPConnector("invalid.host.local", 1143)
	c.SetCredentials("user", "pass")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.Connect(ctx)

	// Should fail with connection error (timeout or refused)
	if err == nil {
		c.Disconnect()
		t.Error("expected connection error, got nil")
	}
}

func TestConnectWithTimeout(t *testing.T) {
	// Use a non-routable IP to force timeout
	c := NewIMAPConnector("10.255.255.1", 1143)
	c.SetCredentials("user", "pass")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := c.Connect(ctx)

	if err == nil {
		c.Disconnect()
		t.Error("expected timeout error, got nil")
	}
}

func TestDisconnectWhenNotConnected(t *testing.T) {
	c := NewDefaultIMAPConnector()

	err := c.Disconnect()

	if err != nil {
		t.Errorf("expected no error when disconnecting while not connected, got %v", err)
	}
}

func TestIsConnected(t *testing.T) {
	c := NewDefaultIMAPConnector()

	if c.IsConnected() {
		t.Error("expected IsConnected to return false initially")
	}
}

func TestListThreadsNotConnected(t *testing.T) {
	c := NewDefaultIMAPConnector()
	ctx := context.Background()

	_, err := c.ListThreads(ctx, mail.ListOptions{})

	if !errors.Is(err, mail.ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGetThreadNotConnected(t *testing.T) {
	c := NewDefaultIMAPConnector()
	ctx := context.Background()

	_, err := c.GetThread(ctx, "thread-id")

	if !errors.Is(err, mail.ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestGetMessagesNotConnected(t *testing.T) {
	c := NewDefaultIMAPConnector()
	ctx := context.Background()

	_, err := c.GetMessages(ctx, "thread-id")

	if !errors.Is(err, mail.ErrNotConnected) {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewDefaultIMAPConnector()
	c.SetCredentials("user", "pass")

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	// Concurrent IsConnected calls
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.IsConnected()
		}()
	}

	// Concurrent SetCredentials calls
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.SetCredentials("user", "pass")
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any race condition panics (would fail the test)
	for err := range errChan {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestNormalizeMessageID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<abc@example.com>", "abc@example.com"},
		{"abc@example.com", "abc@example.com"},
		{"  <abc@example.com>  ", "abc@example.com"},
		{"", ""},
		{"<>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeMessageID(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeMessageID(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestContainsUID(t *testing.T) {
	t.Run("finds existing UID", func(t *testing.T) {
		uids := []imap.UID{1, 2, 3, 4, 5}
		if !containsUID(uids, 3) {
			t.Error("expected to find UID 3")
		}
	})

	t.Run("does not find missing UID", func(t *testing.T) {
		uids := []imap.UID{1, 2, 3, 4, 5}
		if containsUID(uids, 10) {
			t.Error("did not expect to find UID 10")
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		var uids []imap.UID
		if containsUID(uids, 1) {
			t.Error("did not expect to find UID in empty slice")
		}
	})
}

func TestIsTimeoutError(t *testing.T) {
	t.Run("nil error is not timeout", func(t *testing.T) {
		if isTimeoutError(nil) {
			t.Error("nil error should not be timeout")
		}
	})

	t.Run("regular error is not timeout", func(t *testing.T) {
		err := errors.New("some error")
		if isTimeoutError(err) {
			t.Error("regular error should not be timeout")
		}
	})

	t.Run("timeout error is detected", func(t *testing.T) {
		err := &net.OpError{
			Op:  "dial",
			Err: &timeoutError{},
		}
		if !isTimeoutError(err) {
			t.Error("timeout error should be detected")
		}
	})
}

// timeoutError is a helper for testing timeout detection.
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("some error"), false},
		{errors.New("authentication failed"), true},
		{errors.New("AUTHENTICATION FAILED"), true},
		{errors.New("invalid credentials"), true},
		{errors.New("login failed"), true},
		{errors.New("NO auth mechanisms"), true},
		{errors.New("BAD LOGIN command failed"), true},
	}

	for _, tt := range tests {
		name := "nil"
		if tt.err != nil {
			name = tt.err.Error()
		}
		t.Run(name, func(t *testing.T) {
			result := isAuthError(tt.err)
			if result != tt.expected {
				t.Errorf("isAuthError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsMailboxNotFoundError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{nil, false},
		{errors.New("some error"), false},
		{errors.New("NO such mailbox"), true},
		{errors.New("NO [NONEXISTENT] mailbox"), true},
		{errors.New("mailbox doesn't exist"), true},
		{errors.New("mailbox not found"), true},
	}

	for _, tt := range tests {
		name := "nil"
		if tt.err != nil {
			name = tt.err.Error()
		}
		t.Run(name, func(t *testing.T) {
			result := isMailboxNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("isMailboxNotFoundError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestBuildSearchCriteria(t *testing.T) {
	t.Run("empty options", func(t *testing.T) {
		opts := mail.ListOptions{}
		criteria := buildSearchCriteria(opts)

		if !criteria.Since.IsZero() {
			t.Error("expected Since to be zero")
		}
		if !criteria.Before.IsZero() {
			t.Error("expected Before to be zero")
		}
	})

	t.Run("with date filters", func(t *testing.T) {
		now := time.Now()
		yesterday := now.Add(-24 * time.Hour)

		opts := mail.ListOptions{
			Since:  &yesterday,
			Before: &now,
		}
		criteria := buildSearchCriteria(opts)

		if !criteria.Since.Equal(yesterday) {
			t.Error("expected Since to match yesterday")
		}
		if !criteria.Before.Equal(now) {
			t.Error("expected Before to match now")
		}
	})
}

func TestInterfaceCompliance(t *testing.T) {
	// Verify that IMAPConnector implements the required interfaces
	var _ mail.MailConnector = (*IMAPConnector)(nil)
	var _ mail.CredentialSetter = (*IMAPConnector)(nil)
}

// Integration tests - only run if Proton Bridge is available
func TestIntegrationConnect(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg == nil {
		t.Skip("Proton Bridge integration test skipped: environment variables not set")
	}

	c := NewIMAPConnector(cfg.host, cfg.port)
	c.SetCredentials(cfg.username, cfg.password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := c.Connect(ctx)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer c.Disconnect()

	if !c.IsConnected() {
		t.Error("expected IsConnected to return true after successful connection")
	}
}

func TestIntegrationListThreads(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg == nil {
		t.Skip("Proton Bridge integration test skipped: environment variables not set")
	}

	c := NewIMAPConnector(cfg.host, cfg.port)
	c.SetCredentials(cfg.username, cfg.password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer c.Disconnect()

	threads, err := c.ListThreads(ctx, mail.ListOptions{
		Limit:     10,
		MailboxID: "All Mail", // Following Protonmail memory note
	})
	if err != nil {
		t.Fatalf("failed to list threads: %v", err)
	}

	t.Logf("Found %d threads", len(threads))
	for i, thread := range threads {
		t.Logf("Thread %d: %s (messages: %d)", i+1, thread.Subject, thread.MessageCount)
	}
}

func TestIntegrationReconnect(t *testing.T) {
	cfg := getIntegrationConfig()
	if cfg == nil {
		t.Skip("Proton Bridge integration test skipped: environment variables not set")
	}

	c := NewIMAPConnector(cfg.host, cfg.port)
	c.SetCredentials(cfg.username, cfg.password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First connection
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	// Disconnect
	if err := c.Disconnect(); err != nil {
		t.Fatalf("failed to disconnect: %v", err)
	}

	if c.IsConnected() {
		t.Error("expected IsConnected to return false after disconnect")
	}

	// Reconnect
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("failed to reconnect: %v", err)
	}
	defer c.Disconnect()

	if !c.IsConnected() {
		t.Error("expected IsConnected to return true after reconnect")
	}
}

// integrationConfig holds credentials for integration tests.
type integrationConfig struct {
	host     string
	port     int
	username string
	password string
}

// getIntegrationConfig returns integration test config from environment variables.
// Returns nil if any required variable is missing.
func getIntegrationConfig() *integrationConfig {
	host := os.Getenv(envProtonHost)
	if host == "" {
		host = DefaultProtonBridgeHost
	}

	portStr := os.Getenv(envProtonPort)
	port := DefaultProtonBridgePort
	if portStr != "" {
		var err error
		port, err = parsePort(portStr)
		if err != nil {
			return nil
		}
	}

	username := os.Getenv(envProtonUser)
	password := os.Getenv(envProtonPassword)

	if username == "" || password == "" {
		return nil
	}

	return &integrationConfig{
		host:     host,
		port:     port,
		username: username,
		password: password,
	}
}

func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil {
		return 0, errors.New("invalid port")
	}
	if port <= 0 || port > 65535 {
		return 0, errors.New("port out of range")
	}
	return port, nil
}

func TestParseRawMIMEMessage_MultipartAlternative(t *testing.T) {
	// Typical example.test accounting email: multipart/alternative with QP-encoded HTML
	raw := "From: compta@example.test\r\n" +
		"To: user@example.test\r\n" +
		"Content-Type: multipart/alternative; boundary=\"boundary123\"\r\n" +
		"\r\n" +
		"--boundary123\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Veuillez payer par virement\r\n" +
		"Montant : 6 764,73 =E2=82=AC\r\n" +
		"IBAN : BE85 6792 0036 3806\r\n" +
		"Communication : +++ 021/8456/13106 +++\r\n" +
		"--boundary123\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<html><body><p>Veuillez payer par virement<br>6 764,73 \xe2\x82\xac</p></body></html>\r\n" +
		"--boundary123--\r\n"

	plain, html := parseRawMIMEMessage([]byte(raw))

	if !strings.Contains(plain, "6 764,73") {
		t.Errorf("expected amount in plain body, got: %q", plain)
	}
	if !strings.Contains(plain, "BE85 6792 0036 3806") {
		t.Errorf("expected IBAN in plain body, got: %q", plain)
	}
	if !strings.Contains(html, "6 764,73") {
		t.Errorf("expected amount in HTML body, got: %q", html)
	}
}

func TestParseRawMIMEMessage_PlainText(t *testing.T) {
	raw := "From: test@example.com\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Le montant à virer est de 1234 EUR.\r\n"

	plain, html := parseRawMIMEMessage([]byte(raw))
	if !strings.Contains(plain, "1234 EUR") {
		t.Errorf("expected body in plain, got: %q", plain)
	}
	if html != "" {
		t.Errorf("expected empty html, got: %q", html)
	}
}

func TestParseRawMIMEMessage_Base64HTML(t *testing.T) {
	htmlContent := "<html><body>Montant : 6 764,73 \xe2\x82\xac</body></html>"
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlContent))
	raw := "From: test@example.com\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		encoded + "\r\n"

	plain, html := parseRawMIMEMessage([]byte(raw))
	if plain != "" {
		t.Errorf("expected empty plain, got: %q", plain)
	}
	if !strings.Contains(html, "6 764,73") {
		t.Errorf("expected amount in HTML, got: %q", html)
	}
}
