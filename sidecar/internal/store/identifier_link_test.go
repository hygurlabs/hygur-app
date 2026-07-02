package store

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
)

// TestResolvePersonNorms_WordBoundary proves the resolution matches whole tokens / full-name
// phrases, not arbitrary substrings — the O1 fix. Fictional household only (Alice/Bob Bernard).
func TestResolvePersonNorms_WordBoundary(t *testing.T) {
	db, err := NewDB(filepath.Join(t.TempDir(), "eil.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	mkItem(t, db, "doc-1")
	// Two distinct people sharing the surname "bernard", plus a bare-surname mention.
	if err := db.ReplaceEntityMentions(ctx, "doc-1", []EntityMention{
		{EntityNorm: "alice bernard", EntityRaw: "Alice Bernard", Attribute: "ner_person"},
		{EntityNorm: "bob bernard", EntityRaw: "Bob Bernard", Attribute: "ner_person"},
		{EntityNorm: "bernard", EntityRaw: "Bernard", Attribute: "ner_person"},
		{EntityNorm: "carine bernardino", EntityRaw: "Carine Bernardino", Attribute: "ner_person"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	get := func(q string) []string {
		out, err := db.ResolvePersonNorms(ctx, q, 20)
		if err != nil {
			t.Fatalf("resolve %q: %v", q, err)
		}
		sort.Strings(out)
		return out
	}

	// Whole-token surname pools exactly the people whose name CONTAINS the token "bernard"
	// as a word — Alice, Bob, and the bare "bernard" — but NOT "bernardino" (substring only).
	if got := get("bernard"); !equalStrs(got, []string{"alice bernard", "bernard", "bob bernard"}) {
		t.Errorf(`resolve("bernard") = %v, want [alice bernard, bernard, bob bernard] (no bernardino)`, got)
	}

	// A partial token that used to over-match ("bern" ⊂ everything) now matches nothing.
	if got := get("bern"); len(got) != 0 {
		t.Errorf(`resolve("bern") = %v, want [] (partial token must not match)`, got)
	}

	// A legitimate full name still resolves to exactly that one person.
	if got := get("alice bernard"); !equalStrs(got, []string{"alice bernard"}) {
		t.Errorf(`resolve("alice bernard") = %v, want [alice bernard]`, got)
	}

	// A distinctive first name resolves to just its owner (whole-token match).
	if got := get("alice"); !equalStrs(got, []string{"alice bernard"}) {
		t.Errorf(`resolve("alice") = %v, want [alice bernard]`, got)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
