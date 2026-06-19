package retrieval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

// EntitySearchOptions tunes the structured lookup performed for factual_entity
// queries. Defaults are sensible for the probe; override TopK/MaxScan to widen
// the candidate pool when needed.
type EntitySearchOptions struct {
	// TopK caps the number of results returned (default 10).
	TopK int
	// MaxScan caps how many candidate documents are scanned at the SQL layer
	// before scoring (default 200). Higher values are safer but slower.
	MaxScan int
	// AllowedContentIDs, when non-nil, restricts the SQL search to documents
	// whose content_id is in the list. An empty slice (non-nil) yields no
	// results. Nil disables the filter.
	AllowedContentIDs []string
	// UseEntityIndex turns on the brick-1 associative entity lens: items whose
	// cached claims mention the queried entity are folded in (claim-grounded
	// recall + precision) on top of the surface match. Off → legacy behavior.
	UseEntityIndex bool
	// EntityNorms are additional normalized entities (brick-2 synonyms of the
	// queried one) to fold into the index lookup, alongside the queried entity's
	// own norm. Empty → only the queried entity is looked up.
	EntityNorms []string
}

func (o *EntitySearchOptions) defaults() {
	if o.TopK <= 0 {
		o.TopK = 10
	}
	if o.MaxScan <= 0 {
		o.MaxScan = 200
	}
}

// attributeMetadataKeys maps an Attribute label produced by the classifier to
// the metadata.extracted_* keys that, when present and non-empty, indicate
// that the document carries the requested fact. Keys are tried in order; any
// hit counts as a positive signal.
//
// Attributes not in the map have no metadata signal — the entity-name match
// remains the only ranking dimension.
var attributeMetadataKeys = map[string][]string{
	"iban":            {"extracted_iban"},
	"amount":          {"extracted_amounts"},
	"invoice":         {"extracted_amounts", "extracted_structured_comm"},
	"communication":   {"extracted_structured_comm"},
	"phone":           {"extracted_phones"},
	"national_number": {"extracted_phones"},
	// Tier 2 attributes (populated by the LLM extractor at ingestion).
	"person":       {"extracted_persons"},
	"organization": {"extracted_orgs"},
	"project":      {"extracted_projects"},
	"topic":        {"extracted_topics"},
}

// entityNameMetadataKeys are the metadata.extracted_* lists in which the
// entity *name* itself can plausibly appear (independently of the requested
// attribute). Hitting one of these — e.g. "Jean" inside extracted_persons —
// is a stronger signal than a raw body LIKE match because Tier 2 NER will
// have already normalized casing and stripped surrounding noise.
var entityNameMetadataKeys = []string{
	"extracted_persons",
	"extracted_orgs",
	"extracted_projects",
	"extracted_topics",
}

