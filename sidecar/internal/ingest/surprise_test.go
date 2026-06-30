package ingest

import (
	"math"
	"testing"
)

func set(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
}

func pair(norm, attr string) string { return norm + "\x00" + attr }

func TestNoveltyDrift(t *testing.T) {
	cases := []struct {
		name       string
		newNorms   []string
		newPairs   []string
		knownNorms map[string]struct{}
		knownPairs map[string]struct{}
		wantNovel  float64
		wantDrift  bool
	}{
		{
			name: "all entities brand new", newNorms: []string{"a", "b"},
			newPairs:  []string{pair("a", "x"), pair("b", "y")},
			wantNovel: 1.0, wantDrift: false, // new entities are novelty, not drift
		},
		{
			name: "all known, nothing new", newNorms: []string{"a"},
			newPairs: []string{pair("a", "x")}, knownNorms: set("a"), knownPairs: set(pair("a", "x")),
			wantNovel: 0.0, wantDrift: false,
		},
		{
			name: "known entity gains a new attribute (drift)", newNorms: []string{"a"},
			newPairs: []string{pair("a", "y")}, knownNorms: set("a"), knownPairs: set(pair("a", "x")),
			wantNovel: 0.0, wantDrift: true,
		},
		{
			name: "half novel + drift", newNorms: []string{"a", "b"},
			newPairs: []string{pair("a", "y"), pair("b", "z")}, knownNorms: set("a"), knownPairs: set(pair("a", "x")),
			wantNovel: 0.5, wantDrift: true,
		},
		{
			name: "empty", newNorms: nil, newPairs: nil,
			wantNovel: 0.0, wantDrift: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nr, dr := NoveltyDrift(c.newNorms, c.newPairs, c.knownNorms, c.knownPairs)
			if math.Abs(nr-c.wantNovel) > 1e-9 || dr != c.wantDrift {
				t.Errorf("got (novel=%.3f drift=%v), want (novel=%.3f drift=%v)", nr, dr, c.wantNovel, c.wantDrift)
			}
		})
	}
}

func TestComputeSurprise(t *testing.T) {
	cases := []struct {
		novel float64
		drift bool
		want  float64
	}{
		{0.0, false, 0.0},
		{1.0, false, 0.60}, // novelty only
		{0.0, true, 0.40},  // drift only
		{0.5, true, 0.70},  // 0.6*0.5 + 0.4
		{1.0, true, 1.00},  // 0.6 + 0.4, clamped
	}
	for _, c := range cases {
		if got := ComputeSurprise(c.novel, c.drift); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("ComputeSurprise(%.2f,%v) = %.3f, want %.3f", c.novel, c.drift, got, c.want)
		}
	}
}
