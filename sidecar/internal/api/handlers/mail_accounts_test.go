package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/mail"
)

// fakeRunner implements AccountSyncRunner for handler tests.
type fakeRunner struct {
	snapshots  []AccountSnapshot
	verifyErr  error
	verified   AccountSnapshot
	connectors map[string]mail.MailConnector // optional: enables ConnectorFor
}

func (f *fakeRunner) Accounts() AccountRegistrySnapshot {
	return &fakeRegSnap{snaps: f.snapshots, connectors: f.connectors}
}

func (f *fakeRunner) VerifyAccount(_ context.Context, accountID string) (AccountSnapshot, error) {
	if f.verifyErr != nil {
		return AccountSnapshot{}, f.verifyErr
	}
	return f.verified, nil
}

func (f *fakeRunner) SyncAccount(_ context.Context, _ string, _ AccountSyncOptions) (*AccountSyncResult, error) {
	return &AccountSyncResult{Processed: 1}, nil
}

type fakeRegSnap struct {
	snaps      []AccountSnapshot
	connectors map[string]mail.MailConnector
}

func (s *fakeRegSnap) Snapshot() []AccountSnapshot { return s.snaps }

// ConnectorFor satisfies accountConnectorFor, enabling the labels/mailboxes handlers.
func (s *fakeRegSnap) ConnectorFor(accountID string) (mail.MailConnector, string, error) {
	if s.connectors != nil {
		if c, ok := s.connectors[accountID]; ok {
			return c, "gmail", nil
		}
	}
	return nil, "", errors.New("mail account not found")
}

// fakeCounts implements AccountCounts for handler tests.
type fakeCounts struct {
	counts map[string]int64
}

func (f *fakeCounts) CountMailItemsByAccount(_ context.Context, accountID, _ string) (int64, time.Time, error) {
	return f.counts[accountID], time.Time{}, nil
}

func newAccountsRouter(h *MailHandler) chi.Router {
	r := chi.NewRouter()
	r.Get("/mail/accounts", h.Accounts)
	r.Post("/mail/accounts/{account_id}/verify", h.VerifyAccount)
	r.Get("/mail/accounts/{account_id}/stats", h.AccountStats)
	r.Get("/mail/accounts/{account_id}/labels", h.AccountLabels)
	r.Get("/mail/accounts/{account_id}/mailboxes", h.AccountMailboxes)
	return r
}

// fakeLabelConn is a minimal MailConnector + LabelLister used in tests.
type fakeLabelConn struct {
	labels    []mail.Label
	labelsErr error
}

func (c *fakeLabelConn) Connect(_ context.Context) error { return nil }
func (c *fakeLabelConn) Disconnect() error               { return nil }
func (c *fakeLabelConn) IsConnected() bool               { return true }
func (c *fakeLabelConn) ListThreads(_ context.Context, _ mail.ListOptions) ([]mail.Thread, error) {
	return nil, nil
}
func (c *fakeLabelConn) GetThread(_ context.Context, _ string) (*mail.Thread, error) { return nil, nil }
func (c *fakeLabelConn) GetMessages(_ context.Context, _ string) ([]mail.Message, error) {
	return nil, nil
}
func (c *fakeLabelConn) GetMessagesByThread(_ context.Context, _ *mail.Thread) ([]mail.Message, error) {
	return nil, nil
}
func (c *fakeLabelConn) ListLabels(_ context.Context) ([]mail.Label, error) {
	return c.labels, c.labelsErr
}

// fakeMailboxConn is a minimal MailConnector + MailboxLister used in tests.
type fakeMailboxConn struct {
	mailboxes    []string
	mailboxesErr error
}

func (c *fakeMailboxConn) Connect(_ context.Context) error { return nil }
func (c *fakeMailboxConn) Disconnect() error               { return nil }
func (c *fakeMailboxConn) IsConnected() bool               { return true }
func (c *fakeMailboxConn) ListThreads(_ context.Context, _ mail.ListOptions) ([]mail.Thread, error) {
	return nil, nil
}
func (c *fakeMailboxConn) GetThread(_ context.Context, _ string) (*mail.Thread, error) {
	return nil, nil
}
func (c *fakeMailboxConn) GetMessages(_ context.Context, _ string) ([]mail.Message, error) {
	return nil, nil
}
func (c *fakeMailboxConn) GetMessagesByThread(_ context.Context, _ *mail.Thread) ([]mail.Message, error) {
	return nil, nil
}
func (c *fakeMailboxConn) ListMailboxes(_ context.Context) ([]string, error) {
	return c.mailboxes, c.mailboxesErr
}

func TestMailHandler_Accounts_EmptyWhenNoRunner(t *testing.T) {
	h := NewMailHandler(testLogger())
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp AccountsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Accounts) != 0 {
		t.Errorf("want empty accounts, got %d", len(resp.Accounts))
	}
}

