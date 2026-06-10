// Command hygur-console is the Hygur Cloud control plane (C8): it serves the
// device enroll/refresh API and provides operator commands to create accounts
// and mint enrollment codes. One instance for the whole fleet (NOT per-tenant).
//
//	hygur-console serve                      # run the enroll/refresh HTTP API
//	hygur-console account create --email x   # create an account → tenant id + number
//	hygur-console account show --account N
//	hygur-console code create --account N [--label web] [--ttl 10m]
//	hygur-console device revoke --device ID
//
// Config (env): HYGUR_CONSOLE_DB (path), HYGUR_CONSOLE_DB_KEY (SQLCipher key),
// HYGUR_AUTH_PRIVATE_KEY (issuer key PEM, for `serve`), HYGUR_CONSOLE_DOMAIN
// (tenant domain, default hygur.ai), HYGUR_CONSOLE_ADDR (default :8090).
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hygur/sidecar/internal/controlplane"
	"github.com/hygur/sidecar/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "account":
		runAccount(os.Args[2:])
	case "code":
		runCode(os.Args[2:])
	case "device":
		runDevice(os.Args[2:])
	case "provisions":
		runProvisions(os.Args[2:])
	case "backup-db":
		runConsoleBackupDB(os.Args[2:])
	case "usage":
		runConsoleUsage(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hygur-console <serve|account|code|device|provisions|backup-db|usage> ...")
	os.Exit(2)
}

// runConsoleBackupDB writes a consistent snapshot of the control-plane admin DB
// to --out, preserving its SQLCipher encryption (same key). Invoked by the
// off-box backup job via `kubectl exec`; safe to run while `serve` holds the DB
// open (its own read connection). Reuses store.SnapshotTo — the snapshot is
// schema-agnostic.
func runConsoleBackupDB(args []string) {
	fs := flag.NewFlagSet("backup-db", flag.ExitOnError)
	out := fs.String("out", "", "destination snapshot path (required; must not exist)")
	_ = fs.Parse(args)
	if *out == "" {
		die(fmt.Errorf("backup-db: --out is required"))
	}
	path := os.Getenv("HYGUR_CONSOLE_DB")
	if path == "" {
		path = "hygur-console.db"
	}
	_ = os.Remove(*out) // sqlcipher_export requires a fresh target
	die(store.SnapshotTo(context.Background(), path, *out, os.Getenv("HYGUR_CONSOLE_DB_KEY")))
	fmt.Printf("snapshot written: %s\n", *out)
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hygur-console:", err)
		os.Exit(1)
	}
}

// openStore opens the control-plane admin DB (SQLCipher) from env.
func openStore() *controlplane.Store {
	path := os.Getenv("HYGUR_CONSOLE_DB")
	if path == "" {
		path = "hygur-console.db"
	}
	s, err := controlplane.Open(path, os.Getenv("HYGUR_CONSOLE_DB_KEY"))
	die(err)
	return s
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("HYGUR_CONSOLE_ADDR", ":8090"), "listen address")
	domain := fs.String("domain", envOr("HYGUR_CONSOLE_DOMAIN", "hygur.ai"), "tenant domain")
	ttl := fs.Duration("access-ttl", 15*time.Minute, "access-token lifetime")
	_ = fs.Parse(args)

	priv := os.Getenv("HYGUR_AUTH_PRIVATE_KEY")
	if priv == "" {
		die(fmt.Errorf("serve: HYGUR_AUTH_PRIVATE_KEY (issuer key PEM) is required"))
	}
	store := openStore()
	defer store.Close()
	svc, err := controlplane.NewService(store, priv, *domain, *ttl)
	die(err)

	root := chi.NewRouter()
	// The cloud web shell (cloud.hygur.ai) calls enroll + passkey ceremonies
	// cross-origin; permit it (+ console) here. Loopback is always allowed.
	rpOrigins := splitCSV(envOr("HYGUR_RP_ORIGINS", "https://cloud.hygur.ai,https://console.hygur.ai"))
	root.Use(controlplane.CORSMiddleware(rpOrigins))
	svc.Register(root)

	// Passkeys (WebAuthn): RP ID is the registrable parent domain so a passkey
	// registered on console.hygur.ai authenticates on cloud.hygur.ai.
	rpID := envOr("HYGUR_RP_ID", "hygur.ai")
	wa, err := controlplane.NewWebAuthnService(store, svc, rpID, "Hygur Cloud", rpOrigins)
	die(err)
	wa.Register(root)
	fmt.Printf("hygur-console: passkey (WebAuthn) enabled — RP %q, origins %v\n", rpID, rpOrigins)

	if wh := os.Getenv("HYGUR_STRIPE_WEBHOOK_SECRET"); wh != "" {
		controlplane.NewBilling(store, wh).Register(root)
		fmt.Println("hygur-console: Stripe billing webhook + success page enabled")
	}

	// Operator admin surface (cost dashboard API), gated by the operator account's
	// passkey-minted token. HYGUR_OPERATOR_ACCOUNT = admin@hygur.ai's account number.
	if op := strings.TrimSpace(os.Getenv("HYGUR_OPERATOR_ACCOUNT")); op != "" {
		controlplane.NewAdminConsole(store, svc, op).Register(root)
		fmt.Printf("hygur-console: admin cost API enabled (/admin/cost, operator %s)\n", op)
	}

	fmt.Printf("hygur-console serving on %s (domain %s)\n", *addr, *domain)
	srv := &http.Server{Addr: *addr, Handler: root, ReadHeaderTimeout: 10 * time.Second}
	die(srv.ListenAndServe())
}

