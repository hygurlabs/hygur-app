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
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hygur/sidecar/internal/controlplane"
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
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hygur-console <serve|account|code|device> ...")
	os.Exit(2)
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

	fmt.Printf("hygur-console serving enroll/refresh on %s (domain %s)\n", *addr, *domain)
	srv := &http.Server{Addr: *addr, Handler: svc.Routes(), ReadHeaderTimeout: 10 * time.Second}
	die(srv.ListenAndServe())
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
		_ = fs.Parse(args[1:])
		if *email == "" {
			die(fmt.Errorf("account create: --email is required"))
		}
		acc, err := store.CreateAccount(time.Now(), *email, *status, nil)
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
