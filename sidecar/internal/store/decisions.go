package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Decision is a decision/commitment record. The statement (title), rationale
// (body), tags and project live on the underlying knowledge_item
// (source_type="decision"); the decision state (status, the date it was decided,
// the source ids that ground it) lives in decision_attrs. ID is the
// knowledge_item content_id ("decision:<uuid>").
type Decision struct {
	ID         string   `json:"id"`
	Statement  string   `json:"statement"`
	Rationale  string   `json:"rationale"`
	Status     string   `json:"status"` // "proposed" | "standing" | "superseded"
	DecidedOn  string   `json:"decided_on,omitempty"`
	SourceRefs []string `json:"source_refs"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

// Decision status values (decision_attrs.status). Canonical home — prefer these
// over bare literals so the authority layer and the write-back agree on spelling.
const (
	DecisionProposed   = "proposed"   // detected by the nightly scan, awaiting confirmation (not yet authoritative)
	DecisionStanding   = "standing"   // active, user-confirmed — "fait foi"
	DecisionSuperseded = "superseded" // no longer holds
)

// NormalizeStatement collapses whitespace and lowercases a decision statement so
// two statements that differ only in spacing/case compare equal. Shared by the
// dedup key and the duplicate-cleanup grouping so they agree on identity.
func NormalizeStatement(statement string) string {
	return strings.ToLower(strings.Join(strings.Fields(statement), " "))
}

// DecisionDedupKey is the stable key that makes the nightly scan idempotent: the
// same decision (same source scope + same statement) is never re-proposed. The
// sourceRef is the dedup SCOPE — the caller passes the source item's content
// HASH when known (so two ingested copies of the same content, each under its own
// content_id, collapse to one decision) and falls back to the content_id
// otherwise. Empty for manually-logged decisions (no dedup).
func DecisionDedupKey(sourceRef, statement string) string {
	sum := sha256.Sum256([]byte(sourceRef + "\n" + NormalizeStatement(statement)))
	return hex.EncodeToString(sum[:])
}

// UpsertDecisionAttrs writes the decision state for a content id, stamping
// updated_at. status defaults to "standing" when blank. sourceRefs is stored as
// a JSON array; dedupKey is empty for manual decisions.
func (d *DB) UpsertDecisionAttrs(ctx context.Context, contentID, status, decidedOn string, sourceRefs []string, dedupKey string) error {
	if strings.TrimSpace(status) == "" {
		status = "standing"
	}
	if sourceRefs == nil {
		sourceRefs = []string{}
	}
	refsJSON, err := json.Marshal(sourceRefs)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = d.db.ExecContext(ctx, `
INSERT INTO decision_attrs (content_id, status, decided_on, source_refs, dedup_key, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(content_id) DO UPDATE SET status = excluded.status, decided_on = excluded.decided_on, source_refs = excluded.source_refs, updated_at = excluded.updated_at`,
		contentID, status, decidedOn, string(refsJSON), dedupKey, now, now)
	return err
}

// DecisionStatuses returns content_id → status for the given ids that carry a
// decision_attrs row. Ids without a row (i.e. non-decision captures) are simply
// absent from the map. Used by the authority layer to tag retrieval results in a
// single batch query rather than N point lookups.
func (d *DB) DecisionStatuses(ctx context.Context, contentIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(contentIDs))
	if len(contentIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(contentIDs))
	args := make([]any, len(contentIDs))
	for i, id := range contentIDs {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT content_id, status FROM decision_attrs WHERE content_id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, st string
		if err := rows.Scan(&cid, &st); err != nil {
			return nil, err
		}
		out[cid] = st
	}
	return out, rows.Err()
}

// SetDecisionStatus updates only the status (proposed→standing on confirm,
// standing→superseded), stamping updated_at.
func (d *DB) SetDecisionStatus(ctx context.Context, contentID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.db.ExecContext(ctx,
		`UPDATE decision_attrs SET status = ?, updated_at = ? WHERE content_id = ?`,
		status, now, contentID)
	return err
}

// CountStandingDecisions counts confirmed (standing) decisions — a learning-gauge
// pillar (the user's psyché feedback: decisions they have confirmed).
func (d *DB) CountStandingDecisions(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_attrs WHERE status = 'standing'`).Scan(&n)
	return n, err
}

