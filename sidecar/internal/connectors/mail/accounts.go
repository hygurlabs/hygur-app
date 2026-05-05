package mail

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hygur/sidecar/internal/auth"
	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/mail/diag"
	"github.com/hygur/sidecar/internal/mail/gmail"
	"github.com/hygur/sidecar/internal/mail/proton"
	"github.com/hygur/sidecar/internal/plugin"
)

// verifyTTL is how long a successful VerifyAccount result is cached. Multiple
// rapid screen openings within this window reuse the cached HealthStatus
// instead of hitting the upstream provider.
const verifyTTL = 30 * time.Second

// AccountSession holds the live state of one configured mail account: the
// underlying provider connector + session, the most recent verification
// result, and the current health snapshot.
type AccountSession struct {
	AccountID   string
	Provider    string
	Email       string
	Conn        mailpkg.MailConnector
	Session     *mailpkg.Session
	Health      plugin.HealthStatus
	BriefReason diag.BriefReason
	LastVerify  time.Time
}

// AccountInfo is the public, secret-free view of a single account's state,
// suitable for serialisation to the macOS app via /mail/accounts.
type AccountInfo struct {
	AccountID    string
	Provider     string
	Email        string
	Status       string
	BriefReason  string
	LastSync     time.Time
	LastVerified time.Time
}

// ErrAccountNotFound is returned by lookups against an unknown account id.
var ErrAccountNotFound = errors.New("mail account not found")

// LoadAccountsFromStore reads the persisted MailAccount credentials and
// returns ready-to-register AccountSessions. It does NOT connect to upstream
// providers — that happens lazily on Verify/Sync. Caller is expected to
// register the returned sessions on a MailConnector via RegisterAccount().
func LoadAccountsFromStore(credStore *auth.CredentialStore) ([]*AccountSession, error) {
	if credStore == nil {
		return nil, nil
	}
	infos, err := credStore.ListMailAccounts()
	if err != nil {
		return nil, fmt.Errorf("listing mail accounts: %w", err)
	}

	sessions := make([]*AccountSession, 0, len(infos))
	for _, info := range infos {
		cred, err := credStore.GetMailAccountCredential(info.AccountID)
		if err != nil {
			continue
		}
		sess, err := buildAccountSession(cred)
		if err != nil {
			// Skip accounts that fail to materialise; record an unhealthy
			// session so the UI can still display them.
			sessions = append(sessions, &AccountSession{
				AccountID:   cred.AccountID,
				Provider:    cred.Provider,
				Email:       fallbackEmail(cred),
				Health:      plugin.HealthStatus{Status: plugin.StatusUnconfigured, Message: err.Error()},
				BriefReason: diag.ReasonNotConfigured,
			})
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// buildAccountSession instantiates the provider-specific connector for a
// stored credential. The session is wrapped to enable auto-reconnect.
func buildAccountSession(cred *auth.MailAccountCredential) (*AccountSession, error) {
	switch cred.Provider {
	case "proton":
		if cred.Username == "" || cred.Password == "" {
			return nil, fmt.Errorf("proton account %q missing username/password", cred.AccountID)
		}
		conn := proton.NewDefaultIMAPConnector()
		conn.SetCredentials(cred.Username, cred.Password)
		return &AccountSession{
			AccountID:   cred.AccountID,
			Provider:    "proton",
			Email:       fallbackEmail(cred),
			Conn:        conn,
			Health:      plugin.HealthStatus{Status: plugin.StatusUnconfigured},
			BriefReason: diag.ReasonNotConfigured,
		}, nil

	case "gmail":
		if cred.RefreshToken == "" || cred.ClientID == "" {
			return nil, fmt.Errorf("gmail account %q missing oauth credentials", cred.AccountID)
		}
		conn := gmail.NewGmailConnector(cred.ClientID, cred.ClientSecret, "urn:ietf:wg:oauth:2.0:oob")
		conn.SetRefreshToken(cred.RefreshToken)
		return &AccountSession{
			AccountID:   cred.AccountID,
			Provider:    "gmail",
			Email:       fallbackEmail(cred),
			Conn:        conn,
			Health:      plugin.HealthStatus{Status: plugin.StatusUnconfigured},
			BriefReason: diag.ReasonNotConfigured,
		}, nil

	default:
		return nil, fmt.Errorf("unknown provider %q for account %q", cred.Provider, cred.AccountID)
	}
}

func fallbackEmail(cred *auth.MailAccountCredential) string {
	if cred.Email != "" {
		return cred.Email
	}
	return cred.AccountID
}

// AccountRegistry holds the in-memory pool of configured mail accounts.
// It is safe for concurrent use.
type AccountRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*AccountSession
}

// NewAccountRegistry creates an empty registry.
func NewAccountRegistry() *AccountRegistry {
	return &AccountRegistry{sessions: make(map[string]*AccountSession)}
}

// Register adds (or replaces) a session for an account.
func (r *AccountRegistry) Register(sess *AccountSession) {
	if sess == nil || sess.AccountID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sess.AccountID] = sess
}

// Unregister removes a session.
func (r *AccountRegistry) Unregister(accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, accountID)
}

