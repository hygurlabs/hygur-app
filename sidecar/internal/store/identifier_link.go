package store

import (
	"context"
	"strings"
)

// ResolvePersonNorms maps a name query to the person entity norms that contain it — the
// graph indexes people by full name ("alice bernard"), so a bare "alice" must resolve to
// them before a lookup can find their identifier neighbors. Returns distinct ner_person
// norms whose token stream contains the query as a WHOLE TOKEN or a contiguous full-name
// phrase — never as an arbitrary substring. A bare-substring match (the old behavior) let a
// partial name pool several distinct people (a shared surname pooled everyone who carries it;
// a bare first name pooled every full-name variant): the caller then returned one of them at
// high confidence with no ambiguity signal. Entity norms are single-space-separated
// alphanumeric tokens (NormKey), so space-padding both sides turns LIKE into a word-boundary
// match: ' bernard ' matches "alice bernard" but ' bern ' does not.
func (d *DB) ResolvePersonNorms(ctx context.Context, query string, limit int) ([]string, error) {
	// Collapse to single-spaced lowercase tokens so the space-padding boundary holds even if
	// the caller passes a raw (un-normalized) query.
	q := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT entity_norm FROM entity_mentions
		 WHERE attribute = 'ner_person'
		   AND (' ' || entity_norm || ' ') LIKE ('% ' || ? || ' %')
		 LIMIT ?`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IdentifierLink is a proximity-confident (person ↔ typed identifier) association found in
// one document — emitted only when the pairing is unambiguous there (nearest same-type,
// clear runner-up margin, mutual). Prox ∈ (0,1]. The lookup aggregates these across docs on
// top of the NPMI edge to break the family-member tie doc-level co-occurrence cannot.
type IdentifierLink struct {
	PersonNorm string
	IDNorm     string
	IDType     string
	Prox       float64
}

// ReplaceIdentifierLinks replaces every proximity link for contentID (clear + reinsert), so
// a re-run is idempotent. One transaction.
func (d *DB) ReplaceIdentifierLinks(ctx context.Context, contentID string, links []IdentifierLink) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_identifier_link WHERE content_id = ?`, contentID); err != nil {
		tx.Rollback()
		return err
	}
	for _, l := range links {
		if l.PersonNorm == "" || l.IDNorm == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO entity_identifier_link (content_id, person_norm, id_norm, id_type, prox)
			 VALUES (?, ?, ?, ?, ?)`,
			contentID, l.PersonNorm, l.IDNorm, l.IDType, l.Prox); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// IdentifierLinksForID returns the proximity links recorded for one identifier value across
// all documents — which persons it was unambiguously tied to, and how strongly.
func (d *DB) IdentifierLinksForID(ctx context.Context, idNorm string) ([]IdentifierLink, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT person_norm, id_norm, id_type, prox FROM entity_identifier_link WHERE id_norm = ?`, idNorm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdentifierLink
	for rows.Next() {
		var l IdentifierLink
		if err := rows.Scan(&l.PersonNorm, &l.IDNorm, &l.IDType, &l.Prox); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
