package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	mailpkg "github.com/hygur/sidecar/internal/mail"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// reconcileConn is a minimal MailConnector that also enumerates present thread IDs
// (MessageIDLister), so the recycle reconcile can be driven without a live server.
// recycleReconcile only ever calls ListMessageIDs; the rest satisfy the interface.
type reconcileConn struct {
	ids []string
	err error
}

func (r *reconcileConn) Connect(context.Context) error { return nil }
func (r *reconcileConn) Disconnect() error             { return nil }
func (r *reconcileConn) IsConnected() bool             { return true }
func (r *reconcileConn) ListThreads(context.Context, mailpkg.ListOptions) ([]mailpkg.Thread, error) {
	return nil, nil
}
func (r *reconcileConn) GetThread(context.Context, string) (*mailpkg.Thread, error) {
	return nil, nil
}
func (r *reconcileConn) GetMessages(context.Context, string) ([]mailpkg.Message, error) {
	return nil, nil
}
func (r *reconcileConn) GetMessagesByThread(context.Context, *mailpkg.Thread) ([]mailpkg.Message, error) {
	return nil, nil
}
func (r *reconcileConn) ListMessageIDs(context.Context, string) ([]string, int, error) {
	if r.err != nil {
		return nil, 0, r.err
	}
	return r.ids, len(r.ids), nil
}

func seedGmailItem(t *testing.T, db *store.DB, ctx context.Context, cid string, withRef bool) {
	t.Helper()
	md := map[string]any{"account_id": "acctA"}
	if withRef {
		md["source_ref"] = "gmail:" + cid[len("email:"):]
	}
	if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
		ContentID: cid, SourceType: "mail", Title: cid, NormalizedText: "body " + cid,
		Metadata: md, VersionID: "v1", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed %s: %v", cid, err)
	}
}

// TestRecycleReconcile_PrunesAbsentMail is the E2E for Gmail-direct deletion
// reconciliation: items with no source_ref (the legacy local-ingest shape) are
// backfilled, then mail absent from the server's present-set is soft-deleted to
// the recycle bin while present mail stays active.
func TestRecycleReconcile_PrunesAbsentMail(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	seedGmailItem(t, db, ctx, "email:t1", false) // backfill will stamp gmail:t1
	seedGmailItem(t, db, ctx, "email:t2", false) // backfill will stamp gmail:t2

	conn := &reconcileConn{ids: []string{"t1"}} // server still has t1; t2 deleted
	c := &MailConnector{store: db, logger: zerolog.Nop()}
	c.recycleReconcile(ctx, conn, "gmail", "acctA")

	if it, _ := db.GetKnowledgeItem(ctx, "email:t1"); it == nil {
		t.Fatal("t1 (present on server) must stay active")
	}
	if it, _ := db.GetKnowledgeItem(ctx, "email:t2"); it != nil {
		t.Fatal("t2 (deleted on server) must be recycled out of active items")
	}
	entries, err := db.ListRecycleByPrefix(ctx, "gmail:")
	if err != nil {
		t.Fatalf("ListRecycleByPrefix: %v", err)
	}
	if len(entries) != 1 || entries[0].ContentID != "email:t2" {
		t.Fatalf("expected t2 in recycle, got %+v", entries)
	}
}

// TestRecycleReconcile_FailSafeOnEnumError: a failed/partial enumeration must
// never be read as "everything else is deleted" — nothing is recycled.
func TestRecycleReconcile_FailSafeOnEnumError(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	seedGmailItem(t, db, ctx, "email:t1", true)

	conn := &reconcileConn{err: errors.New("enumeration boom")}
	c := &MailConnector{store: db, logger: zerolog.Nop()}
	c.recycleReconcile(ctx, conn, "gmail", "acctA")

	if it, _ := db.GetKnowledgeItem(ctx, "email:t1"); it == nil {
		t.Fatal("a failed enumeration must not recycle anything")
	}
}
