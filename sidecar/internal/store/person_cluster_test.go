package store

import "testing"

// TestDistinctPeople covers the four cases the identifier lookup depends on: a person's own
// name-variant norms collapse to one; genuinely distinct people (bare-name overlap or a shared
// surname) stay separate; an OCR accent split is one person; and a shared short norm never
// transitively merges two unrelated full names. Fictional names only.
func TestDistinctPeople(t *testing.T) {
	cases := []struct {
		name  string
		norms []string
		want  int
	}{
		{"empty", nil, 0},
		{"single", []string{"alice bernard"}, 1},
		// One person, two variants: reversed order + a middle name. {bernard,alice} ⊆
		// {alice,marie,bernard} → 1 maximal → one person → resolve.
		{"reversed+middle variant", []string{"bernard alice", "alice marie bernard"}, 1},
		// A chain of variants (bare → surname+first → +middle) is still one person.
		{"variant chain", []string{"alice", "alice bernard", "alice marie bernard"}, 1},
		// OCR accent split: "gérard" vs "gerard" fold equal → same token set → one person.
		{"accent OCR split", []string{"alice gérard bernard", "bernard alice gerard"}, 1},
		// Two DISTINCT people sharing a surname (different first names) → 2 non-subset maximals.
		{"siblings share surname", []string{"alice bernard", "bob bernard"}, 2},
		// A bare first name shared by two DISTINCT full names → 2 maximals (the short norm is a
		// subset of BOTH but must NOT transitively merge them).
		{"shared first name, distinct", []string{"alice", "alice bernard", "alice carter"}, 2},
		// The critical non-merge: a bare first name that is a subset of two unrelated full names
		// still leaves the two full names as distinct maximals.
		{"no transitive merge", []string{"denis", "denis gerard petit", "denis martin"}, 2},
		// Five genuinely distinct people (no shared tokens) — the KG-1 shape — stay five.
		{"five distinct", []string{"al ka", "bo qux", "cy zog", "di wun", "ef pol"}, 5},
	}
	for _, tc := range cases {
		if got := DistinctPeople(tc.norms); got != tc.want {
			t.Errorf("%s: DistinctPeople(%v) = %d, want %d", tc.name, tc.norms, got, tc.want)
		}
	}
}
