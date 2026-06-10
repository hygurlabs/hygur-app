package store

import "context"

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
