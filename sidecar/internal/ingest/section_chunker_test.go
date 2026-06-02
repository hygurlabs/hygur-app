package ingest

import (
	"strings"
	"testing"
)

func TestBuildSectionsStructural(t *testing.T) {
	doc := strings.Join([]string{
		"Intro avant tout titre.", // preamble
		"",
		"# Recharges véhicule",
		"Vue d'ensemble des recharges.",
		"",
		"## Avril 2026",
		"Recharge du 3 avril : 42 kWh.",
		"",
		"### Borne maison",
		"Détail borne maison.",
		"",
		"## Mai 2026",
		"Recharge du 5 mai : 30 kWh.",
	}, "\n")

	secs := BuildSections("ki-1", doc, 512)

	// preamble + H1 + 2×H2 + 1×H3 = 5 sections
	if len(secs) != 5 {
		t.Fatalf("expected 5 sections, got %d: %s", len(secs), dumpHeadings(secs))
	}

	// 0: preamble (level 0, no heading), 1: H1, 2: H2 Avril, 3: H3 Borne, 4: H2 Mai
	if secs[0].Section.Level != 0 || secs[0].Section.Heading != "" {
		t.Errorf("section0 should be preamble: %+v", secs[0].Section)
	}
	if secs[1].Section.Level != 1 || secs[1].Section.Heading != "Recharges véhicule" {
		t.Errorf("section1 should be H1: %+v", secs[1].Section)
	}
	avril := secs[2].Section
	if avril.Level != 2 || avril.Heading != "Avril 2026" {
		t.Fatalf("section2 should be H2 Avril: %+v", avril)
	}
	// H2 parent is H1
	if avril.ParentSectionID == nil || *avril.ParentSectionID != secs[1].Section.SectionID {
		t.Errorf("Avril parent should be H1, got %v", avril.ParentSectionID)
	}
	wantPath := []string{"Recharges véhicule", "Avril 2026"}
	if strings.Join(avril.HeadingPath, " > ") != strings.Join(wantPath, " > ") {
		t.Errorf("Avril heading_path = %v, want %v", avril.HeadingPath, wantPath)
	}
	// H3 "Borne maison" parent is H2 Avril
	borne := secs[3].Section
	if borne.Level != 3 || borne.ParentSectionID == nil || *borne.ParentSectionID != avril.SectionID {
		t.Errorf("Borne parent should be Avril H2: %+v", borne)
	}
	// H2 "Mai 2026" must pop back to H1 as parent (not stay under Avril/Borne)
	mai := secs[4].Section
	if mai.Heading != "Mai 2026" || mai.ParentSectionID == nil || *mai.ParentSectionID != secs[1].Section.SectionID {
		t.Errorf("Mai parent should be H1, got %v", mai.ParentSectionID)
	}

	// Ordinals are document order 0..4.
	for i, sc := range secs {
		if sc.Section.Ordinal != i {
			t.Errorf("section %d ordinal = %d", i, sc.Section.Ordinal)
		}
		if len(sc.Chunks) == 0 {
			t.Errorf("section %d (%q) has no chunks", i, sc.Section.Heading)
		}
		for _, ch := range sc.Chunks {
			if ch.SectionID == nil || *ch.SectionID != sc.Section.SectionID {
				t.Errorf("chunk not linked to its section in %q", sc.Section.Heading)
			}
			if ch.ContentID != "ki-1" {
				t.Errorf("chunk content_id = %q", ch.ContentID)
			}
		}
	}

	// FullText of the Avril H2 section includes its heading line + body.
	if !strings.Contains(avril.FullText, "## Avril 2026") || !strings.Contains(avril.FullText, "42 kWh") {
		t.Errorf("Avril full_text missing heading or body: %q", avril.FullText)
	}
	// Model (a): the H2 block stops at the next heading — it must NOT swallow
	// its H3 subsection's body.
	if strings.Contains(avril.FullText, "Détail borne maison") {
		t.Errorf("Avril full_text should not include H3 subsection body: %q", avril.FullText)
	}
}

func TestBuildSectionsFlatFallback(t *testing.T) {
	// No headings → flat fallback. Multiple short paragraphs fit one block.
	doc := "Première facture EDF.\n\nSeconde ligne TVA 21%.\n\nTroisième paragraphe."
	secs := BuildSections("ki-flat", doc, 512)
	if len(secs) == 0 {
		t.Fatal("expected at least one flat section")
	}
	for _, sc := range secs {
		if sc.Section.Level != 0 {
			t.Errorf("flat section should be level 0, got %d", sc.Section.Level)
		}
		if len(sc.Section.HeadingPath) != 0 {
			t.Errorf("flat section should have empty heading_path, got %v", sc.Section.HeadingPath)
		}
	}
}

func TestBuildSectionsLargeSectionMultiChunk(t *testing.T) {
	// A single section whose body far exceeds the chunk budget must yield
	// several chunks, all sharing the section id.
	body := strings.Repeat("Ligne de détail facturation. ", 400) // well over 512 tokens
	doc := "# Gros chapitre\n" + body
	secs := BuildSections("ki-big", doc, 128)
	if len(secs) != 1 {
		t.Fatalf("expected 1 section, got %d", len(secs))
	}
	if len(secs[0].Chunks) < 2 {
		t.Fatalf("expected the oversized section to split into >1 chunk, got %d", len(secs[0].Chunks))
	}
	id := secs[0].Section.SectionID
	for _, ch := range secs[0].Chunks {
		if ch.SectionID == nil || *ch.SectionID != id {
			t.Errorf("all chunks must share the section id")
		}
	}
}

func TestBuildSectionsEmpty(t *testing.T) {
	if secs := BuildSections("ki", "   \n\n  ", 512); secs != nil {
		t.Errorf("blank input should yield nil, got %d sections", len(secs))
	}
}

func dumpHeadings(secs []SectionChunk) string {
	var b strings.Builder
	for _, s := range secs {
		b.WriteString("[L")
		b.WriteByte(byte('0' + s.Section.Level))
		b.WriteString(" ")
		b.WriteString(s.Section.Heading)
		b.WriteString("] ")
	}
	return b.String()
}