// runProvisions is the poller's interface to the admin DB (runs on-box, with the
// DB key). The internet-facing `serve` never provisions; the poller drives state.
//
//	hygur-console provisions pending        # \t-sep: <sub_id> <tenant_id> <account>
//	hygur-console provisions deprovision    # tenants to reap (canceled)
//	hygur-console provisions suspend        # tenants to scale-to-0 (payment past_due)
//	hygur-console provisions resume         # tenants to scale-to-1 (payment recovered)
//	hygur-console provisions purgeable [--days 30]  # reaped tenants past retention → reclaim PV
//	hygur-console provisions count          # live tenants (pending+ready) for the cap
//	hygur-console provisions ready     <sub># pod created / resumed → mark ready
//	hygur-console provisions suspended <sub># pod scaled to 0 → mark suspended
//	hygur-console provisions failed    <sub># provisioning failed (will retry next pass)
//	hygur-console provisions gone      <sub># pod reaped (stamps the retention clock) → mark gone
//	hygur-console provisions purged    <sub># PV/host dir reclaimed → mark purged
func runProvisions(args []string) {
	if len(args) == 0 {
		die(fmt.Errorf("usage: hygur-console provisions <pending|deprovision|suspend|resume|purgeable|count|ready|suspended|failed|gone|purged> [sub_id]"))
	}
	store := openStore()
	defer store.Close()
	printRows := func(rows []controlplane.ProvisionRow) {
		for _, r := range rows {
			fmt.Printf("%s\t%s\t%s\n", r.SubID, r.TenantID, r.Account)
		}
	}
	switch args[0] {
	case "pending", "deprovision", "suspend", "resume":
		rows, err := store.ListProvisions(args[0])
		die(err)
		printRows(rows)
	case "purgeable":
		fs := flag.NewFlagSet("provisions purgeable", flag.ExitOnError)
		days := fs.Int("days", 30, "retention window in days before a reaped tenant's PV is reclaimed")
		_ = fs.Parse(args[1:])
		rows, err := store.ListPurgeable(time.Now(), time.Duration(*days)*24*time.Hour)
		die(err)
		printRows(rows)
	case "count":
		n, err := store.CountActiveTenants()
		die(err)
		fmt.Println(n)
	case "ready", "suspended", "failed", "gone", "purged":
		if len(args) < 2 {
			die(fmt.Errorf("usage: hygur-console provisions %s <sub_id>", args[0]))
		}
		die(store.SetProvisionState(args[1], args[0]))
	default:
		die(fmt.Errorf("unknown provisions subcommand %q", args[0]))
	}
}