// Get returns the session for the given account id, or ErrAccountNotFound.
func (r *AccountRegistry) Get(accountID string) (*AccountSession, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[accountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	return s, nil
}

// All returns a copy of all registered sessions.
func (r *AccountRegistry) All() []*AccountSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*AccountSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// ConnectorFor returns the live MailConnector and provider name for the
// given account. Used by handlers that need to operate directly on the
// underlying provider (listing threads, fetching messages, etc.).
func (r *AccountRegistry) ConnectorFor(accountID string) (mailpkg.MailConnector, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[accountID]
	if !ok {
		return nil, "", ErrAccountNotFound
	}
	return s.Conn, s.Provider, nil
}

// Snapshot returns the secret-free public view of all accounts.
func (r *AccountRegistry) Snapshot() []AccountInfo {
	sessions := r.All()
	out := make([]AccountInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, AccountInfo{
			AccountID:    s.AccountID,
			Provider:     s.Provider,
			Email:        s.Email,
			Status:       string(s.Health.Status),
			BriefReason:  string(s.BriefReason),
			LastSync:     s.Health.LastSync,
			LastVerified: s.LastVerify,
		})
	}
	return out
}

// VerifyAccount runs an active connectivity check against the upstream
// provider for the given account, with a `verifyTTL` cache. Returns the
// resulting health status. The Connect/Reconnect plumbing on each connector
// is reused so this works for both Gmail OAuth and Proton IMAP.
func (r *AccountRegistry) VerifyAccount(ctx context.Context, accountID string) (*AccountSession, error) {
	r.mu.RLock()
	sess, ok := r.sessions[accountID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrAccountNotFound
	}

	if !sess.LastVerify.IsZero() && time.Since(sess.LastVerify) < verifyTTL && sess.BriefReason == diag.ReasonHealthy {
		return sess, nil
	}

	verifyErr := liveVerify(ctx, sess)
	r.mu.Lock()
	defer r.mu.Unlock()
	sess.LastVerify = time.Now()
	sess.BriefReason = diag.Classify(verifyErr)
	if verifyErr == nil {
		sess.Health.Status = plugin.StatusHealthy
		sess.Health.Message = ""
	} else {
		sess.Health.Status = plugin.StatusUnhealthy
		sess.Health.Message = string(sess.BriefReason)
	}
	return sess, nil
}

// liveVerify performs the lightweight, provider-specific reachability check.
// Gmail: GetProfile (single users.getProfile API call, ~50 ms typical).
// Proton: Connect() if not already connected (IMAP NOOP-equivalent).
func liveVerify(ctx context.Context, sess *AccountSession) error {
	if sess.Conn == nil {
		return fmt.Errorf("session connector not initialised")
	}

	if sess.Session != nil {
		return sess.Session.EnsureConnected(ctx)
	}

	if !sess.Conn.IsConnected() {
		if err := sess.Conn.Connect(ctx); err != nil {
			return err
		}
	}

	if g, ok := sess.Conn.(interface {
		Verify(ctx context.Context) error
	}); ok {
		return g.Verify(ctx)
	}
	return nil
}
