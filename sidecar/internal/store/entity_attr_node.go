package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// AttrNode is one DETERMINED attribute of a KEYED ENTITY as an engram NODE with its context EDGES
// (GENERALIZATION_PLAN — the universal entity-anchor). Where figure_nodes anchors a labelled figure
// to a person/org entity, AttrNode anchors an attribute to a KEY: the vehicle's plate, the bike's
// serial, the cat's chip, the phone's IMEI. The NODE is (attribute, value); the EDGES are key_norm
// (the anchor — the canonical key), key_type + kind (which family of key), and content_id (source).
// It reuses the same idioms as FigureNode (idempotent replace-per-document, entity graph keys,
// temporal supersession on read) rather than a parallel truth store. The KEY anchor is what keeps
// "distinct entities declined": only claims anchored to THIS key can fill THIS key's attribute.
type AttrNode struct {
	ContentID string    // source edge — the document the attribute was found in
	KeyNorm   string    // anchor edge — the canonical key ("gt 139 rr"), the entity the attribute is OF
	KeyType   string    // "plate" (extensible: "serial", "chip", "imei"…)
	Kind      string    // "vehicle" (extensible: "bike", "cat", "phone"…)
	Attribute string    // normalized attribute key ("modele", "immatriculation")
	AttrRaw   string    // attribute as written ("modèle"), for display
	Value     string    // normalized value key (agreement/grouping key)
	ValueRaw  string    // value as written ("Tesla Model X 2023"), for display
	Prox      float64   // attribution strength of the key anchor (0,1]
	DocDate   time.Time // source document date (knowledge_items.created_at) — for temporal supersession
}

// ReplaceAttrNodes replaces every keyed-attribute node for contentID (clear + reinsert) so a re-run
// is idempotent. One transaction. Mirrors ReplaceFigureNodes.
func (d *DB) ReplaceAttrNodes(ctx context.Context, contentID string, nodes []AttrNode) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_attr_nodes WHERE content_id = ?`, contentID); err != nil {
		tx.Rollback()
		return err
	}
	for _, n := range nodes {
		if n.KeyNorm == "" || n.Attribute == "" || n.Value == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO entity_attr_nodes
			 (content_id, key_norm, key_type, kind, attribute, attr_raw, value, value_raw, prox)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			contentID, n.KeyNorm, n.KeyType, n.Kind, n.Attribute, n.AttrRaw, n.Value, n.ValueRaw, n.Prox); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// PlateNormsByModelLike returns the distinct plate keys whose determined modèle/description VALUE
// contains any of the given lowercased tokens — the model → plate traversal that lets "l'assurance de
// la Zoé" reach the plate-anchored assureur without the user ever typing the plate. Each token must be
// ≥3 chars (the caller filters stopwords); matching is a case-insensitive substring on the stored
// (already lowercased) value. Bounded to the modèle/description attributes so it never joins on an
// unrelated attribute. Empty/short tokens → no rows.
func (d *DB) PlateNormsByModelLike(ctx context.Context, tokens []string) ([]string, error) {
	conds := make([]string, 0, len(tokens))
	args := make([]any, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) < 3 {
			continue
		}
		conds = append(conds, "value LIKE ?")
		args = append(args, "%"+t+"%")
	}
	if len(conds) == 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT key_norm FROM entity_attr_nodes
		 WHERE key_type = 'plate' AND attribute IN ('modele','description') AND (`+strings.Join(conds, " OR ")+`)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AttrNodesForKeys returns every keyed-attribute node anchored (key edge) to ANY of the given key
// norms, across all attributes — the candidate set the determined-facts resolver traverses so a
// keyed entity's attributes are available in-context. Empty keys → no rows.
func (d *DB) AttrNodesForKeys(ctx context.Context, keyNorms []string) ([]AttrNode, error) {
	seen := map[string]bool{}
	ph := make([]string, 0, len(keyNorms))
	args := make([]any, 0, len(keyNorms))
	for _, k := range keyNorms {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		ph = append(ph, "?")
		args = append(args, k)
	}
	if len(ph) == 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT a.content_id, a.key_norm, a.key_type, a.kind, a.attribute, a.attr_raw, a.value, a.value_raw, a.prox, k.created_at
		 FROM entity_attr_nodes a LEFT JOIN knowledge_items k ON k.content_id = a.content_id
		 WHERE a.key_norm IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttrNode
	for rows.Next() {
		var n AttrNode
		var docDate sql.NullTime
		if err := rows.Scan(&n.ContentID, &n.KeyNorm, &n.KeyType, &n.Kind, &n.Attribute, &n.AttrRaw,
			&n.Value, &n.ValueRaw, &n.Prox, &docDate); err != nil {
			return nil, err
		}
		if docDate.Valid {
			n.DocDate = docDate.Time
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