// EntitySearch finds documents whose title, body, mail headers, or participants
// mention the entity name carried by the intent. Results are ranked by a small
// hand-crafted score that rewards title matches, recency, and presence of an
// extracted_* metadata key matching the requested attribute. Returns an empty
// slice (not an error) when nothing matches — the caller treats this as
// abstention.
func EntitySearch(ctx context.Context, db *store.DB, intent *QueryIntent, opts EntitySearchOptions) ([]UnifiedResult, error) {
	if db == nil {
		return nil, fmt.Errorf("entity search: nil store")
	}
	if intent == nil || strings.TrimSpace(intent.Entity) == "" {
		return nil, nil
	}
	opts.defaults()

	entity := strings.TrimSpace(intent.Entity)
	pattern := "%" + entity + "%"
	// Search in title, body, mail_subject, mail_from, participants, and the
	// Tier 2 NER lists (extracted_persons / extracted_orgs / extracted_projects
	// / extracted_topics) in one query. JSON arrays are lifted via json_each
	// for a string match.
	const baseSQL = `
		SELECT content_id, source_type, source_path, title, normalized_text, metadata,
		       version_id, created_at, updated_at
		FROM knowledge_items
		WHERE (title LIKE ?
		   OR normalized_text LIKE ?
		   OR (metadata IS NOT NULL AND (
		         json_extract(metadata, '$.mail_subject') LIKE ?
		      OR json_extract(metadata, '$.mail_from') LIKE ?
		      OR EXISTS (
		           SELECT 1 FROM json_each(json_extract(metadata, '$.participants'))
		           WHERE value LIKE ?
		         )
		      OR EXISTS (
		           SELECT 1 FROM json_each(json_extract(metadata, '$.extracted_persons'))
		           WHERE value LIKE ?
		         )
		      OR EXISTS (
		           SELECT 1 FROM json_each(json_extract(metadata, '$.extracted_orgs'))
		           WHERE value LIKE ?
		         )
		      OR EXISTS (
		           SELECT 1 FROM json_each(json_extract(metadata, '$.extracted_projects'))
		           WHERE value LIKE ?
		         )
		      OR EXISTS (
		           SELECT 1 FROM json_each(json_extract(metadata, '$.extracted_topics'))
		           WHERE value LIKE ?
		         )
		   )))
	`

	// Focus scope short-circuit: an explicit empty allow-list means the user
	// asked for a scope that resolved to zero documents — abstain.
	if opts.AllowedContentIDs != nil && len(opts.AllowedContentIDs) == 0 {
		return nil, nil
	}

	args := []any{pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern}
	searchSQL := baseSQL
	if len(opts.AllowedContentIDs) > 0 {
		placeholders := strings.Repeat("?,", len(opts.AllowedContentIDs))
		placeholders = placeholders[:len(placeholders)-1]
		searchSQL += " AND content_id IN (" + placeholders + ")"
		for _, cid := range opts.AllowedContentIDs {
			args = append(args, cid)
		}
	}
	searchSQL += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, opts.MaxScan)

	now := time.Now()
	wantedKeys := attributeMetadataKeys[intent.Attribute]

	// Brick 1 — associative entity index. Items whose cached claims mention the
	// queried entity are claim-grounded hits: a stronger, normalized signal than an
	// incidental body substring, and they recall items the surface LIKE may miss.
	// claimSet flags them for scoring; missing ones are fetched in a second pass.
	// No-op (byte-identical to the legacy path) when the flag is off or the index
	// is empty — so existing factual lookups can't regress.
	claimSet := map[string]bool{}
	if opts.UseEntityIndex {
		norms := append([]string{contradict.NormKey(entity)}, opts.EntityNorms...)
		if cids, cerr := db.EntityMentionContentIDs(ctx, norms, opts.MaxScan); cerr == nil {
			for _, cid := range cids {
				claimSet[cid] = true
			}
		}
	}

	rows, err := db.SQLDB().QueryContext(ctx, searchSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("entity search: query: %w", err)
	}
	pool, err := scanEntityCandidates(rows, entity, wantedKeys, claimSet, now)
	rows.Close()
	if err != nil {
		return nil, err
	}

	// Second pass: claim-grounded items the surface query didn't already return.
	if len(claimSet) > 0 {
		inPool := make(map[string]bool, len(pool))
		for _, c := range pool {
			inPool[c.item.ContentID] = true
		}
		missing := make([]string, 0, len(claimSet))
		for cid := range claimSet {
			if !inPool[cid] {
				missing = append(missing, cid)
			}
		}
		extra, eerr := entityIndexCandidates(ctx, db, missing, entity, wantedKeys, claimSet, now, opts.AllowedContentIDs)
		if eerr != nil {
			return nil, eerr
		}
		pool = append(pool, extra...)
	}

	// Sort by score descending, secondary by updated_at descending (already
	// implicit via SQL ORDER BY but stable across equal scores).
	sortStableByScore(pool)

	if len(pool) > opts.TopK {
		pool = pool[:opts.TopK]
	}

	results := make([]UnifiedResult, 0, len(pool))
	for _, s := range pool {
		r := UnifiedResult{
			ContentID:  s.item.ContentID,
			SourceType: s.item.SourceType,
			Title:      s.item.Title,
			Score:      s.score,
			Metadata:   s.item.Metadata,
		}
		const maxDocLength = 2000
		if len(s.item.NormalizedText) > maxDocLength {
			r.Excerpt = s.item.NormalizedText[:maxDocLength] + "..."
		} else {
			r.Excerpt = s.item.NormalizedText
		}
		if s.item.Metadata != nil {
			if v, ok := s.item.Metadata["mail_from"].(string); ok {
				r.MailFrom = v
			}
			if v, ok := s.item.Metadata["mail_subject"].(string); ok {
				r.MailSubject = v
			}
			if v, ok := s.item.Metadata["mail_date"].(string); ok {
				r.MailDate = v
			}
		}
		r.ParsedDate = store.GetCanonicalDate(s.item)
		if !r.ParsedDate.IsZero() {
			r.Date = r.ParsedDate.UTC().Format(time.RFC3339)
		}
		results = append(results, r)
	}
	return results, nil
}

