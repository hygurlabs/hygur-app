// Package proton provides a MailConnector implementation for Proton Mail
// via Proton Bridge using IMAP.
//
// This connector communicates with Proton Bridge, which must be running locally
// on the default port 1143. The connector uses TLS for secure communication,
// even for localhost connections.
//
// Security considerations:
//   - Credentials (username/password) are stored in memory only
//   - Credentials are NEVER logged or persisted
//   - TLS is mandatory for all connections
//   - Self-signed certificates are accepted for localhost connections only
//
// Usage:
//
//	connector := proton.NewDefaultIMAPConnector()
//	connector.SetCredentials(username, password)  // credentials from Proton Bridge
//	if err := connector.Connect(ctx); err != nil {
//	    // handle error
//	}
//	defer connector.Disconnect()
//
//	// List threads from All Mail (recommended for comprehensive results)
//	threads, err := connector.ListThreads(ctx, mail.ListOptions{
//	    MailboxID: "All Mail",
//	    Limit:     50,
//	})
//
// Error handling:
//
//	if errors.Is(err, mail.ErrConnectionLost) {
//	    // Connection was lost, caller must reconnect
//	}
//	if errors.Is(err, mail.ErrAuthFailed) {
//	    // Invalid credentials
//	}
//
// Environment variables for integration tests:
//   - PROTON_BRIDGE_USER: Proton Bridge username
//   - PROTON_BRIDGE_PASSWORD: Proton Bridge password
//   - PROTON_BRIDGE_HOST: (optional) Host, defaults to 127.0.0.1
//   - PROTON_BRIDGE_PORT: (optional) Port, defaults to 1143
package proton
