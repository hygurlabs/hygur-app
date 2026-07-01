package store

import "context"

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