type entityScore struct {
	score   float64
	matches []string
}

// scoreEntityMatch produces a small hand-crafted score combining (a) where in
// the document the entity name appears, (b) recency, and (c) whether the
// metadata carries the extracted_* key matching the requested attribute.
//
// The baseline weights:
//
//	title hit       → 1.0
//	NER list hit    → 0.9   (Tier 2 extracted_persons/orgs/projects/topics)
//	subject hit     → 0.8
//	participants    → 0.7
//	body hit        → 0.5
//
// Then ×1.3 if any wantedKeys is present and non-empty.
// Then linearly blended with recency score (Wr=0.3) so a fresh body match can
// outrank a stale title match.
func scoreEntityMatch(item *store.KnowledgeItem, entity string, wantedKeys []string, claimHit bool, now time.Time) entityScore {
	matches := []string{}
	base := 0.0

	lower := strings.ToLower(entity)
	if containsFold(item.Title, lower) {
		matches = append(matches, "title")
		if base < 1.0 {
			base = 1.0
		}
	}
	if item.Metadata != nil {
		// Tier 2 NER hit: name appears in an extracted_* list. Stronger than
		// a body match because Tier 2 normalizes spelling and casing — if the
		// LLM emitted "Jean" in extracted_persons, it's the canonical name,
		// not a coincidental substring.
		for _, key := range entityNameMetadataKeys {
			if listContainsFold(item.Metadata, key, lower) {
				matches = append(matches, "ner:"+key)
				if base < 0.9 {
					base = 0.9
				}
				break
			}
		}
		if v, ok := item.Metadata["mail_subject"].(string); ok && containsFold(v, lower) {
			matches = append(matches, "subject")
			if base < 0.8 {
				base = 0.8
			}
		}
		if v, ok := item.Metadata["mail_from"].(string); ok && containsFold(v, lower) {
			matches = append(matches, "from")
			if base < 0.7 {
				base = 0.7
			}
		}
		if parts, ok := item.Metadata["participants"].([]any); ok {
			for _, p := range parts {
				if ps, ok := p.(string); ok && containsFold(ps, lower) {
					matches = append(matches, "participants")
					if base < 0.7 {
						base = 0.7
					}
					break
				}
			}
		}
	}
	// Claim-grounded: the item's cached claims mention this entity (normalized,
	// not an incidental substring) — at least as strong as a Tier 2 NER hit.
	if claimHit {
		matches = append(matches, "claim")
		if base < 0.9 {
			base = 0.9
		}
	}
	if base == 0 && containsFold(item.NormalizedText, lower) {
		matches = append(matches, "body")
		base = 0.5
	}

	// Attribute boost: metadata extracted_* present and non-empty.
	if len(wantedKeys) > 0 && item.Metadata != nil {
		for _, k := range wantedKeys {
			if hasNonEmptyList(item.Metadata, k) {
				base *= 1.3
				matches = append(matches, "attr:"+k)
				break
			}
		}
	}

	// Recency blend (Wr=0.3): final = base*0.7 + recency*0.3.
	date := store.GetCanonicalDate(item)
	rec := 0.0
	if !date.IsZero() {
		days := now.Sub(date).Hours() / 24
		if days < 0 {
			days = 0
		}
		rec = 1.0 / (1.0 + days)
	}
	final := base*0.7 + rec*0.3
	return entityScore{score: final, matches: matches}
}

