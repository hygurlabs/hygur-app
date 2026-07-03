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
	ContentID  string // the document the link was found in (populated on read; the write side passes it separately)
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
		`SELECT content_id, person_norm, id_norm, id_type, prox FROM entity_identifier_link WHERE id_norm = ?`, idNorm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdentifierLink
	for rows.Next() {
		var l IdentifierLink
		if err := rows.Scan(&l.ContentID, &l.PersonNorm, &l.IDNorm, &l.IDType, &l.Prox); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// NationalNumbersByPersons returns, for each given person norm, the set of national_number
// values it is proximity-linked to (its OWN NISS). Feeds the father/son guard in
// DistinctPeopleGuarded so a father inside his son's full name is not merged into the son.
func (d *DB) NationalNumbersByPersons(ctx context.Context, norms []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(norms) == 0 {
		return out, nil
	}
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
		return out, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT person_norm, id_norm FROM entity_identifier_link
		 WHERE id_type = 'national_number' AND person_norm IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p, id string
		if err := rows.Scan(&p, &id); err != nil {
			return nil, err
		}
		out[p] = append(out[p], id)
	}
	return out, rows.Err()
}

// IdentifierTypesForPersons returns the distinct identifier types (national_number,
// enterprise_number, duns…) that any of the given person/org norms is proximity-linked
// to. This is the precise source for a dossier's Identity block: the id types a subject
// actually carries, independent of whether the (numeric, junk-looking) identifier value
// ranks inside the subject's top network neighbors.
func (d *DB) IdentifierTypesForPersons(ctx context.Context, norms []string) ([]string, error) {
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
		`SELECT DISTINCT id_type FROM entity_identifier_link
		 WHERE person_norm IN (`+strings.Join(ph, ",")+`) AND id_type <> ''`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PersonNormsContainingTokens returns distinct ner_person entity norms that contain ANY of the
// given whole (space-delimited) tokens — a bounded candidate set for owner recognition, which
// the caller narrows with an identity.Matcher. Word-boundary matched (space-padding), so 'l'
// matches "denis l" but not "petit". Capped to keep a read-time call bounded.
func (d *DB) PersonNormsContainingTokens(ctx context.Context, tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var conds []string
	var args []any
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		conds = append(conds, "(' ' || entity_norm || ' ') LIKE ('% ' || ? || ' %')")
		args = append(args, t)
	}
	if len(conds) == 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT entity_norm FROM entity_mentions
		 WHERE attribute = 'ner_person' AND (`+strings.Join(conds, " OR ")+`) LIMIT 500`, args...)
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
