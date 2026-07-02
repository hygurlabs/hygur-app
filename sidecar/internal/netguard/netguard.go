// Package netguard is the shared SSRF-hardening layer for every server-side
// fetch to a user- or model-supplied target: the web tools (fetch_url,
// web_search) AND the outbound connectors (CalDAV, IMAP). Its single job is to
// make sure such fetches can never reach non-public addresses (cloud metadata,
// k8s API, other tenants, localhost) unless an operator has explicitly opted in
// via allowPrivate.
//
// The guard resolves the target at DIAL time and refuses if any resolved IP is
// disallowed — closing the DNS-rebinding/TOCTOU window (a name that resolves
// public at validation but internal at connect). HTTP redirects are re-validated
// on every hop; libraries that take a *net.Dialer get the same check via the
// dialer's Control hook (which runs on the concrete resolved IP).
//
// allowPrivate is an OPERATOR/global setting (see config.ConnectorSecurity):
// managed multi-tenant cloud leaves it false; a self-hoster may set it true for
// a LAN CalDAV/Nextcloud or IMAP server. It must NEVER be sourced from a
// tenant-editable connector config.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// dialTimeout bounds the TCP connect once a host has been resolved. Matches the
// value previously hard-coded in the web tools' safe dialer.
const dialTimeout = 5 * time.Second

// maxRedirects caps HTTP redirect chains; each hop is still re-validated by the
// safe dialer, so this only stops redirect loops / amplification.
const maxRedirects = 5

// IsDisallowedIP reports whether an IP must never be dialed for a public fetch:
// loopback, private (RFC1918 + ULA), link-local (incl. 169.254.169.254 metadata),
// CGNAT, unspecified, and multicast.
func IsDisallowedIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT 100.64.0.0/10 (sometimes routes to cloud metadata/infra).
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

// DialContext returns a DialContext function that resolves the target at dial
// time and, unless allowPrivate is set, refuses if any resolved IP is disallowed.
// It is re-run on every redirect hop by the HTTP client built in Client.
func DialContext(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", host, err)
		}
		if !allowPrivate {
			for _, ip := range ips {
				if IsDisallowedIP(ip) {
					return nil, fmt.Errorf("refusing to connect to non-public address %s (%s)", ip, host)
				}
			}
		}
		d := &net.Dialer{Timeout: dialTimeout}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}

// Client builds an HTTP client that only ever connects to public hosts (via the
// dial-time guard), bounds the time, and re-validates every redirect hop. When
// allowPrivate is true the IP check is skipped (self-host LAN targets).
func Client(timeout time.Duration, allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: DialContext(allowPrivate), MaxIdleConns: 4},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil // each hop still dials through the guard
		},
	}
}

// Dialer returns a *net.Dialer whose Control hook validates each resolved
// address before the socket connects (DNS-rebinding-safe: Control runs on the
// concrete IP that will be dialed), refusing non-public IPs unless allowPrivate.
// It is meant for libraries that accept a *net.Dialer rather than a DialContext
// func — e.g. go-imap's imapclient.Options.Dialer.
func Dialer(timeout time.Duration, allowPrivate bool) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if allowPrivate {
		return d
	}
	d.Control = func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			// Control always receives a resolved IP:port; a non-IP here is
			// unexpected — fail closed rather than connect blind.
			return fmt.Errorf("refusing to connect to unresolved address %q", address)
		}
		if IsDisallowedIP(ip) {
			return fmt.Errorf("refusing to connect to non-public address %s", ip)
		}
		return nil
	}
	return d
}

// ValidateURL parses raw, enforces the given schemes (default http/https), and —
// unless allowPrivate — fails fast if the host is a non-public IP literal.
// It is defence in depth: the dial-time guard is authoritative (it also catches
// hostnames that resolve internal), but this rejects obviously-bad URLs (wrong
// scheme, literal internal IP) before any request leaves the host.
func ValidateURL(raw string, allowPrivate bool, schemes ...string) (*url.URL, error) {
	if len(schemes) == 0 {
		schemes = []string{"http", "https"}
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	allowed := false
	for _, s := range schemes {
		if u.Scheme == s {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("only %s URLs are allowed (got %q)", strings.Join(schemes, "/"), u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	if !allowPrivate {
		if ip := net.ParseIP(host); ip != nil && IsDisallowedIP(ip) {
			return nil, fmt.Errorf("refusing non-public address %s", host)
		}
	}
	return u, nil
}
