package ingest

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/store"
)

func mkNISS(base9 string) string { // fictional, pre-2000 checksum
	b, _ := strconv.ParseInt(base9, 10, 64)
	return base9 + fmt.Sprintf("%02d", 97-(b%97))
}
func fmtNN(n string) string {
	return n[0:2] + "." + n[2:4] + "." + n[4:6] + "-" + n[6:9] + "." + n[9:11]
}

func TestIdentifierProximityLinks(t *testing.T) {
	nnA, nnB := mkNISS("700101123"), mkNISS("850202234")

	// Each name is clearly closest to its OWN number (rows separated), so each links to its
	// own — the multi-person case NPMI alone can't resolve.
	tableItem := &store.KnowledgeItem{
		Title: "menage",
		NormalizedText: "Alice Bernard, numero national " + fmtNN(nnA) + ", enregistre. " +
			strings.Repeat("ligne de remplissage. ", 8) +
			"Bob Carter, numero national " + fmtNN(nnB) + ", enregistre.",
		Metadata: map[string]any{"extracted_persons": []string{"Alice Bernard", "Bob Carter"}},
	}
	got := map[string]string{} // person_norm -> id_norm
	for _, l := range identifierProximityLinks(tableItem) {
		got[l.PersonNorm] = l.IDNorm
	}
	if got[contradict.NormKey("Alice Bernard")] != nnA {
		t.Errorf("Alice should link to nnA; got %v", got)
	}
	if got[contradict.NormKey("Bob Carter")] != nnB {
		t.Errorf("Bob should link to nnB; got %v", got)
	}

	// Ambiguous: one name flanked by two same-type numbers → no confident link.
	amb := &store.KnowledgeItem{
		Title:          "x",
		NormalizedText: fmtNN(nnA) + " Bernard " + fmtNN(nnB),
		Metadata:       map[string]any{"extracted_persons": []string{"Bernard"}},
	}
	if l := identifierProximityLinks(amb); len(l) != 0 {
		t.Errorf("flanked (ambiguous) case should yield no link, got %v", l)
	}

	// Beyond the window: a number far from any person → no link.
	far := &store.KnowledgeItem{
		Title:          "x",
		NormalizedText: "Alice Bernard" + strings.Repeat(" ", 350) + fmtNN(nnA),
		Metadata:       map[string]any{"extracted_persons": []string{"Alice Bernard"}},
	}
	if l := identifierProximityLinks(far); len(l) != 0 {
		t.Errorf("number beyond the window should not link, got %v", l)
	}
}
