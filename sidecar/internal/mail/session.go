// Package mail provides abstractions for email integration with various providers
// such as Proton Bridge (IMAP) and Gmail API.
package mail

import (
	"context"
	"sync"

	"github.com/rs/zerolog"
)

// Session wraps a MailConnector with automatic reconnection and timeout support.
// Each Session manages a single persistent connection; concurrent operations are
// serialized but multiple Sessions may exist independently.
type Session struct {
	connector   MailConnector
	reconnector Reconnector
	logger      zerolog.Logger
	mu          sync.Mutex
	connected   bool
}

// NewSession creates a new MailSession wrapping the given connector.
func NewSession(connector MailConnector, logger zerolog.Logger) *Session {
	return &Session{
		connector: connector,
		logger:    logger,
	}
}

// EnsureConnected verifies the connection is active. If not, it calls Connect
// (or Reconnect if available) to establish a fresh connection.
func (s *Session) EnsureConnected(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.connected {
		return nil
	}

	// Try reconnect first (handles OAuth token refresh)
	if s.reconnector != nil {
		if err := s.reconnector.Reconnect(ctx); err == nil {
			s.connected = true
			return nil
		}
	}

	// Fall back to initial connect
	if err := s.connector.Connect(ctx); err != nil {
		return err
	}
	s.connected = true
	return nil
}

// WithConnection executes fn on the active connector, ensuring a connection first.
// If the connector drops during use, WithConnection will reconnect automatically.
func (s *Session) With(ctx context.Context, fn func(MailConnector) error) error {
	if err := s.EnsureConnected(ctx); err != nil {
		return err
	}
	return fn(s.connector)
}

// SetReconnector sets the optional reconnector (for OAuth providers).
func (s *Session) SetReconnector(r Reconnector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconnector = r
}

// IsConnected returns true if the underlying connector reports as connected.
func (s *Session) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected && s.connector.IsConnected()
}

// Disconnect closes the connection.
func (s *Session) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	return s.connector.Disconnect()
}

// IsAuthenticated checks if the underlying connector is authenticated and connected.
func (s *Session) IsAuthenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected && s.connector.IsConnected()
}
