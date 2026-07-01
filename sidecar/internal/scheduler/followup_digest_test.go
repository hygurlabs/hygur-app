package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// The follow-up read must never surface a matter the deterministic layer marks closed
// or in conflict — a superseded decision, or an item in an open contradiction — so it
// can't recommend chasing a thread that already ended (e.g. in a refusal).
func TestDropClosedItemsGate(t *testing.T) {
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	for _, id := range []string{"i1", "i2", "i3"} {
		if err := db.InsertKnowledgeItem(ctx, &store.KnowledgeItem{
			ContentID: id, SourceType: store.SourceTypeNote, Title: id,
			NormalizedText: "x", VersionID: "v1", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	if err := db.UpsertDecisionAttrs(ctx, "i1", store.DecisionSuperseded, "2026-01-01", nil, ""); err != nil {
		t.Fatalf("decision: %v", err)
	}
	conflicts := `[{"members":[{"source_id":"i2"},{"source_id":"other"}],"verdict":{"kind":"conflict"}}]`
	if err := db.PutContradictionCache(ctx, "", conflicts, 1); err != nil {
		t.Fatalf("contradiction cache: %v", err)
	}

	d := &DailyBrief{store: db}
	items := []*store.KnowledgeItem{{ContentID: "i1"}, {ContentID: "i2"}, {ContentID: "i3"}}
	got := d.dropClosedItems(ctx, items)
	if len(got) != 1 || got[0].ContentID != "i3" {
		var ids []string
		for _, it := range got {
			ids = append(ids, it.ContentID)
		}
		t.Fatalf("want [i3] (superseded i1 + contradicted i2 dropped), got %v", ids)
	}
}

func digestItems() []*store.KnowledgeItem {
	mk := func(id, title, from string, day int) *store.KnowledgeItem {
		return &store.KnowledgeItem{
			ContentID: id,
			Title:     title,
			Metadata:  map[string]any{"mail_from": from},
			CreatedAt: time.Date(2026, 6, day, 9, 0, 0, 0, time.UTC),
		}
	}
	return []*store.KnowledgeItem{
		mk("a", "Devis Projet X", "alice@acme.com", 1),
		mk("b", "Facture Projet X", "bob@acme.com", 2),
		mk("c", "Newsletter", "news@x.com", 3),
	}
}

func TestParseFollowupJSON(t *testing.T) {
	ok := []string{
		`{"topics":[{"title":"T","note":"n","refs":[1]}],"contradictions":[]}`,
		"```json\n{\"topics\":[],\"contradictions\":[]}\n```",
		"Voici la synthèse :\n{\"topics\":[],\"contradictions\":[]}\nFin.",
	}
	for _, s := range ok {
		if _, good := parseFollowupJSON(s); !good {
			t.Errorf("parseFollowupJSON should accept: %q", s)
		}
	}
	if _, good := parseFollowupJSON("pas de json ici"); good {
		t.Error("parseFollowupJSON should reject prose with no object")
	}
}

func TestGateDropsHallucinatedAndUngrounded(t *testing.T) {
	items := digestItems()
	rd := rawDigest{}
	// Topic with a valid ref → kept.
	rd.Topics = append(rd.Topics, struct {
		Title string `json:"title"`
		Note  string `json:"note"`
		Refs  []int  `json:"refs"`
	}{"Projet X", "Devis puis facture.", []int{1, 2}})
	// Topic with no valid ref → dropped.
	rd.Topics = append(rd.Topics, struct {
		Title string `json:"title"`
		Note  string `json:"note"`
		Refs  []int  `json:"refs"`
	}{"Fantôme", "Inventé.", []int{99}})
	// Topic with empty note → dropped.
	rd.Topics = append(rd.Topics, struct {
		Title string `json:"title"`
		Note  string `json:"note"`
		Refs  []int  `json:"refs"`
	}{"Vide", "", []int{3}})

	out := gateDigest(rd, items)
	if len(out.Topics) != 1 {
		t.Fatalf("want 1 grounded topic, got %d: %+v", len(out.Topics), out.Topics)
	}
	if out.Topics[0].Title != "Projet X" || len(out.Topics[0].Sources) != 2 {
		t.Errorf("unexpected kept topic: %+v", out.Topics[0])
	}
	if out.Topics[0].Sources[0].From != "alice@acme.com" {
		t.Errorf("citation interlocutor wrong: %+v", out.Topics[0].Sources[0])
	}
}

func TestGateContradictionRequiresTwoDistinctSources(t *testing.T) {
	items := digestItems()
	cases := []struct {
		name string
		refs []int
		keep bool
	}{
		{"two distinct", []int{1, 2}, true},
		{"single ref", []int{1}, false},
		{"same item twice", []int{1, 1}, false},
		{"one valid one out-of-range", []int{1, 99}, false},
		{"both out-of-range", []int{50, 99}, false},
	}
	for _, c := range cases {
		rd := rawDigest{}
		rd.Contradictions = append(rd.Contradictions, struct {
			Note string `json:"note"`
			Refs []int  `json:"refs"`
		}{"montant 1000 vs 1200", c.refs})
		out := gateDigest(rd, items)
		got := len(out.Contradictions) == 1
		if got != c.keep {
			t.Errorf("%s: refs=%v keep=%v, got kept=%v", c.name, c.refs, c.keep, got)
		}
	}
}

func TestSnippetTruncates(t *testing.T) {
	if s := snippet("  hello   world  ", 100); s != "hello world" {
		t.Errorf("snippet collapse failed: %q", s)
	}
	if s := snippet("abcdefghij", 4); s != "abcd…" {
		t.Errorf("snippet truncate failed: %q", s)
	}
}

func TestRecencyDate(t *testing.T) {
	const sent = "2026-05-01T09:00:00Z"
	ingest := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)
	mailDated := &store.KnowledgeItem{SourceType: store.SourceTypeMail, Metadata: map[string]any{"canonical_date": sent}, CreatedAt: ingest}
	mailUndated := &store.KnowledgeItem{SourceType: store.SourceTypeMail, Metadata: map[string]any{}, CreatedAt: ingest}
	noteUndated := &store.KnowledgeItem{SourceType: store.SourceTypeNote, Metadata: map[string]any{}, CreatedAt: ingest}

	if got := recencyDate(mailDated); got.UTC().Format(time.RFC3339) != sent {
		t.Errorf("dated mail: got %v, want %s", got, sent)
	}
	if got := recencyDate(mailUndated); !got.IsZero() {
		t.Errorf("undated mail must be zero (excluded), got %v", got)
	}
	if got := recencyDate(noteUndated); !got.Equal(ingest) {
		t.Errorf("note falls back to created_at: got %v, want %v", got, ingest)
	}
}
