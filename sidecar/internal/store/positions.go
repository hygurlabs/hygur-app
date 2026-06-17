package store

import (
	"context"
	"encoding/json"
	"time"
)

// Angle A-2b — the durable cache for the "standing positions" synopsis. It lives in
// the generic app_settings KV store (no new table), content-addressed by a fingerprint
// of the decision set so it regenerates only when the decisions change. The scheduler
// writes it; the digest reads/refreshes it; the chat path reads it cheaply.

// v2: second-person voice ("you"). Bumping the key invalidates the v1 cache so the
// summary regenerates with the new prompt rather than serving the stale text.
const positionsSynopsisKey = "positions_synopsis_v2"

type positionsSynopsisCache struct {
	Text        string `json:"text"`
	Fingerprint string `json:"fingerprint"`
	GeneratedAt string `json:"generated_at"`
}

// GetPositionsSynopsis returns the cached synopsis and the fingerprint of the
// decision set it was built from. found is false when nothing has been cached yet.
func (d *DB) GetPositionsSynopsis(ctx context.Context) (text, fingerprint string, found bool, err error) {
	js, err := d.GetAppSetting(ctx, positionsSynopsisKey)
	if err != nil || js == "" {
		return "", "", false, err
	}
	var c positionsSynopsisCache
	if json.Unmarshal([]byte(js), &c) != nil {
		return "", "", false, nil
	}
	return c.Text, c.Fingerprint, c.Text != "", nil
}

// PutPositionsSynopsis caches the synopsis with the fingerprint it was built from,
// stamped with the current time.
func (d *DB) PutPositionsSynopsis(ctx context.Context, text, fingerprint string) error {
	blob, err := json.Marshal(positionsSynopsisCache{
		Text: text, Fingerprint: fingerprint, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return d.SetAppSetting(ctx, positionsSynopsisKey, string(blob))
}
