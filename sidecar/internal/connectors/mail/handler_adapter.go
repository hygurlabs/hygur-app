package mail

import (
	"context"

	"github.com/hygur/sidecar/internal/api/handlers"
	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/plugin"
)

// AsAccountRunner wraps a MailConnector as an AccountSyncRunner usable by
// the api/handlers MailHandler. This adapter exists so the handlers package
// stays unaware of the connector implementation, breaking what would
// otherwise be a circular import.
func AsAccountRunner(c *MailConnector) handlers.AccountSyncRunner {
	return &handlerAdapter{c: c}
}

type handlerAdapter struct {
	c *MailConnector
}

func (a *handlerAdapter) Accounts() handlers.AccountRegistrySnapshot {
	return &registryAdapter{r: a.c.accounts}
}

func (a *handlerAdapter) VerifyAccount(ctx context.Context, accountID string) (handlers.AccountSnapshot, error) {
	sess, err := a.c.VerifyAccount(ctx, accountID)
	if err != nil {
		return handlers.AccountSnapshot{}, err
	}
	return sessionToSnapshot(sess), nil
}

func (a *handlerAdapter) SyncAccount(ctx context.Context, accountID string, opts handlers.AccountSyncOptions) (*handlers.AccountSyncResult, error) {
	res, err := a.c.SyncAccount(ctx, accountID, plugin.SyncOptions{
		Mailbox: opts.Mailbox,
		Limit:   opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &handlers.AccountSyncResult{
		Processed: res.Processed,
		Skipped:   res.Skipped,
		Failed:    res.Failed,
		Duration:  res.Duration,
	}, nil
}

type registryAdapter struct {
	r *AccountRegistry
}

func (a *registryAdapter) Snapshot() []handlers.AccountSnapshot {
	src := a.r.Snapshot()
	out := make([]handlers.AccountSnapshot, 0, len(src))
	for _, s := range src {
		out = append(out, handlers.AccountSnapshot{
			AccountID:    s.AccountID,
			Provider:     s.Provider,
			Email:        s.Email,
			Status:       s.Status,
			BriefReason:  s.BriefReason,
			LastSync:     s.LastSync,
			LastVerified: s.LastVerified,
		})
	}
	return out
}

// ConnectorFor exposes the underlying provider connector for the account.
// The handler uses this to call ListThreads / GetThread directly without
// going through the unified MailConnector, preserving back-pressure on the
// original provider connection.
func (a *registryAdapter) ConnectorFor(accountID string) (mailpkg.MailConnector, string, error) {
	return a.r.ConnectorFor(accountID)
}

func sessionToSnapshot(s *AccountSession) handlers.AccountSnapshot {
	return handlers.AccountSnapshot{
		AccountID:    s.AccountID,
		Provider:     s.Provider,
		Email:        s.Email,
		Status:       string(s.Health.Status),
		BriefReason:  string(s.BriefReason),
		LastSync:     s.Health.LastSync,
		LastVerified: s.LastVerify,
	}
}
