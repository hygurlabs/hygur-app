package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/auth"
)

// TestAdminCost_OperatorGate is the wiring test: /admin/cost is mounted behind
// the operator gate — 401 without/with a bad token, 403 for a non-operator
// account, 200 for the operator's passkey-minted token.
func TestAdminCost_OperatorGate(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "c.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, privPEM, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(store, privPEM, "hygur.ai", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	const opAccount = "999999"
	r := chi.NewRouter()
	NewAdminConsole(store, svc, opAccount).Register(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	get := func(tok string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/cost", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	now := time.Now()
	opTok, err := svc.mintAccess(now, Account{AccountNumber: opAccount, TenantID: "operator"}, Device{DeviceID: "d1", JTI: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	wrongTok, err := svc.mintAccess(now, Account{AccountNumber: "111111", TenantID: "operator"}, Device{DeviceID: "d2", JTI: "j2"})
	if err != nil {
		t.Fatal(err)
	}

	if c := get(""); c != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", c)
	}
	if c := get("garbage.token.value"); c != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", c)
	}
	if c := get(wrongTok); c != http.StatusForbidden {
		t.Fatalf("non-operator account: got %d, want 403", c)
	}
	if c := get(opTok); c != http.StatusOK {
		t.Fatalf("operator: got %d, want 200", c)
	}
}

// TestAdminCost_BudgetSurfaced is the §A wiring test: when a fleet daily token
// budget is configured and today's fleet total exceeds it, /admin/cost surfaces
// budget.status="over" (the dashboard banner's source). With no budget, status is "ok".
func TestAdminCost_BudgetSurfaced(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "c.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, privPEM, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(store, privPEM, "hygur.ai", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }

	// Today's fleet usage = 1.2M tokens (chat 800k+300k + ingest 100k).
	if err := store.UpsertTenantUsage(fixed, TenantUsageDay{
		TenantID: "home", Account: "42", Day: fixed.Format("2006-01-02"),
		ChatIn: 800_000, ChatOut: 300_000, Ingest: 100_000,
	}); err != nil {
		t.Fatal(err)
	}

	const opAccount = "999999"
	opTok, err := svc.mintAccess(fixed, Account{AccountNumber: opAccount, TenantID: "operator"}, Device{DeviceID: "d1", JTI: "j1"})
	if err != nil {
		t.Fatal(err)
	}

	type costResp struct {
		Budget FleetBudget `json:"budget"`
	}
	fetch := func(budget int) costResp {
		r := chi.NewRouter()
		NewAdminConsole(store, svc, opAccount).WithDailyTokenBudget(budget).Register(r)
		srv := httptest.NewServer(r)
		defer srv.Close()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/cost", nil)
		req.Header.Set("Authorization", "Bearer "+opTok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		var cr costResp
		if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
			t.Fatal(err)
		}
		return cr
	}

	// 1M budget < 1.2M used → over.
	if cr := fetch(1_000_000); cr.Budget.Status != FleetBudgetOver || cr.Budget.TodayTokens != 1_200_000 {
		t.Fatalf("over: got status=%q today=%d, want over/1200000", cr.Budget.Status, cr.Budget.TodayTokens)
	}
	// No budget → disabled, status ok.
	if cr := fetch(0); cr.Budget.Status != FleetBudgetOK || cr.Budget.TokensPerDay != 0 {
		t.Fatalf("disabled: got status=%q budget=%d, want ok/0", cr.Budget.Status, cr.Budget.TokensPerDay)
	}
}
