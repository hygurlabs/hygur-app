package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// FigureNode is one labelled MONETARY figure as an ENGRAM NODE with its typed CONTEXT EDGES
// (FIGURES_TRUTH_PLAN §3, F1). The NODE is (value, unit); the EDGES are entity (whose figure),
// period, direction, and source (the document). It reuses the entity graph — entity_norm is the
// same canonical person/org key the identifier links use — rather than a parallel truth store.
// Emitted at ingest by the deterministic figure extractor + proximity attribution; resolved by a
// deterministic traversal (filter label+direction, order by period, pick latest / decline).
type FigureNode struct {
	ContentID  string // source edge — the document the figure was found in
	EntityNorm string // entity edge — the nearest person/org (the owner, for the founder's VAT)
	Label      string // normalized figure label ("vat")
	Value      string // canonical numeric value node ("7421.85")
	Raw        string // amount as written ("7 421,85"), for display
	Unit       string // unit edge ("EUR")
	Period     string // period edge ("2026-Q1", "2026-03", "2026") or ""
	Direction  string // direction edge ("payable"/"refund"/"advance"/"due") or ""
	// Medication is the qualifier edge of a DOSAGE figure (the drug the dose belongs to) — folded;
	// "" for a monetary figure. Filtered on resolution exactly like Direction (C7).
	Medication string
	// Frequency is a dosage's cadence, normalized to "N×/day" (or ""). A context edge, not a discriminant.
	Frequency string
	Prox      float64   // attribution strength of the entity edge (0,1]
	DocDate   time.Time // source document date (knowledge_items.created_at) — read-only, for temporal supersession
}

// ReplaceFigureNodes replaces every figure node for contentID (clear + reinsert) so a re-run is
// idempotent. One transaction. Mirrors ReplaceIdentifierLinks.
func (d *DB) ReplaceFigureNodes(ctx context.Context, contentID string, nodes []FigureNode) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM figure_nodes WHERE content_id = ?`, contentID); err != nil {
		tx.Rollback()
		return err
	}
	for _, n := range nodes {
		if n.EntityNorm == "" || n.Value == "" || n.Label == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO figure_nodes
			 (content_id, entity_norm, label, value, raw, unit, period, direction, medication, frequency, prox)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			contentID, n.EntityNorm, n.Label, n.Value, n.Raw, n.Unit, n.Period, n.Direction, n.Medication, n.Frequency, n.Prox); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// AllFigureNodesForEntities returns every figure node attached (entity edge) to ANY of the given
// entity norms, across ALL labels — the candidate set the determined-facts layer enumerates so a
// subject's figures are ALWAYS available in-context (FIGURES_TRUTH_PLAN pilier 1). Same shape as
// FigureNodesForEntities but without the label filter. Empty norms → no rows.
func (d *DB) AllFigureNodesForEntities(ctx context.Context, norms []string) ([]FigureNode, error) {
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
		`SELECT f.content_id, f.entity_norm, f.label, f.value, f.raw, f.unit, f.period, f.direction, f.medication, f.frequency, f.prox, k.created_at
		 FROM figure_nodes f LEFT JOIN knowledge_items k ON k.content_id = f.content_id
		 WHERE f.entity_norm IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFigureNodes(rows)
}

// FigureNodesForEntities returns every figure node of a given label attached (entity edge) to ANY
// of the given entity norms — the candidate set the resolver traverses. Precise (no ranking): the
// resolver applies direction filtering and period ordering. Empty norms/label → no rows.
func (d *DB) FigureNodesForEntities(ctx context.Context, norms []string, label string) ([]FigureNode, error) {
	if strings.TrimSpace(label) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	ph := make([]string, 0, len(norms))
	args := make([]any, 0, len(norms)+1)
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
	args = append(args, label)
	rows, err := d.db.QueryContext(ctx,
		`SELECT f.content_id, f.entity_norm, f.label, f.value, f.raw, f.unit, f.period, f.direction, f.medication, f.frequency, f.prox, k.created_at
		 FROM figure_nodes f LEFT JOIN knowledge_items k ON k.content_id = f.content_id
		 WHERE f.entity_norm IN (`+strings.Join(ph, ",")+`) AND f.label = ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFigureNodes(rows)
}

// scanFigureNodes scans the shared figure-node projection (node columns + the joined document date).
func scanFigureNodes(rows *sql.Rows) ([]FigureNode, error) {
	var out []FigureNode
	for rows.Next() {
		var n FigureNode
		var docDate sql.NullTime
		if err := rows.Scan(&n.ContentID, &n.EntityNorm, &n.Label, &n.Value, &n.Raw, &n.Unit,
			&n.Period, &n.Direction, &n.Medication, &n.Frequency, &n.Prox, &docDate); err != nil {
			return nil, err
		}
		if docDate.Valid {
			n.DocDate = docDate.Time
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
