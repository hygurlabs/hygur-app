package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/hygur/sidecar/internal/push"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

// maxPushSubscriptions caps stored devices per tenant (abuse guard).
const maxPushSubscriptions = 20

// PushHandler manages Web Push subscriptions + a manual test send. Every route
// returns 503 when no VAPID keypair is configured (push disabled).
type PushHandler struct {
	store  *store.DB
	sender *push.Sender
	logger zerolog.Logger
}

// NewPushHandler builds the push handler. A nil/unconfigured sender disables push.
func NewPushHandler(s *store.DB, sender *push.Sender, logger zerolog.Logger) *PushHandler {
	return &PushHandler{store: s, sender: sender, logger: logger.With().Str("handler", "push").Logger()}
}

type pushSubscribeReq struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (h *PushHandler) enabled() bool { return h.sender != nil && h.sender.Configured() }

// HandleSubscribe stores a browser push subscription (W3C PushSubscription JSON).
func (h *PushHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	if !h.enabled() {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	var req pushSubscribeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}
	// Anti-SSRF: only accept endpoints on a real browser push service (HTTPS +
	// allowlisted host), so the server can't be coerced into outbound requests.
	if !push.ValidEndpoint(req.Endpoint) {
		http.Error(w, "invalid endpoint", http.StatusBadRequest)
		return
	}
	// Cap subscriptions per tenant (abuse / push-amplification guard); a re-subscribe
	// of an existing endpoint is always allowed.
	if subs, err := h.store.ListPushSubscriptions(r.Context()); err == nil && len(subs) >= maxPushSubscriptions {
		known := false
		for _, s := range subs {
			if s.Endpoint == req.Endpoint {
				known = true
				break
			}
		}
		if !known {
			http.Error(w, "too many devices", http.StatusForbidden)
			return
		}
	}
	if err := h.store.UpsertPushSubscription(r.Context(), store.PushSubscription{
		Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth,
	}); err != nil {
		h.logger.Error().Err(err).Msg("store push subscription")
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleUnsubscribe removes a subscription by endpoint.
func (h *PushHandler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Endpoint == "" {
		http.Error(w, "invalid endpoint", http.StatusBadRequest)
		return
	}
	_ = h.store.DeletePushSubscription(r.Context(), req.Endpoint)
	w.WriteHeader(http.StatusNoContent)
}

// HandleTest sends a sample push to all subscriptions so the user can verify the
// setup without waiting for the nightly brief. Prunes endpoints reported gone.
func (h *PushHandler) HandleTest(w http.ResponseWriter, r *http.Request) {
	if !h.enabled() {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	subs, err := h.store.ListPushSubscriptions(r.Context())
	if err != nil {
		http.Error(w, "store failed", http.StatusInternalServerError)
		return
	}
	dead := h.sender.Send(r.Context(), pushSubs(subs), push.Notification{
		Title: "Hygur",
		Body:  "Notifications are on — your daily brief will land here.",
		Icon:  "/icon-192.png",
		Data:  map[string]any{"url": "/", "test": true},
	})
	for _, ep := range dead {
		_ = h.store.DeletePushSubscription(r.Context(), ep)
	}
	h.logger.Info().Int("subscriptions", len(subs)).Int("pruned", len(dead)).Msg("push test sent")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"sent": len(subs) - len(dead)})
}

func pushSubs(in []store.PushSubscription) []push.Subscription {
	out := make([]push.Subscription, len(in))
	for i, s := range in {
		out[i] = push.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}
	}
	return out
}
