// Package contradict detects deterministic contradictions across knowledge
// items: the same structured attribute (monetary amount, due date, IBAN, VAT
// number, Belgian structured communication) carrying divergent values within a
// single thread cluster (normalized mail subject).
//
// It runs entirely on the Tier-1 fields already extracted into item metadata
// (the extracted_* keys written by internal/extract), so it costs no LLM call
// and every reported value is a real, cited substring of a source item. This is
// the "facts before reply" foundation the LLM reconciliation layer (W6) builds
// on: Hygur signals a divergence and cites both sides — it never asserts.
//
// Precision over recall: a conflict is reported only when ≥2 *distinct* values
// are carried by ≥2 *distinct* source items in the same thread. Values that
// cannot be parsed deterministically (e.g. an ambiguous date) are skipped
// rather than guessed.
package contradict

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// Conflict is a deterministic contradiction within one thread cluster: the same
// attribute carries divergent values across distinct source items.
type Conflict struct {
	Type     string           `json:"type"`     // amount | due_date | iban | vat | structured_comm
	Cluster  string           `json:"cluster"`  // normalized subject (the thread key)
	Severity string           `json:"severity"` // high | medium
	Members  []ConflictMember `json:"members"`  // ≥2, one representative per distinct value, cited
}

// ConflictMember cites one side of a conflict: the divergent value and the item
// it came from (content_id + subject + interlocutor + date), so the user can
// open the source and decide.
type ConflictMember struct {
	ContentID string    `json:"content_id"`
	Title     string    `json:"title"`
	From      string    `json:"from"`
	Date      time.Time `json:"date"`
	Value     string    `json:"value"` // verbatim value as extracted (cited)
}

// occ is one occurrence of a value inside a cluster.
type occ struct {
	item  *store.KnowledgeItem
	value string // raw value (cited)
}

// Detect clusters items by normalized subject and reports divergent structured
// values within each cluster. Order is deterministic (by cluster, then type).
func Detect(items []*store.KnowledgeItem) []Conflict {
	clusters := map[string][]*store.KnowledgeItem{}
	for _, it := range items {
		if it == nil {
			continue
		}
		key := normalizeSubject(it.Title)
		if key == "" {
			continue // never cluster blank subjects together
		}
		clusters[key] = append(clusters[key], it)
	}

	keys := make([]string, 0, len(clusters))
	for k := range clusters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Conflict
	for _, k := range keys {
		group := clusters[k]
		if len(group) < 2 {
			continue
		}
		out = append(out, detectInCluster(k, group)...)
	}
	return out
}

// detectInCluster runs each attribute check over one thread cluster.
func detectInCluster(cluster string, group []*store.KnowledgeItem) []Conflict {
	var out []Conflict

	// Exact-match attributes: divergence = distinct normalized strings.
	for _, a := range []struct {
		typ, key, sev string
		norm          func(string) (string, bool)
	}{
		{"iban", "extracted_iban", "high", normExact},
		{"vat", "extracted_vat_numbers", "high", normExact},
		{"structured_comm", "extracted_structured_comm", "medium", normExact},
	} {
		byKey := map[string][]occ{}
		for _, it := range group {
			for _, v := range metaStrings(it.Metadata, a.key) {
				if ck, ok := a.norm(v); ok {
					byKey[ck] = append(byKey[ck], occ{it, v})
				}
			}
		}
		if c, ok := buildConflict(a.typ, a.sev, cluster, byKey); ok {
			out = append(out, c)
		}
	}

	// Amounts: divergence within a currency (cents differ).
	amountKeys := map[string][]occ{}
	for _, it := range group {
		for _, v := range metaStrings(it.Metadata, "extracted_amounts") {
			if ck, ok := amountKey(v); ok {
				amountKeys[ck] = append(amountKeys[ck], occ{it, v})
			}
		}
	}
	if c, ok := buildConflict("amount", "high", cluster, amountKeys); ok {
		out = append(out, c)
	}

	// Due dates: compare only within the same shape (with-year vs day/month
	// only) so "4 mai" never falsely conflicts with "2026-05-04".
	withYear := map[string][]occ{}
	dayMonth := map[string][]occ{}
	for _, it := range group {
		for _, v := range metaStrings(it.Metadata, "extracted_due_dates") {
			if ck, hasYear, ok := dateKey(v); ok {
				if hasYear {
					withYear[ck] = append(withYear[ck], occ{it, v})
				} else {
					dayMonth[ck] = append(dayMonth[ck], occ{it, v})
				}
			}
		}
	}
	if c, ok := buildConflict("due_date", "medium", cluster, withYear); ok {
		out = append(out, c)
	}
	if c, ok := buildConflict("due_date", "medium", cluster, dayMonth); ok {
		out = append(out, c)
	}

	return out
}

