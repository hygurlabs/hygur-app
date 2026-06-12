package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
)

// runDEK handles `hygur dek <subcommand>` (operator/provisioning CLI). Today only
// `generate`: mint a fresh per-tenant Data Encryption Key (DEK). The DEK is what
// the cloud injects as HYGUR_DB_KEY; the tenant DB is SQLCipher-encrypted with it.
// At rest the DEK is wrapped (sealed-secrets) and never committed in clear.
func runDEK(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "generate", "gen", "":
		runDEKGenerate(args)
	default:
		fmt.Fprintf(os.Stderr, "dek: unknown subcommand %q (want: generate)\n", sub)
		os.Exit(1)
	}
}

func runDEKGenerate(args []string) {
	fs := flag.NewFlagSet("dek generate", flag.ExitOnError)
	raw := fs.Bool("raw", false, "print only the key, no comments (for scripting/piping)")
	if len(args) > 1 {
		_ = fs.Parse(args[1:])
	}
	key, err := generateDEK()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dek generate:", err)
		os.Exit(1)
	}
	if *raw {
		fmt.Println(key)
		return
	}
	fmt.Println("# Hygur tenant Data Encryption Key (DEK) — 256-bit, base64.")
	fmt.Println("# Inject as HYGUR_DB_KEY on the tenant pod (seal it via sealed-secrets;")
	fmt.Println("# never commit it in clear). The tenant DB is SQLCipher-encrypted with it.")
	fmt.Println(key)
}

// generateDEK returns a fresh 256-bit key, base64-encoded — suitable as the
// SQLCipher passphrase (HYGUR_DB_KEY).
func generateDEK() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