func runAccount(args []string) {
	if len(args) == 0 {
		die(fmt.Errorf("usage: hygur-console account <create|show> ..."))
	}
	store := openStore()
	defer store.Close()
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("account create", flag.ExitOnError)
		email := fs.String("email", "", "account email (required)")
		status := fs.String("status", "trialing", "subscription status")
		tenant := fs.String("tenant", "", "pin tenant id (e.g. home); default auto instance-personal-<num>")
		_ = fs.Parse(args[1:])
		if *email == "" {
			die(fmt.Errorf("account create: --email is required"))
		}
		var acc controlplane.Account
		var err error
		if strings.TrimSpace(*tenant) != "" {
			acc, err = store.CreateAccountWithTenant(time.Now(), *email, *status, *tenant, nil)
		} else {
			acc, err = store.CreateAccount(time.Now(), *email, *status, nil)
		}
		die(err)
		fmt.Printf("account_number: %s\ntenant_id:      %s\nemail:          %s\nstatus:         %s\n",
			acc.AccountNumber, acc.TenantID, acc.Email, acc.Status)
		fmt.Printf("\nNext: provision the tenant '%s' (with the control-plane PUBLIC key + HYGUR_TENANT_ID=%s),\nthen `hygur-console code create --account %s` and hand the code to the client.\n",
			acc.TenantID, acc.TenantID, acc.AccountNumber)
	case "show":
		fs := flag.NewFlagSet("account show", flag.ExitOnError)
		num := fs.String("account", "", "account number (required)")
		_ = fs.Parse(args[1:])
		acc, err := store.GetAccount(*num)
		die(err)
		valid := "—"
		if acc.ValidUntil != nil {
			valid = acc.ValidUntil.Format(time.RFC3339)
		}
		fmt.Printf("account_number: %s\ntenant_id:      %s\nemail:          %s\nstatus:         %s\nvalid_until:    %s\n",
			acc.AccountNumber, acc.TenantID, acc.Email, acc.Status, valid)
	default:
		die(fmt.Errorf("unknown account subcommand %q", args[0]))
	}
}

func runCode(args []string) {
	if len(args) == 0 || args[0] != "create" {
		die(fmt.Errorf("usage: hygur-console code create --account N [--label web] [--ttl 10m]"))
	}
	fs := flag.NewFlagSet("code create", flag.ExitOnError)
	num := fs.String("account", "", "account number (required)")
	label := fs.String("label", "web", "device label")
	ttl := fs.Duration("ttl", 10*time.Minute, "code lifetime")
	_ = fs.Parse(args[1:])
	if *num == "" {
		die(fmt.Errorf("code create: --account is required"))
	}
	store := openStore()
	defer store.Close()
	code, err := store.CreateEnrollCode(time.Now(), *num, *label, *ttl)
	die(err)
	fmt.Printf("enrollment code (valid %s, single use):\n%s\n", ttl.String(), code)
}

func runDevice(args []string) {
	if len(args) == 0 {
		die(fmt.Errorf("usage: hygur-console device <list|revoke> ..."))
	}
	store := openStore()
	defer store.Close()
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("device list", flag.ExitOnError)
		num := fs.String("account", "", "account number (required)")
		_ = fs.Parse(args[1:])
		devs, err := store.ListDevices(*num)
		die(err)
		for _, d := range devs {
			state := "active"
			if d.RevokedAt != nil {
				state = "revoked"
			}
			fmt.Printf("%s  %-8s  %-6s  jti=%s\n", d.DeviceID, d.Label, state, d.JTI)
		}
	case "revoke":
		fs := flag.NewFlagSet("device revoke", flag.ExitOnError)
		id := fs.String("device", "", "device id (required)")
		_ = fs.Parse(args[1:])
		die(store.RevokeDevice(time.Now(), *id))
		fmt.Println("revoked (propagate the jti to the tenant's auth.revoked_jtis for immediate cutoff)")
	default:
		die(fmt.Errorf("unknown device subcommand %q", args[0]))
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// splitCSV splits a comma-separated env value into trimmed, non-empty entries.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