// buildConflict turns occurrences grouped by canonical value into a conflict,
// iff there are ≥2 distinct values AND ≥2 distinct source items. It picks one
// representative per value, greedily preferring sources not yet used so the
// citation set spans real distinct senders/items (a single item that mentions
// two values is an explanation, not a cross-source contradiction).
func buildConflict(typ, severity, cluster string, byKey map[string][]occ) (Conflict, bool) {
	if len(byKey) < 2 {
		return Conflict{}, false
	}
	if distinctSources(byKey) < 2 {
		return Conflict{}, false
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	chosen := map[string]bool{}
	members := make([]ConflictMember, 0, len(keys))
	for _, k := range keys {
		best := pickRepresentative(byKey[k], chosen)
		chosen[best.item.ContentID] = true
		members = append(members, memberFrom(best))
	}
	if len(chosen) < 2 {
		return Conflict{}, false
	}

	sort.Slice(members, func(i, j int) bool {
		if members[i].Date.Equal(members[j].Date) {
			return members[i].ContentID < members[j].ContentID
		}
		return members[i].Date.Before(members[j].Date)
	})
	return Conflict{Type: typ, Severity: severity, Cluster: cluster, Members: members}, true
}

// pickRepresentative returns the earliest occurrence whose source is not yet in
// `used`; falling back to the earliest occurrence overall. Ties broken by
// content_id for determinism.
func pickRepresentative(occs []occ, used map[string]bool) occ {
	earlier := func(a, b occ) bool {
		ta, tb := a.item.CreatedAt, b.item.CreatedAt
		if ta.Equal(tb) {
			return a.item.ContentID < b.item.ContentID
		}
		return ta.Before(tb)
	}
	var best, bestFresh *occ
	for i := range occs {
		o := occs[i]
		if best == nil || earlier(o, *best) {
			best = &occs[i]
		}
		if !used[o.item.ContentID] {
			if bestFresh == nil || earlier(o, *bestFresh) {
				bestFresh = &occs[i]
			}
		}
	}
	if bestFresh != nil {
		return *bestFresh
	}
	return *best
}

func memberFrom(o occ) ConflictMember {
	return ConflictMember{
		ContentID: o.item.ContentID,
		Title:     o.item.Title,
		From:      sender(o.item.Metadata),
		Date:      o.item.CreatedAt,
		Value:     o.value,
	}
}

// distinctSources counts the distinct content_ids across all occurrences.
func distinctSources(byKey map[string][]occ) int {
	set := map[string]bool{}
	for _, occs := range byKey {
		for _, o := range occs {
			set[o.item.ContentID] = true
		}
	}
	return len(set)
}

// --- value canonicalizers ---------------------------------------------------

var subjectPrefixRe = regexp.MustCompile(`(?i)^\s*(re|fwd|fw|tr|aw|rv)\s*(\[\d+\])?\s*:\s*`)

// normalizeSubject strips reply/forward prefixes, collapses whitespace and
// lowercases — the thread key. Returns "" for an empty subject.
func normalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		n := subjectPrefixRe.ReplaceAllString(s, "")
		n = strings.TrimSpace(n)
		if n == s {
			break
		}
		s = n
	}
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// normExact uppercases and removes spaces — for IBAN / VAT / structured comm.
func normExact(s string) (string, bool) {
	k := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if k == "" {
		return "", false
	}
	return k, true
}

