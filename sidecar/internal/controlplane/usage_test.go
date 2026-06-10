package controlplane

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTenantUsageSnapshots_AndPricing(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "console.db"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now()

	if err := s.UpsertTenantUsage(now, TenantUsageDay{TenantID: "home", Account: "000042", Day: "2026-06-10", ChatIn: 1000, ChatOut: 500, Ingest: 200}); err != nil {
		t.Fatal(err)
	}
	// Re-poll the same day → overwrite, not duplicate.
	if err := s.UpsertTenantUsage(now, TenantUsageDay{TenantID: "home", Account: "000042", Day: "2026-06-10", ChatIn: 1100, ChatOut: 550, Ingest: 220}); err != nil {
		t.Fatal(err)
	}
	var n, chatIn int
	if err := s.db.QueryRow(`SELECT COUNT(*), chat_in FROM tenant_usage_snapshots WHERE tenant_id='home' AND day='2026-06-10'`).Scan(&n, &chatIn); err != nil {
		t.Fatal(err)
	}
	if n != 1 || chatIn != 1100 {
		t.Fatalf("idempotency: got n=%d chat_in=%d, want 1/1100", n, chatIn)
	}

	if err := s.SetFleetPricing(FleetPricing{ChatInPer1M: 2, ChatOutPer1M: 6, IngestPer1M: 0.5, Currency: "€"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetFleetPricing()
	if err != nil {
		t.Fatal(err)
	}
	if p.ChatInPer1M != 2 || p.ChatOutPer1M != 6 || p.IngestPer1M != 0.5 || p.Currency != "€" {
		t.Fatalf("pricing round-trip: got %+v", p)
	}

	if _, err := s.ListLiveTenants(); err != nil { // no accounts yet → empty, no error
		t.Fatalf("ListLiveTenants: %v", err)
	}
}
