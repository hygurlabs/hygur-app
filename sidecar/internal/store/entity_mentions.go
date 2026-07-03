package store

import (
	"context"
	"database/sql"
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
	Norm         string `json:"norm"`
	Raw          string `json:"raw,omitempty"` // a representative surface form, for client-side search
	Type         string `json:"type,omitempty"`
	Mentions     int    `json:"mentions"`
	LastActivity string `json:"last_activity,omitempty"` // MAX(asserted_at) across the subject's mentions — recency sort
}

// junkSubjectNorms are function words / greetings that must never be a subject (norms
// are stored lowercased + accent-stripped). PII-free generics only.
var junkSubjectNorms = map[string]bool{
	"bonjour": true, "bonsoir": true, "salut": true, "coucou": true, "hi": true,
	"hello": true, "hey": true, "dear": true, "madame": true, "madam": true,
	"monsieur": true, "mademoiselle": true, "mesdames": true, "messieurs": true,
	"mme": true, "mlle": true, "mrs": true, "miss": true, "sir": true,
	"cher": true, "chere": true, "chers": true, "cheres": true,
	"les": true, "des": true, "une": true, "aux": true, "cet": true, "cette": true,
	"ces": true, "the": true, "and": true, "but": true, "our": true, "your": true,
}

// leadingDeterminers mark a generic reference when they start a multi-word norm
// ("the report", "ce message", "le contrat").
var leadingDeterminers = map[string]bool{
	"the": true, "le": true, "la": true, "les": true, "l": true, "un": true,
	"une": true, "des": true, "ce": true, "cet": true, "cette": true, "ces": true,
}

// monthWords are month names (FR/EN, accent-free) — a norm containing one is a date,
// not a subject ("jeudi 21 mai 2026").
var monthWords = map[string]bool{
	"janvier": true, "fevrier": true, "mars": true, "avril": true, "mai": true, "juin": true,
	"juillet": true, "aout": true, "septembre": true, "octobre": true, "novembre": true, "decembre": true,
	"january": true, "february": true, "march": true, "april": true, "may": true, "june": true,
	"july": true, "august": true, "september": true, "october": true, "november": true, "december": true,
}

// isJunkToken reports whether a single word looks like a date year or a hex/UUID token
// (not part of an entity name).
func isJunkToken(w string) bool {
	if monthWords[w] {
		return true
	}
	if len(w) == 4 && (strings.HasPrefix(w, "19") || strings.HasPrefix(w, "20")) {
		allDigit := true
		for _, r := range w {
			if r < '0' || r > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			return true // a year
		}
	}
	if len(w) >= 8 { // long hex run → UUID / token fragment
		hex := true
		for _, r := range w {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				hex = false
				break
			}
		}
		if hex {
			return true
		}
	}
	return false
}

// IsJunkSubjectNorm reports whether a normalized entity norm is too generic or malformed
// to be a real subject: too short ("le"), a function word/greeting (also as the leading
// word — "bonjour x"), a personal email fragment ("x gmail com"), a determiner-led phrase
// ("ce message"), a date, a hex/UUID token, or an over-long sentence fragment. Shared by
// the discovered-subjects list and the Engram network. NER (the small model) mislabels
// all of these as people/orgs, so they must never surface as subjects.
func IsJunkSubjectNorm(norm string) bool {
	norm = strings.TrimSpace(norm)
	if len([]rune(norm)) < 3 {
		return true
	}
	if junkSubjectNorms[norm] || strings.Contains(norm, "gmail") {
		return true
	}
	fields := strings.Fields(norm)
	if len(fields) > 6 { // sentence fragment, not an entity name
		return true
	}
	if len(fields) > 0 && (junkSubjectNorms[fields[0]] || leadingDeterminers[fields[0]]) {
		return true // greeting/function-word/determiner-led ("bonjour x", "ce message")
	}
	for _, f := range fields {
		if isJunkToken(f) {
			return true // contains a date year, month, or hex/UUID token
		}
	}
	return false
}

// TopSubjects returns the most central real subjects (person/org/project — NOT topics,
// which are broad tags) by the number of distinct items mentioning them, minus generic
// salutations NER mislabels as people and the caller-supplied `exclude` norms (already
// normalized — the owner's own identity). The discovered-subjects list for the Engram.
func (d *DB) TopSubjects(ctx context.Context, limit int, exclude []string) ([]SubjectStat, error) {
	if limit <= 0 {
		limit = 50
	}
	// Exclude the owner's own norms (already normalized) in SQL; generic junk is filtered
	// in Go via IsJunkSubjectNorm.
	seen := make(map[string]bool)
	args := make([]any, 0, len(exclude)+1)
	var ph []string
	for _, n := range exclude {
		if n != "" && !seen[n] {
			seen[n] = true
			ph = append(ph, "?")
			args = append(args, n)
		}
	}
	notIn := ""
	if len(ph) > 0 {
		notIn = " AND entity_norm NOT IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, limit*3) // buffer: junk + topic-dominant rows are filtered below
	rows, err := d.db.QueryContext(ctx,
		`SELECT entity_norm, COUNT(DISTINCT content_id) AS c,
		        MAX(asserted_at) AS last_activity, MAX(entity_raw) AS raw
		 FROM entity_mentions
		 WHERE attribute IN ('ner_person', 'ner_org', 'ner_project')`+notIn+`
		 GROUP BY entity_norm ORDER BY c DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("top subjects: %w", err)
	}
	defer rows.Close()
	var out []SubjectStat
	var norms []string
	for rows.Next() {
		var s SubjectStat
		var last, raw sql.NullString
		if err := rows.Scan(&s.Norm, &s.Mentions, &last, &raw); err != nil {
			return nil, err
		}
		s.LastActivity = last.String
		s.Raw = raw.String
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
	// Keep entities that are (a) not generic junk and (b) whose DOMINANT type is a real
	// subject kind (a stray person/org tag on a mostly-topic entity is still a topic).
	filtered := out[:0]
	for i := range out {
		if IsJunkSubjectNorm(out[i].Norm) {
			continue
		}
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
		 WHERE entity_norm IN (`+strings.Join(ph, ",")+`) AND (attribute LIKE 'ner_%' OR attribute LIKE 'id_%')
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
