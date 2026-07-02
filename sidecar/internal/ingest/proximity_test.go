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

// TestIdentifierProximityLinks_DoubleOwner — the value-aware guard (O2). The SAME number sits
// once next to Alice and once next to Bob; each occurrence is nearest a DIFFERENT person, so
// the value has contested ownership in this doc → it links to NEITHER (fixes the double-owner).
func TestIdentifierProximityLinks_DoubleOwner(t *testing.T) {
	nn := mkNISS("700101123")
	item := &store.KnowledgeItem{
		Title: "x",
		NormalizedText: "Alice Bernard " + fmtNN(nn) + strings.Repeat(" filler", 20) +
			" " + fmtNN(nn) + " Bob Carter",
		Metadata: map[string]any{"extracted_persons": []string{"Alice Bernard", "Bob Carter"}},
	}
	if l := identifierProximityLinks(item); len(l) != 0 {
		t.Errorf("value owned by two distinct persons should yield no link, got %v", l)
	}
}

// TestIdentifierProximityLinks_SamePersonVariants — the over-decline regression. The SAME number
// sits once next to a person's "Zephrine Bernard" mention and once next to their "Zephrine
// Josephine Bernard" variant, so it is nearest a DIFFERENT variant norm at each spot. These are
// name-variant norms of ONE person (token-subset), so the value has ONE distinct owner and MUST
// link to that person — it must NOT be dropped as a double-owner the way two genuinely distinct
// persons are (that is the KG-1 case, covered above). Both variant norms link. Fictional NISS.
func TestIdentifierProximityLinks_SamePersonVariants(t *testing.T) {
	nn := mkNISS("700101123")
	item := &store.KnowledgeItem{
		Title: "dossier",
		NormalizedText: "Zephrine Bernard, reference " + fmtNN(nn) + "." +
			strings.Repeat(" texte de remplissage.", 8) +
			" Concernant Zephrine Josephine " + fmtNN(nn) + " Bernard.",
		Metadata: map[string]any{"extracted_persons": []string{"Zephrine Bernard", "Zephrine Josephine Bernard"}},
	}
	links := identifierProximityLinks(item)
	owners := map[string]bool{}
	for _, l := range links {
		if l.IDNorm != nn {
			t.Errorf("unexpected id norm %q, want %q", l.IDNorm, nn)
		}
		owners[l.PersonNorm] = true
	}
	if len(owners) < 2 {
		t.Fatalf("expected the value near BOTH variant norms (exercises owner clustering), got owners=%v", owners)
	}
	// The two owner norms are name variants of ONE person, so the guard did not drop the link.
	if store.DistinctPeople(keysOf(owners)) != 1 {
		t.Errorf("linked owners should cluster to one person, got %v", owners)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestTypedIdentifierMentions_MailcatPrior — the document-trust prior (T1). A transactional
// mail (Invoicing) yields NO typed-identifier node even though its text carries a valid NISS;
// an ordinary administrative document still extracts it. Fictional NISS only.
func TestTypedIdentifierMentions_MailcatPrior(t *testing.T) {
	nn := mkNISS("700101123")
	body := "Numero national " + fmtNN(nn) + " enregistre."
	mk := func(cats []string) *store.KnowledgeItem {
		m := map[string]any{}
		if cats != nil {
			m["mail_categories"] = cats
		}
		return &store.KnowledgeItem{SourceType: store.SourceTypeMail, NormalizedText: body, Metadata: m}
	}
	if got := typedIdentifierMentions(mk([]string{"Invoicing", "Banking & Finance"})); len(got) != 0 {
		t.Errorf("transactional mail should yield no typed identifier, got %v", got)
	}
	found := false
	for _, m := range typedIdentifierMentions(mk([]string{"Legal & Contracts", "Administrative"})) {
		if m.EntityNorm == nn && m.Attribute == "id_national_number" {
			found = true
		}
	}
	if !found {
		t.Error("ordinary document should extract the NISS node")
	}
}