func TestMailHandler_Accounts_RendersSnapshots(t *testing.T) {
	h := NewMailHandler(testLogger())
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	h.SetAccountRunner(&fakeRunner{
		snapshots: []AccountSnapshot{
			{AccountID: "alice@gmail.com", Provider: "gmail", Email: "alice@gmail.com", Status: "healthy", BriefReason: "ok", LastSync: now, LastVerified: now},
			{AccountID: "bob@proton.me", Provider: "proton", Email: "bob@proton.me", Status: "unhealthy", BriefReason: "auth_issue"},
		},
	})
	h.SetAccountCounts(&fakeCounts{counts: map[string]int64{"alice@gmail.com": 42}})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp AccountsResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Accounts) != 2 {
		t.Fatalf("got %d accounts", len(resp.Accounts))
	}
	// First account: alice — connected with thread count.
	a := resp.Accounts[0]
	if a.AccountID != "alice@gmail.com" || a.Status != "connected" || a.BriefReason != "ok" || a.ThreadCount != 42 {
		t.Errorf("alice mismatch: %+v", a)
	}
	if a.LastSync == "" {
		t.Error("alice last_sync should be populated")
	}
	// Second: bob — disconnected with auth_issue, no count.
	b := resp.Accounts[1]
	if b.Status != "disconnected" || b.BriefReason != "auth_issue" || b.ThreadCount != 0 {
		t.Errorf("bob mismatch: %+v", b)
	}
}

func TestMailHandler_VerifyAccount_Success(t *testing.T) {
	h := NewMailHandler(testLogger())
	now := time.Date(2026, 4, 27, 11, 0, 0, 0, time.UTC)
	h.SetAccountRunner(&fakeRunner{verified: AccountSnapshot{
		AccountID: "alice@gmail.com", Status: "healthy", BriefReason: "ok", LastVerified: now,
	}})

	r := newAccountsRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/mail/accounts/alice@gmail.com/verify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp VerifyAccountResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccountID != "alice@gmail.com" || resp.Status != "connected" || resp.BriefReason != "ok" {
		t.Errorf("verify resp mismatch: %+v", resp)
	}
	if resp.LastVerified == "" {
		t.Error("LastVerified should be populated")
	}
}

func TestMailHandler_VerifyAccount_NotFound(t *testing.T) {
	h := NewMailHandler(testLogger())
	h.SetAccountRunner(&fakeRunner{verifyErr: errAccountNotFound{}})
	r := newAccountsRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/mail/accounts/ghost/verify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

type errAccountNotFound struct{}

func (errAccountNotFound) Error() string { return "mail account not found" }

func TestMailHandler_AccountStats(t *testing.T) {
	h := NewMailHandler(testLogger())
	h.SetAccountCounts(&fakeCounts{counts: map[string]int64{"alice@gmail.com": 17}})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/alice@gmail.com/stats", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"thread_count":17`) {
		t.Errorf("expected thread_count in body, got %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AccountLabels tests
// ---------------------------------------------------------------------------

func TestMailHandler_AccountLabels_ReturnsLabels(t *testing.T) {
	h := NewMailHandler(testLogger())
	conn := &fakeLabelConn{
		labels: []mail.Label{
			{ID: "INBOX", Name: "Inbox", Type: "system"},
			{ID: "Label_123", Name: "Recharge", Type: "user"},
		},
	}
	h.SetAccountRunner(&fakeRunner{
		connectors: map[string]mail.MailConnector{"alice@gmail.com": conn},
	})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/alice@gmail.com/labels", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp LabelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Labels) != 2 {
		t.Fatalf("want 2 labels, got %d", len(resp.Labels))
	}
	if resp.Labels[0].ID != "INBOX" || resp.Labels[1].Name != "Recharge" {
		t.Errorf("unexpected labels: %+v", resp.Labels)
	}
}

func TestMailHandler_AccountLabels_NotFound(t *testing.T) {
	h := NewMailHandler(testLogger())
	h.SetAccountRunner(&fakeRunner{connectors: map[string]mail.MailConnector{}})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/ghost@gmail.com/labels", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMailHandler_AccountLabels_NoRunner(t *testing.T) {
	h := NewMailHandler(testLogger())
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/alice@gmail.com/labels", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
}

func TestMailHandler_AccountLabels_ConnectorError(t *testing.T) {
	h := NewMailHandler(testLogger())
	conn := &fakeLabelConn{labelsErr: errors.New("upstream timeout")}
	h.SetAccountRunner(&fakeRunner{
		connectors: map[string]mail.MailConnector{"alice@gmail.com": conn},
	})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/alice@gmail.com/labels", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// AccountMailboxes tests
// ---------------------------------------------------------------------------

func TestMailHandler_AccountMailboxes_ReturnsMailboxes(t *testing.T) {
	h := NewMailHandler(testLogger())
	conn := &fakeMailboxConn{mailboxes: []string{"All Mail", "INBOX", "Sent"}}
	h.SetAccountRunner(&fakeRunner{
		connectors: map[string]mail.MailConnector{"bob@proton.me": conn},
	})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/bob@proton.me/mailboxes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp MailboxesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Mailboxes) != 3 || resp.Mailboxes[0] != "All Mail" {
		t.Errorf("unexpected mailboxes: %+v", resp.Mailboxes)
	}
}

func TestMailHandler_AccountMailboxes_NotFound(t *testing.T) {
	h := NewMailHandler(testLogger())
	h.SetAccountRunner(&fakeRunner{connectors: map[string]mail.MailConnector{}})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/ghost/mailboxes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func TestMailHandler_AccountMailboxes_NotSupported(t *testing.T) {
	// fakeLabelConn implements LabelLister but NOT MailboxLister.
	h := NewMailHandler(testLogger())
	conn := &fakeLabelConn{}
	h.SetAccountRunner(&fakeRunner{
		connectors: map[string]mail.MailConnector{"alice@gmail.com": conn},
	})
	r := newAccountsRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/mail/accounts/alice@gmail.com/mailboxes", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 NOT_SUPPORTED, got %d body=%s", rec.Code, rec.Body.String())
	}
}
