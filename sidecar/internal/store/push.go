package store

import (
	"context"
	"time"
)

// PushSubscription is a browser web-push subscription (W3C Push API shape).
type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// UpsertPushSubscription stores (or refreshes) a web-push subscription, keyed by endpoint.
func (d *DB) UpsertPushSubscription(ctx context.Context, s PushSubscription) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO push_subscriptions(endpoint, p256dh, auth, created_at)
VALUES(?,?,?,?)
ON CONFLICT(endpoint) DO UPDATE SET p256dh=excluded.p256dh, auth=excluded.auth`,
		s.Endpoint, s.P256dh, s.Auth, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ListPushSubscriptions returns all stored web-push subscriptions.
func (d *DB) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT endpoint, p256dh, auth FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeletePushSubscription removes a subscription by endpoint (e.g. expired → 404/410).
func (d *DB) DeletePushSubscription(ctx context.Context, endpoint string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint)
	return err
}
