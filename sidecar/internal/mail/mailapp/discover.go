package mailapp

import (
	"context"
)

// Account is a discovered Mail.app account, suitable for materialising one
// MailAccountCredential per Mail.app UUID at first run.
type Account struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	FullName       string   `json:"fullName"`
	EmailAddresses []string `json:"emailAddresses"`
	MailboxNames   []string `json:"mailboxNames"`
}

// PrimaryEmail returns the first email address for the account, or empty
// string if none are configured.
func (a Account) PrimaryEmail() string {
	if len(a.EmailAddresses) > 0 {
		return a.EmailAddresses[0]
	}
	return ""
}

// DiscoverAccounts enumerates every Mail.app account currently configured on
// the system. The result is suitable for matching against persisted Hygur
// MailAccountCredential rows or seeding new ones.
//
// Apple Events permission must already be granted to the host process; if it
// is not, this returns mailpkg.ErrAutomationDenied via classifyOsascriptError.
func DiscoverAccounts(ctx context.Context) ([]Account, error) {
	return discoverAccountsWith(ctx, newOsascriptRunner())
}

// discoverAccountsWith is the test seam for DiscoverAccounts.
func discoverAccountsWith(ctx context.Context, r runner) ([]Account, error) {
	var out []Account
	if err := runJSON(ctx, r, scriptListAccounts, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DiscoverMailboxes returns the mailbox names exposed by a single Mail.app
// account. Note that some account types (e.g. Exchange) may return an empty
// list — for those, callers should fall back to Mail.app's local unified
// mailboxes (Mail.mailboxes()) which the connector handles transparently.
func DiscoverMailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	return discoverMailboxesWith(ctx, newOsascriptRunner(), accountID)
}

func discoverMailboxesWith(ctx context.Context, r runner, accountID string) ([]Mailbox, error) {
	var out []Mailbox
	args := map[string]any{"accountId": accountID}
	if err := runJSON(ctx, r, scriptDiscoverMailboxes, args, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Mailbox describes a Mail.app folder under a given account.
type Mailbox struct {
	Name         string `json:"name"`
	FullName     string `json:"fullName"`
	MessageCount int    `json:"messageCount"`
}
