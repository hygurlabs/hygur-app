package store

import (
	"context"
	"strings"
	"time"
)

// MeetingNode is one meeting-TIME assertion as an ENGRAM NODE with its typed CONTEXT EDGES — the
// meeting-time analogue of FigureNode (contradiction-aware rendez-vous). The NODE is the datetime
// (When); the EDGES are EntityNorm (whom the meeting is with — the same canonical entity key the
// figure/identifier graphs use), Source (email | calendar) and ContentID (the message / calendar
// event). AssertedAt is the assertion timestamp the C7 supersession mechanism orders "latest wins"
// by, so a newer email time supersedes a stale calendar time and the disagreement becomes a
// cross-source contradiction. Written at ingest (email extractor + calendar sync); resolved by
// internal/rendezvous via the SAME figure.ResolveTemporal traversal.
type MeetingNode struct {
	ContentID  string    // source edge — the message / calendar event the time was found in
	EntityNorm string    // entity edge — the folded person/org norm the meeting is with
	When       time.Time // the datetime value node
	Source     string    // source channel — "email" | "calendar"
	AssertedAt time.Time // assertion timestamp — when this source stated this time
	Title      string    // source display title, for citation
}

// ReplaceMeetingNodes replaces every meeting node for contentID (clear + reinsert) so a re-run is
// idempotent. One transaction. Mirrors ReplaceFigureNodes.
func (d *DB) ReplaceMeetingNodes(ctx context.Context, contentID string, nodes []MeetingNode) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM meeting_nodes WHERE content_id = ?`, contentID); err != nil {
		tx.Rollback()
		return err
	}
	for _, n := range nodes {
		if n.EntityNorm == "" || n.When.IsZero() || n.Source == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO meeting_nodes
			 (content_id, entity_norm, when_utc, source, asserted_at, title)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			contentID, n.EntityNorm, n.When.UTC().Format(time.RFC3339), n.Source,
			n.AssertedAt.UTC().Format(time.RFC3339), n.Title); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MeetingNodesForEntities returns every meeting node attached (entity edge) to ANY of the given
// entity norms — the candidate set the rendez-vous resolver traverses. Empty norms → no rows.
func (d *DB) MeetingNodesForEntities(ctx context.Context, norms []string) ([]MeetingNode, error) {
	seen := map[string]bool{}
	ph := make([]string, 0, len(norms))
	args := make([]any, 0, len(norms))
	for _, n := range norms {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		ph = append(ph, "?")
		args = append(args, n)
	}
	if len(ph) == 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT content_id, entity_norm, when_utc, source, asserted_at, title
		 FROM meeting_nodes WHERE entity_norm IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MeetingNode
	for rows.Next() {
		var n MeetingNode
		var whenStr, assertedStr string
		if err := rows.Scan(&n.ContentID, &n.EntityNorm, &whenStr, &n.Source, &assertedStr, &n.Title); err != nil {
			return nil, err
		}
		if t, e := time.Parse(time.RFC3339, whenStr); e == nil {
			n.When = t
		}
		if t, e := time.Parse(time.RFC3339, assertedStr); e == nil {
			n.AssertedAt = t
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
