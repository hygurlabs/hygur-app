package store

import (
	"context"
	"fmt"
	"strings"
)

// EntityMention is one (entity, attribute) an item asserts a claim about. The
// normalized form (EntityNorm) is the lookup key shared with the contradiction
// layer; EntityRaw is kept for display/debug. Populated from the cached claims
// (extracted_claims) — see ingest.BackfillEntityIndex / applyItemClaims.
type EntityMention struct {
	EntityNorm string
	EntityRaw  string
	Attribute  string
	AssertedAt string
}

// ReplaceEntityMentions replaces all entity_mentions rows for contentID in one
// transaction (delete-then-insert), so re-indexing an item is idempotent. An
// empty mentions slice just clears the item's rows. Rows with an empty
// EntityNorm are skipped; duplicate (norm, attribute) pairs collapse to one.
func (d *DB) ReplaceEntityMentions(ctx context.Context, contentID string, mentions []EntityMention) error {
	if d == nil || d.db == nil || strings.TrimSpace(contentID) == "" {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("entity mentions: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_mentions WHERE content_id = ?`, contentID); err != nil {
		return fmt.Errorf("entity mentions: clear: %w", err)
	}
	seen := make(map[string]bool, len(mentions))
	for _, m := range mentions {
		if strings.TrimSpace(m.EntityNorm) == "" {
			continue
		}
		key := m.EntityNorm + "\x1f" + m.Attribute
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO entity_mentions (entity_norm, entity_raw, content_id, attribute, asserted_at)
			 VALUES (?, ?, ?, ?, ?)`,
			m.EntityNorm, m.EntityRaw, contentID, m.Attribute, m.AssertedAt); err != nil {
			return fmt.Errorf("entity mentions: insert: %w", err)
		}
	}
	return tx.Commit()
}

// EntityMentionContentIDs returns the distinct content_ids whose claims mention
// any of the normalized entities, most-recently-asserted first, capped at limit
// (default 200). The norms slice is the brick-1 lookup key set — an embedding
// synonymy pass widens it before the call. Empty norms yields nil (no lookup).
func (d *DB) EntityMentionContentIDs(ctx context.Context, norms []string, limit int) ([]string, error) {
	if d == nil || d.db == nil {
		return nil, nil
	}
	clean := make([]string, 0, len(norms))
	for _, n := range norms {
		if strings.TrimSpace(n) != "" {
			clean = append(clean, n)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	query := `SELECT content_id FROM entity_mentions
	          WHERE entity_norm IN (` + placeholders + `)
	          GROUP BY content_id
	          ORDER BY MAX(asserted_at) DESC
	          LIMIT ?`
	args := make([]any, 0, len(clean)+1)
	for _, n := range clean {
		args = append(args, n)
	}
	args = append(args, limit)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("entity mentions: query: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			return nil, fmt.Errorf("entity mentions: scan: %w", err)
		}
		out = append(out, cid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity mentions: iterate: %w", err)
	}
	return out, nil
}

// EntityNormsMatching returns, among the given candidate norms, those that exist in
// the entity index, mapped to their mention count. Used by deterministic query→entity
// detection: more mentions = more central to the corpus, so it anchors better.
func (d *DB) EntityNormsMatching(ctx context.Context, norms []string) (map[string]int, error) {
	out := make(map[string]int, len(norms))
	if len(norms) == 0 {
		return out, nil
	}
	ph := make([]string, len(norms))
	args := make([]any, len(norms))
	for i, n := range norms {
		ph[i] = "?"
		args[i] = n
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, COUNT(*) FROM entity_mentions WHERE entity_norm IN (`+strings.Join(ph, ",")+`) GROUP BY entity_norm`, args...)
	if err != nil {
		return nil, fmt.Errorf("entity norms matching: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var c int
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, rows.Err()
}

// SubjectStat is one discovered subject: its norm, dominant NER type, and how many
// distinct items mention it (its centrality).
type SubjectStat struct {
	Norm     string `json:"norm"`
	Type     string `json:"type,omitempty"`
	Mentions int    `json:"mentions"`
}

// genericSubjectNorms are salutations / forms of address that NER mislabels as people
// ("Bonjour", "Madame", …). Normalized (lowercase, accent-free). PII-free by design —
// no real names here. Excluded from the discovered-subjects list.
var genericSubjectNorms = []string{
	"bonjour", "bonsoir", "salut", "coucou", "hi", "hello", "hey", "dear",
	"madame", "madam", "monsieur", "mademoiselle", "mesdames", "messieurs",
	"mme", "mlle", "mr", "mrs", "ms", "miss", "sir", "cher", "chere", "chers", "cheres", "dr",
}

// TopSubjects returns the most central real subjects (person/org/project — NOT topics,
// which are broad tags) by the number of distinct items mentioning them, minus generic
// salutations NER mislabels as people. The discovered-subjects list for the Engram index.
func (d *DB) TopSubjects(ctx context.Context, limit int) ([]SubjectStat, error) {
	if limit <= 0 {
		limit = 50
	}
	args := make([]any, 0, len(genericSubjectNorms)+1)
	ph := make([]string, len(genericSubjectNorms))
	for i, n := range genericSubjectNorms {
		ph[i] = "?"
		args = append(args, n)
	}
	args = append(args, limit*2) // buffer: some rows pass the WHERE but are topic-dominant, filtered below
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, COUNT(DISTINCT content_id) AS c FROM entity_mentions
		 WHERE attribute IN ('ner_person', 'ner_org', 'ner_project')
		   AND entity_norm NOT IN (`+strings.Join(ph, ",")+`)
		 GROUP BY entity_norm ORDER BY c DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("top subjects: %w", err)
	}
	defer rows.Close()
	var out []SubjectStat
	var norms []string
	for rows.Next() {
		var s SubjectStat
		if err := rows.Scan(&s.Norm, &s.Mentions); err != nil {
			return nil, err
		}
		out = append(out, s)
		norms = append(norms, s.Norm)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	types, err := d.EntityDominantTypes(ctx, norms)
	if err != nil {
		return nil, err
	}
	// Keep only entities whose DOMINANT type is a real subject kind: an entity with a
	// stray person/org tag but mostly a topic is still a topic. Then cap to limit.
	filtered := out[:0]
	for i := range out {
		switch types[out[i].Norm] {
		case "person", "org", "project":
			out[i].Type = types[out[i].Norm]
			filtered = append(filtered, out[i])
		}
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

// EntityDominantTypes returns, for each given norm, its dominant NER type
// (person/org/project/topic — the ner_* attribute with the most mentions, prefix
// stripped) or "" when the norm is only seen through claims (no ner_* attribute).
// Batch form used to type Engram network neighbors so named entities can be preferred.
func (d *DB) EntityDominantTypes(ctx context.Context, norms []string) (map[string]string, error) {
	out := make(map[string]string)
	clean := make([]string, 0, len(norms))
	seen := make(map[string]bool, len(norms))
	for _, n := range norms {
		if strings.TrimSpace(n) != "" && !seen[n] {
			seen[n] = true
			clean = append(clean, n)
		}
	}
	if len(clean) == 0 {
		return out, nil
	}
	ph := make([]string, len(clean))
	args := make([]any, len(clean))
	for i, n := range clean {
		ph[i] = "?"
		args[i] = n
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, attribute, COUNT(*) FROM entity_mentions
		 WHERE entity_norm IN (`+strings.Join(ph, ",")+`) AND attribute LIKE 'ner_%'
		 GROUP BY entity_norm, attribute`, args...)
	if err != nil {
		return nil, fmt.Errorf("entity dominant types: %w", err)
	}
	defer rows.Close()
	best := make(map[string]int, len(clean))
	for rows.Next() {
		var norm, attr string
		var c int
		if err := rows.Scan(&norm, &attr, &c); err != nil {
			return nil, err
		}
		if c > best[norm] {
			best[norm] = c
			out[norm] = strings.TrimPrefix(attr, "ner_")
		}
	}
	return out, rows.Err()
}

// EntityAttributeCounts returns the attribute → mention-count distribution for one
// entity norm. Used to label a subject by its dominant NER tag (person/org/project/
// topic) in an Engram dossier; a norm seen only via claims has no ner_* attribute.
func (d *DB) EntityAttributeCounts(ctx context.Context, norm string) (map[string]int, error) {
	out := make(map[string]int)
	if d == nil || d.db == nil || strings.TrimSpace(norm) == "" {
		return out, nil
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT attribute, COUNT(*) FROM entity_mentions WHERE entity_norm = ? GROUP BY attribute`, norm)
	if err != nil {
		return nil, fmt.Errorf("entity attribute counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var attr string
		var c int
		if err := rows.Scan(&attr, &c); err != nil {
			return nil, err
		}
		out[attr] = c
	}
	return out, rows.Err()
}