// amountKey returns "<CURRENCY>:<cents>" for a value formatted "<value> <cur>"
// (the form written by extract.MergeTier1IntoMetadata, e.g. "1200.00 EUR").
func amountKey(s string) (string, bool) {
	f := strings.Fields(s)
	if len(f) < 2 {
		return "", false
	}
	num := strings.ReplaceAll(f[0], ",", ".")
	val, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return "", false
	}
	cur := strings.ToUpper(f[len(f)-1])
	cents := int64(math.Round(val * 100))
	return cur + ":" + strconv.FormatInt(cents, 10), true
}

var (
	reNumDate   = regexp.MustCompile(`^(\d{1,2})[\s./-](\d{1,2})(?:[\s./-](\d{2,4}))?$`)
	reNamedDMY  = regexp.MustCompile(`(?i)^(\d{1,2})\s+([\p{L}]{3,12})(?:\s+(\d{4}))?$`)
	reNamedMDY  = regexp.MustCompile(`(?i)^([\p{L}]{3,12})\s+(\d{1,2})(?:,?\s+(\d{4}))?$`)
	monthByName = map[string]int{
		"janvier": 1, "january": 1, "jan": 1,
		"février": 2, "fevrier": 2, "february": 2, "feb": 2, "fév": 2, "fev": 2,
		"mars": 3, "march": 3, "mar": 3,
		"avril": 4, "april": 4, "apr": 4, "avr": 4,
		"mai": 5, "may": 5,
		"juin": 6, "june": 6, "jun": 6,
		"juillet": 7, "july": 7, "jul": 7, "juil": 7,
		"août": 8, "aout": 8, "august": 8, "aug": 8,
		"septembre": 9, "september": 9, "sep": 9, "sept": 9,
		"octobre": 10, "october": 10, "oct": 10,
		"novembre": 11, "november": 11, "nov": 11,
		"décembre": 12, "decembre": 12, "december": 12, "dec": 12, "déc": 12,
	}
)

// dateKey canonicalizes a raw due-date string. Returns the comparison key, a
// flag indicating whether a year was present (keys are compared only within the
// same shape), and ok=false when the string cannot be parsed unambiguously.
func dateKey(s string) (key string, hasYear bool, ok bool) {
	s = strings.TrimSpace(s)

	if m := reNumDate.FindStringSubmatch(s); m != nil {
		day, _ := strconv.Atoi(m[1]) // day-first (FR/BE accounting convention)
		mon, _ := strconv.Atoi(m[2])
		return finishDate(day, mon, m[3])
	}
	if m := reNamedDMY.FindStringSubmatch(s); m != nil {
		mon, found := monthByName[strings.ToLower(m[2])]
		if !found {
			return "", false, false
		}
		day, _ := strconv.Atoi(m[1])
		return finishDate(day, mon, m[3])
	}
	if m := reNamedMDY.FindStringSubmatch(s); m != nil {
		mon, found := monthByName[strings.ToLower(m[1])]
		if !found {
			return "", false, false
		}
		day, _ := strconv.Atoi(m[2])
		return finishDate(day, mon, m[3])
	}
	return "", false, false
}

func finishDate(day, mon int, yearStr string) (string, bool, bool) {
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return "", false, false
	}
	if yearStr == "" {
		return pad2(mon) + "-" + pad2(day), false, true
	}
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		return "", false, false
	}
	if year < 100 {
		year += 2000
	}
	return strconv.Itoa(year) + "-" + pad2(mon) + "-" + pad2(day), true, true
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// sender returns the interlocutor for citation (mail_from, falling back to from).
func sender(m map[string]any) string {
	for _, k := range []string{"mail_from", "from"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// metaStrings reads a metadata value that may be []string or []any (the form it
// takes after a JSON round-trip through the store) into []string.
func metaStrings(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	switch arr := m[key].(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
