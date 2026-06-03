package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hygur/sidecar/internal/auth"
)

// runGenAuthKey prints a fresh Ed25519 keypair (PEM). The public key goes in the
// server's auth.public_key; the private key stays with the issuer (self-host
// operator or, later, the control plane) for `issue-token`.
func runGenAuthKey() {
	pub, priv, err := auth.GenerateEd25519KeyPEM()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-auth-key:", err)
		os.Exit(1)
	}
	fmt.Println("# Ed25519 keypair for Hygur remote auth.")
	fmt.Println("# Server:  set auth.mode=remote and auth.public_key to the PUBLIC key below.")
	fmt.Println("# Issuer:  keep the PRIVATE key (auth.private_key / HYGUR_AUTH_PRIVATE_KEY) to mint device tokens.")
	fmt.Println("# --- PUBLIC KEY ---")
	fmt.Print(pub)
	fmt.Println("# --- PRIVATE KEY ---")
	fmt.Print(priv)
}

// runIssueToken mints a per-device JWT signed with a locally-held private key.
// Self-hosting convenience; in Hygur Cloud the control plane issues these.
func runIssueToken(args []string) {
	fs := flag.NewFlagSet("issue-token", flag.ExitOnError)
	user := fs.String("user", "", "user id (sub) — required")
	account := fs.String("account", "", "account id")
	device := fs.String("device", "", "device id")
	ttl := fs.Duration("ttl", 90*24*time.Hour, "token lifetime (e.g. 720h)")
	keyFile := fs.String("key", "", "path to Ed25519 private key PEM (or env HYGUR_AUTH_PRIVATE_KEY)")
	_ = fs.Parse(args)

	if *user == "" {
		fmt.Fprintln(os.Stderr, "issue-token: --user is required")
		os.Exit(1)
	}

	keyPEM := os.Getenv("HYGUR_AUTH_PRIVATE_KEY")
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "issue-token:", err)
			os.Exit(1)
		}
		keyPEM = string(b)
	}
	if keyPEM == "" {
		fmt.Fprintln(os.Stderr, "issue-token: provide --key <file> or set HYGUR_AUTH_PRIVATE_KEY")
		os.Exit(1)
	}

	priv, err := auth.ParseEd25519PrivateKeyPEM(keyPEM)
	if err != nil {
		fmt.Fprintln(os.Stderr, "issue-token:", err)
		os.Exit(1)
	}

	now := time.Now()
	tok, err := auth.SignDeviceToken(priv, auth.DeviceClaims{
		Sub: *user,
		Acc: *account,
		Dev: *device,
		Jti: auth.NewJTI(),
		Iat: now.Unix(),
		Exp: now.Add(*ttl).Unix(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "issue-token:", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
