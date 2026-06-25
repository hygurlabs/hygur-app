// Package push delivers Web Push notifications (browser notifications that work
// when the tab is closed), signed with the app's VAPID keys. Pattern ported from
// CompliMetric's Node notificationService (prune subscriptions on 404/410).
package push

import (
	"context"
	"encoding/json"

	webpush "github.com/SherClockHolmes/webpush-go"
)

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
}

// NewSender builds a Sender. subscriber is the VAPID "sub" (a mailto: or URL);
// it defaults to a mailto when empty.
func NewSender(publicKey, privateKey, subscriber string) *Sender {
	if subscriber == "" {
		subscriber = "mailto:admin@hygur.ai"
	}
	return &Sender{publicKey: publicKey, privateKey: privateKey, subscriber: subscriber}
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
		Subscriber:      s.subscriber,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             86400,
	}
	for _, sub := range subs {
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, opts)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			dead = append(dead, sub.Endpoint)
		}
	}
	return dead
}
