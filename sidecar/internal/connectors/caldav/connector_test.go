package caldav

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/plugin"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func TestConnectorInfoAndSchema(t *testing.T) {
	c := New(nil, nil, nil, zerolog.Nop(), false)
	if got := c.Info().ID; got != "caldav" {
		t.Errorf("Info().ID = %q", got)
	}
	if !c.Info().MultiInstance {
		t.Error("CalDAV must be multi-instance")
	}
	// url must be a required field; schedule must default to a cron expr.
	var sawURL, sawCron bool
	for _, g := range c.ConfigSchema().Groups {
		for _, f := range g.Fields {
			if f.Key == "url" && f.Required {
				sawURL = true
			}
			if f.Type == plugin.FieldCron && f.Default != "" {
				sawCron = true
			}
		}
	}
	if !sawURL {
		t.Error("schema missing required url field")
	}
	if !sawCron {
		t.Error("schema missing cron field with default")
	}
	if keys := c.SecretFieldKeys(); len(keys) != 1 || keys[0] != "password" {
		t.Errorf("SecretFieldKeys = %v", keys)
	}
}

func TestSyncIngestsEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	// allowPrivate=true: the httptest server binds loopback (127.0.0.1), which the
	// SSRF guard blocks by default; a self-host LAN calendar is the real analogue.
	c := New(db, nil /* emb=nil → skip embedding */, nil, zerolog.Nop(), true)
	ctx := context.Background()
	if err := c.Init(ctx, plugin.ConnectorConfig{Enabled: true, Settings: map[string]string{"url": srv.URL}}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	res, err := c.Sync(ctx, plugin.SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Processed != 2 {
		t.Fatalf("Processed = %d, want 2", res.Processed)
	}

	items, err := db.ListKnowledgeItemsBySourceType(ctx, "event", 50, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("stored %d events, want 2", len(items))
	}

	// The timed event must carry start/canonical_date metadata + its summary.
	var found bool
	for _, it := range items {
		if it.Title == "Réunion budget, Q3" {
			found = true
			if it.Metadata["canonical_date"] == nil {
				t.Error("event missing canonical_date metadata")
			}
			if it.Metadata["location"] != "Salle B" {
				t.Errorf("event location metadata = %v", it.Metadata["location"])
			}
		}
	}
	if !found {
		t.Error("timed event not found in store")
	}

	// A second sync of the same feed must be idempotent (no duplicates).
	if _, err := c.Sync(ctx, plugin.SyncOptions{}); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	items, _ = db.ListKnowledgeItemsBySourceType(ctx, "event", 50, 0)
	if len(items) != 2 {
		t.Fatalf("after re-sync stored %d events, want 2 (idempotent)", len(items))
	}
}

// TestSyncSSRFToggle is the key proof: the same loopback httptest server is
// reachable when allowPrivate=true and REFUSED when allowPrivate=false. It shows
// the guard blocks internal targets and that the operator toggle works both ways.
func TestSyncSSRFToggle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	newMem := func(t *testing.T) *store.DB {
		db, err := store.NewDB(":memory:")
		if err != nil {
			t.Fatalf("NewDB: %v", err)
		}
		return db
	}

	// allowPrivate=true → CAN reach the loopback server.
	t.Run("allow", func(t *testing.T) {
		db := newMem(t)
		defer db.Close()
		c := New(db, nil, nil, zerolog.Nop(), true)
		ctx := context.Background()
		if err := c.Init(ctx, plugin.ConnectorConfig{Settings: map[string]string{"url": srv.URL}}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		res, err := c.Sync(ctx, plugin.SyncOptions{})
		if err != nil {
			t.Fatalf("allowPrivate=true Sync should succeed: %v", err)
		}
		if res.Processed == 0 {
			t.Fatal("allowPrivate=true: expected events, got 0")
		}
	})

	// allowPrivate=false → REFUSES to connect to the non-public target.
	t.Run("block", func(t *testing.T) {
		db := newMem(t)
		defer db.Close()
		c := New(db, nil, nil, zerolog.Nop(), false)
		ctx := context.Background()
		if err := c.Init(ctx, plugin.ConnectorConfig{Settings: map[string]string{"url": srv.URL}}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		_, err := c.Sync(ctx, plugin.SyncOptions{})
		if err == nil {
			t.Fatal("allowPrivate=false Sync should be refused")
		}
		if !strings.Contains(err.Error(), "non-public") {
			t.Errorf("refusal error = %q, want it to mention non-public", err)
		}
	})
}

// TestSyncRejectsNonHTTPScheme checks a file:// (or any non-http/https) URL is
// rejected before any request — even with allowPrivate=true, so it is the scheme
// allowlist doing the rejecting, not the IP guard.
func TestSyncRejectsNonHTTPScheme(t *testing.T) {
	c := New(nil, nil, nil, zerolog.Nop(), true)
	ctx := context.Background()
	if err := c.Init(ctx, plugin.ConnectorConfig{Settings: map[string]string{"url": "file:///etc/passwd"}}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.Sync(ctx, plugin.SyncOptions{}); err == nil {
		t.Error("file:// url must be rejected before any request")
	}
}

// TestSyncIgnoresTenantAllowPrivate proves the flag is sourced from the global
// config (constructor arg), NOT from the tenant-editable ConnectorConfig: a
// tenant that stuffs allow_private_targets into their settings must NOT be able
// to lift the guard on a managed (allowPrivate=false) connector.
func TestSyncIgnoresTenantAllowPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()

	c := New(db, nil, nil, zerolog.Nop(), false) // managed cloud: guard on
	ctx := context.Background()
	if err := c.Init(ctx, plugin.ConnectorConfig{Settings: map[string]string{
		"url":                   srv.URL,
		"allow_private_targets": "true", // tenant attempt — must be ignored
	}}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := c.Sync(ctx, plugin.SyncOptions{}); err == nil {
		t.Error("a tenant setting must not be able to lift the SSRF guard")
	}
}

func TestSyncFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(nil, nil, nil, zerolog.Nop(), true) // loopback httptest → allowPrivate
	ctx := context.Background()
	_ = c.Init(ctx, plugin.ConnectorConfig{Settings: map[string]string{"url": srv.URL}})
	if _, err := c.Sync(ctx, plugin.SyncOptions{}); err == nil {
		t.Error("expected error on HTTP 500, got nil")
	}
	if c.Health().Status != plugin.StatusUnhealthy {
		t.Errorf("health after fetch error = %v, want unhealthy", c.Health().Status)
	}
}
