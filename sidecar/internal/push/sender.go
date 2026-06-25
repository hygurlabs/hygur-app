// Package push delivers Web Push notifications (browser notifications that work
// when the tab is closed), signed with the app's VAPID keys. Pattern ported from
// CompliMetric's Node notificationService (prune subscriptions on 404/410).
package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog"
)

// hostOf returns just the host of a push endpoint (never log the full endpoint —
// it contains the per-device secret token).
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return "?"
}

// allowedPushHostSuffixes are the real browser push services. Restricting the
// subscription endpoint to these neutralises SSRF: a caller cannot register an
// internal/metadata URL (its host isn't an allowed push service), so the server
// never makes an outbound request to attacker-chosen internal addresses.
var allowedPushHostSuffixes = []string{
	"fcm.googleapis.com",        // Chrome / FCM (exact host — not all of googleapis.com)
	"push.services.mozilla.com", // Firefox
	"notify.windows.com",        // Edge / Windows (WNS)
	"push.apple.com",            // Safari / Apple
}

// ValidEndpoint reports whether raw is a plausible browser push endpoint: HTTPS
// and a host belonging to a known push service. Used at subscribe time (and
// defensively before send) to prevent SSRF via the stored endpoint URL.
func ValidEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, s := range allowedPushHostSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// pushHTTPClient never follows redirects (a push host must not bounce us to an
// internal URL) and has a bounded timeout.
var pushHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Notification is the JSON payload the service worker receives (sw.js reads it).
type Notification struct {
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Icon  string         `json:"icon,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// Subscription mirrors a stored web-push subscription.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Sender delivers notifications signed with the VAPID keypair. A nil or
// unconfigured Sender (no keys) makes Send a no-op — push is simply disabled.
type Sender struct {
	publicKey  string
	privateKey string
	subscriber string
	logger     zerolog.Logger
}

// NewSender builds a Sender. subscriber is the VAPID "sub" (a mailto: or URL);
// it defaults to a mailto when empty.
func NewSender(publicKey, privateKey, subscriber string, logger zerolog.Logger) *Sender {
	if subscriber == "" {
		subscriber = "mailto:admin@hygur.ai"
	}
	return &Sender{
		publicKey:  publicKey,
		privateKey: privateKey,
		subscriber: subscriber,
		logger:     logger.With().Str("component", "push").Logger(),
	}
}

// Configured reports whether a VAPID keypair is set (push enabled).
func (s *Sender) Configured() bool {
	return s != nil && s.publicKey != "" && s.privateKey != ""
}

// PublicKey returns the VAPID public key (shared with clients via /config).
func (s *Sender) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.publicKey
}

// Send delivers n to every subscription and returns the endpoints the push
// service reports as gone (HTTP 404/410) so the caller can prune them. A
// non-configured Sender or empty list is a no-op.
func (s *Sender) Send(_ context.Context, subs []Subscription, n Notification) (dead []string) {
	if !s.Configured() || len(subs) == 0 {
		return nil
	}
	payload, err := json.Marshal(n)
	if err != nil {
		return nil
	}
	opts := &webpush.Options{
		HTTPClient:      pushHTTPClient,
		Subscriber:      s.subscriber,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             86400,
	}
	for _, sub := range subs {
		if !ValidEndpoint(sub.Endpoint) {
			continue // defence-in-depth: never POST to a non-push-service host
		}
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, opts)
		if err != nil {
			s.logger.Warn().Err(err).Str("host", hostOf(sub.Endpoint)).Msg("web push: send error")
			continue
		}
		switch {
		case resp.StatusCode == 404 || resp.StatusCode == 410:
			dead = append(dead, sub.Endpoint)
		case resp.StatusCode >= 300:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			s.logger.Warn().Int("status", resp.StatusCode).Str("host", hostOf(sub.Endpoint)).
				Str("body", strings.TrimSpace(string(body))).Msg("web push: rejected by push service")
		default:
			s.logger.Info().Int("status", resp.StatusCode).Str("host", hostOf(sub.Endpoint)).
				Msg("web push: accepted by push service")
		}
		_ = resp.Body.Close()
	}
	return dead
}
