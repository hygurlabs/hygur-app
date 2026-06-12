package store

import (
	"context"
	"database/sql"
	"time"
)

// Dismissed contradictions: the "seen it, hide it" set. A contradiction is keyed
// by a stable hash (computed in package contradict) so a dismissal survives the
// ~hourly recomputation as long as the same divergence persists. Per-tenant DB,
// so the set is naturally scoped to the tenant.

// DismissContradiction marks a contradiction key as dismissed (idempotent).
func (d *DB) DismissContradiction(ctx context.Context, key string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO dismissed_contradictions (key) VALUES (?)`, key)
	return err
}

// UndismissContradiction restores a previously dismissed contradiction.
func (d *DB) UndismissContradiction(ctx context.Context, key string) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM dismissed_contradictions WHERE key = ?`, key)
	return err
}

// PutContradictionCache write-throughs the latest reconciled conflicts for a
// scope (raw JSON; the contradict types live a layer up to avoid an import
// cycle). "" = the all-mail+notes scope.
func (d *DB) PutContradictionCache(ctx context.Context, scope, conflictsJSON string, scanned int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx, `
INSERT INTO contradiction_cache (scope, conflicts, scanned, computed_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(scope) DO UPDATE SET conflicts = excluded.conflicts, scanned = excluded.scanned, computed_at = excluded.computed_at`,
		scope, conflictsJSON, scanned, now)
	return err
}

// GetContradictionCache returns the cached conflicts JSON for a scope and how
// long ago it was computed. found=false when the scope has never been computed.
func (d *DB) GetContradictionCache(ctx context.Context, scope string) (conflictsJSON string, scanned int, age time.Duration, found bool, err error) {
	var computed time.Time
	row := d.db.QueryRowContext(ctx,
		`SELECT conflicts, scanned, computed_at FROM contradiction_cache WHERE scope = ?`, scope)
	if e := row.Scan(&conflictsJSON, &scanned, &computed); e != nil {
		if e == sql.ErrNoRows {
			return "", 0, 0, false, nil
		}
		return "", 0, 0, false, e
	}
	return conflictsJSON, scanned, time.Since(computed), true, nil
}

// CachedVerdict is a reconcile verdict held in the per-cluster cache. Kept as
// plain strings so the store doesn't depend on package contradict.
type CachedVerdict struct {
	Kind   string // "conflict" | "supersedes" | "none"
	Reason string
}

// GetReconcileVerdicts loads the whole per-cluster verdict cache as a map keyed by
// cluster Key. The table is small (one row per distinct cluster ever judged) and
// recomputes are infrequent, so loading all is cheap and simplest.
func (d *DB) GetReconcileVerdicts(ctx context.Context) (map[string]CachedVerdict, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT cluster_key, kind, reason FROM reconcile_verdicts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CachedVerdict{}
	for rows.Next() {
		var k string
		var v CachedVerdict
		if err := rows.Scan(&k, &v.Kind, &v.Reason); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// PutReconcileVerdict caches one cluster's verdict (idempotent; a re-judge of the
// same Key overwrites). Stores 'none' too, so unchanged clusters are never re-judged.
func (d *DB) PutReconcileVerdict(ctx context.Context, clusterKey, kind, reason string) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO reconcile_verdicts (cluster_key, kind, reason) VALUES (?, ?, ?)
ON CONFLICT(cluster_key) DO UPDATE SET kind = excluded.kind, reason = excluded.reason`,
		clusterKey, kind, reason)
	return err
}

// DismissedContradictions returns the set of dismissed contradiction keys.
func (d *DB) DismissedContradictions(ctx context.Context) (map[string]bool, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT key FROM dismissed_contradictions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}
