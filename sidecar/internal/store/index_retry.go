package store

import (
	"context"
	"fmt"
	"time"
)

// IndexRetry is one parked re-index attempt: a content unit (for mail, a thread)
// that failed to index for a transient reason (embedder down / timeout / ubatch
// overflow) and must be replayed before the next incremental sync, so a transient
// failure never becomes a silent permanent gap in the knowledge base
// (RELIABILITY_BACKLOG R1). For mail, ConnectorID is the provider ("gmail" /
// "proton" / …) and SourceRef is the thread ID.
type IndexRetry struct {
	ConnectorID   string
	AccountID     string
	SourceRef     string
	Reason        string
	Attempts      int
	LastError     string
	FirstFailedAt time.Time
	NextAttemptAt time.Time
}

// EnqueueIndexRetry records (or refreshes) a transient indexing failure. Keyed by
// (connector_id, account_id, source_ref) so re-failing the same unit UPDATES the
// existing row instead of inserting a duplicate; attempts and first_failed_at are
// preserved across refreshes — only BumpIndexRetry advances the attempt counter.
func (d *DB) EnqueueIndexRetry(ctx context.Context, connectorID, accountID, sourceRef, reason, lastError string, nextAttempt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO index_retry (connector_id, account_id, source_ref, reason, attempts, first_failed_at, next_attempt_at, last_error)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(connector_id, account_id, source_ref) DO UPDATE SET
			reason = excluded.reason,
			next_attempt_at = excluded.next_attempt_at,
			last_error = excluded.last_error
	`, connectorID, accountID, sourceRef, reason, now, nextAttempt.UTC().Format(time.RFC3339), lastError)
	if err != nil {
		return fmt.Errorf("enqueue index retry: %w", err)
	}
	return nil
}

// DueIndexRetries returns up to `limit` parked retries for (connector, account)
// whose next_attempt_at has passed, oldest first. RFC3339 UTC timestamps sort
// lexicographically, so the string comparison is a correct time comparison.
func (d *DB) DueIndexRetries(ctx context.Context, connectorID, accountID string, now time.Time, limit int) ([]IndexRetry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT connector_id, account_id, source_ref, reason, attempts, last_error, first_failed_at, next_attempt_at
		FROM index_retry
		WHERE connector_id = ? AND account_id = ? AND next_attempt_at <= ?
		ORDER BY next_attempt_at
		LIMIT ?
	`, connectorID, accountID, now.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, fmt.Errorf("query due index retries: %w", err)
	}
	defer rows.Close()

	var out []IndexRetry
	for rows.Next() {
		var r IndexRetry
		var first, next string
		if err := rows.Scan(&r.ConnectorID, &r.AccountID, &r.SourceRef, &r.Reason, &r.Attempts, &r.LastError, &first, &next); err != nil {
			return nil, fmt.Errorf("scan index retry: %w", err)
		}
		r.FirstFailedAt, _ = time.Parse(time.RFC3339, first)
		r.NextAttemptAt, _ = time.Parse(time.RFC3339, next)
		out = append(out, r)
	}
	return out, rows.Err()
}

// BumpIndexRetry advances the attempt counter and schedules the next try. Used
// when a parked retry fails again on drain.
func (d *DB) BumpIndexRetry(ctx context.Context, connectorID, accountID, sourceRef string, nextAttempt time.Time, lastError string) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE index_retry
		SET attempts = attempts + 1, next_attempt_at = ?, last_error = ?
		WHERE connector_id = ? AND account_id = ? AND source_ref = ?
	`, nextAttempt.UTC().Format(time.RFC3339), lastError, connectorID, accountID, sourceRef)
	if err != nil {
		return fmt.Errorf("bump index retry: %w", err)
	}
	return nil
}

// DeleteIndexRetry removes a parked retry — on success, permanent give-up, or a
// source that vanished from the server.
func (d *DB) DeleteIndexRetry(ctx context.Context, connectorID, accountID, sourceRef string) error {
	_, err := d.db.ExecContext(ctx, `
		DELETE FROM index_retry WHERE connector_id = ? AND account_id = ? AND source_ref = ?
	`, connectorID, accountID, sourceRef)
	if err != nil {
		return fmt.Errorf("delete index retry: %w", err)
	}
	return nil
}

// CountIndexRetry returns how many retries are parked for (connector, account).
func (d *DB) CountIndexRetry(ctx context.Context, connectorID, accountID string) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM index_retry WHERE connector_id = ? AND account_id = ?
	`, connectorID, accountID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count index retry: %w", err)
	}
	return n, nil
}