func containsFold(haystack, needleLower string) bool {
	return strings.Contains(strings.ToLower(haystack), needleLower)
}

// listContainsFold returns true if metadata[key] is a list whose elements
// contain needleLower (case-insensitive substring). Tolerates both []string
// and []any shapes since JSON unmarshal yields []any but in-memory writes
// (from Tier 2) yield []string.
func listContainsFold(meta map[string]any, key, needleLower string) bool {
	v, ok := meta[key]
	if !ok {
		return false
	}
	switch list := v.(type) {
	case []string:
		for _, s := range list {
			if containsFold(s, needleLower) {
				return true
			}
		}
	case []any:
		for _, e := range list {
			if s, ok := e.(string); ok && containsFold(s, needleLower) {
				return true
			}
		}
	}
	return false
}

func hasNonEmptyList(meta map[string]any, key string) bool {
	v, ok := meta[key]
	if !ok {
		return false
	}
	switch s := v.(type) {
	case []string:
		return len(s) > 0
	case []any:
		return len(s) > 0
	case string:
		return s != ""
	}
	return false
}

type entityCandidate struct {
	item    *store.KnowledgeItem
	score   float64
	matches []string
}

func sortStableByScore(pool []entityCandidate) {
	// Insertion sort: pool sizes here are bounded (≤ MaxScan, default 200) and
	// stability across equal scores preserves the SQL ORDER BY updated_at DESC.
	for i := 1; i < len(pool); i++ {
		for j := i; j > 0 && pool[j-1].score < pool[j].score; j-- {
			pool[j-1], pool[j] = pool[j], pool[j-1]
		}
	}
}

// scanEntityCandidates scores each scanned row via scoreEntityMatch, flagging
// claim-grounded items (those in claimSet). Shared by the surface query and the
// entity-index second pass so both score identically.
func scanEntityCandidates(rows *sql.Rows, entity string, wantedKeys []string, claimSet map[string]bool, now time.Time) ([]entityCandidate, error) {
	var pool []entityCandidate
	for rows.Next() {
		item := &store.KnowledgeItem{}
		var sourcePath, metadataStr sql.NullString
		if err := rows.Scan(&item.ContentID, &item.SourceType, &sourcePath, &item.Title,
			&item.NormalizedText, &metadataStr, &item.VersionID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("entity search: scan: %w", err)
		}
		if sourcePath.Valid {
			item.SourcePath = &sourcePath.String
		}
		if metadataStr.Valid && metadataStr.String != "" {
			_ = json.Unmarshal([]byte(metadataStr.String), &item.Metadata)
		}
		s := scoreEntityMatch(item, entity, wantedKeys, claimSet[item.ContentID], now)
		pool = append(pool, entityCandidate{item: item, score: s.score, matches: s.matches})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity search: iterate: %w", err)
	}
	return pool, nil
}

// entityIndexCandidates fetches and scores the claim-grounded items the surface
// query didn't already return (the recall half of the entity lens). Honors an
// active focus allow-list so the lens can't widen scope past it.
func entityIndexCandidates(ctx context.Context, db *store.DB, ids []string, entity string, wantedKeys []string, claimSet map[string]bool, now time.Time, allowed []string) ([]entityCandidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := `SELECT content_id, source_type, source_path, title, normalized_text, metadata,
	                 version_id, created_at, updated_at
	          FROM knowledge_items WHERE content_id IN (` + ph + `)`
	args := make([]any, 0, len(ids)+len(allowed))
	for _, id := range ids {
		args = append(args, id)
	}
	if len(allowed) > 0 {
		aph := strings.TrimSuffix(strings.Repeat("?,", len(allowed)), ",")
		query += " AND content_id IN (" + aph + ")"
		for _, a := range allowed {
			args = append(args, a)
		}
	}
	rows, err := db.SQLDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("entity search: index query: %w", err)
	}
	defer rows.Close()
	return scanEntityCandidates(rows, entity, wantedKeys, claimSet, now)
}