// DecisionDedupExists reports whether a decision with this dedup key already
// exists (any status) — the nightly scan's idempotency guard.
func (d *DB) DecisionDedupExists(ctx context.Context, dedupKey string) (bool, error) {
	if dedupKey == "" {
		return false, nil
	}
	var n int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decision_attrs WHERE dedup_key = ?`, dedupKey).Scan(&n)
	return n > 0, err
}

// decisionSelect composes a decision from its knowledge_item + decision_attrs.
const decisionSelect = `
SELECT ki.content_id, ki.title, ki.normalized_text, da.status, da.decided_on, da.source_refs, ki.created_at, ki.updated_at
FROM knowledge_items ki
JOIN decision_attrs da ON da.content_id = ki.content_id`

func scanDecision(rows interface{ Scan(...any) error }) (*Decision, error) {
	var d Decision
	var refsJSON string
	var created, updated time.Time
	if err := rows.Scan(&d.ID, &d.Statement, &d.Rationale, &d.Status, &d.DecidedOn, &refsJSON, &created, &updated); err != nil {
		return nil, err
	}
	d.SourceRefs = []string{}
	if strings.TrimSpace(refsJSON) != "" {
		_ = json.Unmarshal([]byte(refsJSON), &d.SourceRefs)
	}
	d.CreatedAt = created.UTC().Format(time.RFC3339)
	d.UpdatedAt = updated.UTC().Format(time.RFC3339)
	return &d, nil
}

// ListDecisions returns decisions, optionally filtered by project (via
// project_links) and status. Proposed first (they need attention), then standing,
// then superseded; within a status, most recently decided/created first.
func (d *DB) ListDecisions(ctx context.Context, projectID, status string) ([]*Decision, error) {
	q := decisionSelect
	var conds []string
	var args []any
	if projectID != "" {
		q += " JOIN project_links pl ON pl.content_id = ki.content_id"
		conds = append(conds, "pl.project_id = ?")
		args = append(args, projectID)
	}
	if status != "" {
		conds = append(conds, "da.status = ?")
		args = append(args, status)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += ` ORDER BY CASE da.status WHEN 'proposed' THEN 0 WHEN 'standing' THEN 1 ELSE 2 END ASC,
	       (da.decided_on = '') ASC, da.decided_on DESC, ki.created_at DESC`

	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Decision
	for rows.Next() {
		dec, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dec)
	}
	return out, rows.Err()
}

// GetDecision loads one decision by content id; returns (nil, nil) when not found.
func (d *DB) GetDecision(ctx context.Context, id string) (*Decision, error) {
	dec, err := scanDecision(d.db.QueryRowContext(ctx, decisionSelect+" WHERE ki.content_id = ?", id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return dec, nil
}

// DecisionDupGroup is one set of scan-created decisions that collapsed onto the
// same content: the same normalized statement grounded in source items that
// share a content_hash (identical ingested content — e.g. the same attachment
// sent twice, each stored under its own content_id). Keep is the earliest-created
// survivor; Delete are the later duplicates to remove.
type DecisionDupGroup struct {
	StatementHash string   // sha256 of the normalized statement — a PII-safe id (no statement text leaks)
	ContentHash   string   // the shared source content_hash the copies collapsed on
	Keep          string   // survivor decision content_id (earliest created)
	Delete        []string // duplicate decision content_ids to delete (later-created)
}

// FindDuplicateDecisions returns the groups of decisions that are duplicates of
// one another because their source items share a content_hash AND their
// statements are identical (whitespace/case-insensitive). Read-only — the caller
// verifies the set before deleting (fail-closed). Fail-closed on scope, too: a
// decision whose source content_hash is unknown ('') is NEVER grouped, so two
// genuinely distinct decisions can't be collapsed.
func (d *DB) FindDuplicateDecisions(ctx context.Context) ([]DecisionDupGroup, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT ki.content_id, ki.title,
       COALESCE(json_extract(src.metadata, '$.content_hash'), '') AS chash
FROM knowledge_items ki
JOIN decision_attrs da ON da.content_id = ki.content_id
LEFT JOIN knowledge_items src ON src.content_id = json_extract(da.source_refs, '$[0]')
WHERE ki.source_type = 'decision'
ORDER BY ki.created_at ASC, ki.content_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type key struct{ stmt, hash string }
	order := []key{}
	groups := map[key][]string{}
	for rows.Next() {
		var id, title, chash string
		if err := rows.Scan(&id, &title, &chash); err != nil {
			return nil, err
		}
		if strings.TrimSpace(chash) == "" {
			continue // fail-closed: no shared content scope → never a duplicate
		}
		k := key{stmt: NormalizeStatement(title), hash: chash}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], id) // created_at ASC → first is earliest
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []DecisionDupGroup
	for _, k := range order {
		ids := groups[k]
		if len(ids) < 2 {
			continue
		}
		sum := sha256.Sum256([]byte(k.stmt))
		out = append(out, DecisionDupGroup{
			StatementHash: hex.EncodeToString(sum[:]),
			ContentHash:   k.hash,
			Keep:          ids[0],
			Delete:        ids[1:],
		})
	}
	return out, nil
}

// DeleteDecisions removes the given decision content_ids in one transaction: the
// derived item_norm rows (no FK) plus the knowledge_items rows, whose ON DELETE
// CASCADE fans out to decision_attrs / project_links / chunks. Foreign keys are
// enforced for this connection so the cascade fires even when opened via the CLI
// (which defaults them off). Returns the number of decisions deleted.
func (d *DB) DeleteDecisions(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := d.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return 0, err
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	in := "(" + strings.Join(ph, ",") + ")"

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_norm WHERE content_id IN `+in, args...); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM knowledge_items WHERE content_id IN `+in, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetAppSetting reads a value from the generic app_settings key/value store;
// returns "" when the key is absent.
func (d *DB) GetAppSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetAppSetting UPSERTs a value into the generic app_settings key/value store.
func (d *DB) SetAppSetting(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
