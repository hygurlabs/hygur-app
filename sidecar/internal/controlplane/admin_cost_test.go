package controlplane

import (
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
