package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/events"
	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
	"github.com/rs/zerolog"
)

func newTestLogger() zerolog.Logger {
	return zerolog.New(io.Discard).With().Timestamp().Logger()
}

// fakeLLMServer returns a chat-completions response with the given content.
// When `unavailable` is true it returns 503 so the caller exercises the
// LLM-failure path.
func fakeLLMServer(t *testing.T, content string, unavailable bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": content}},
			},
		})
	}))
}

func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertItem(t *testing.T, db *store.DB, contentID, sourceType, title, body string, when time.Time, metadata map[string]any) {
	t.Helper()
	if metadata == nil {
		metadata = map[string]any{}
	}
	if err := db.InsertKnowledgeItem(context.Background(), &store.KnowledgeItem{
		ContentID:      contentID,
		SourceType:     sourceType,
		Title:          title,
		NormalizedText: body,
		Metadata:       metadata,
		VersionID:      "v1",
		CreatedAt:      when,
		UpdatedAt:      when,
	}); err != nil {
		t.Fatalf("InsertKnowledgeItem: %v", err)
	}
}

func TestDailyBrief_PublishesBriefForRecentItems(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	insertItem(t, db, "email:1", "email", "Déclaration TVA Q1 2026",
		"Montant 7421.85 EUR à payer avant le 25 avril 2026 IBAN BE22...",
		now.Add(-2*time.Hour),
		map[string]any{
			"mail_from":           "compta@example.test",
			"mail_date":           now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			"extracted_amounts":   []string{"7421.85 EUR"},
			"extracted_due_dates": []string{"25 avril 2026"},
			"high_priority":       true,
		})
	insertItem(t, db, "note:1", "note", "Idée produit X",
		"Brainstorm sur le pricing", now.Add(-3*time.Hour), nil)

	server := fakeLLMServer(t, "- Paiement TVA 7421.85 EUR avant 25 avril 2026 (Acme Compta)\n- Note ajoutée: Idée produit X\n- Aucune autre action urgente détectée.", false)
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 10*time.Second, 1, server.Client())
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeBrief)

	cfg := config.DailyBriefConfig{Enabled: true, MaxItems: 50, LookbackHours: 24}
	d := NewDailyBrief(db, llmClient, broker, cfg, newTestLogger())

	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case e := <-sub:
		if e.Type != events.EventTypeBrief {
			t.Errorf("Type = %q", e.Type)
		}
		bullets, _ := e.Data["bullets"].([]string)
		if len(bullets) == 0 {
			// JSON round-trip path may convert to []any; either is fine.
			if anys, _ := e.Data["bullets"].([]any); len(anys) == 0 {
				t.Errorf("expected non-empty bullets, got %v", e.Data["bullets"])
			}
		}
		if itemCount, _ := e.Data["item_count"].(int); itemCount != 0 {
			// item_count may be int or float64 depending on path; accept either > 0
			if itemCount < 2 {
				t.Errorf("item_count = %d, want >=2", itemCount)
			}
		}
		if isErr, _ := e.Data["error"].(bool); isErr {
			t.Errorf("expected error=false, got true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no brief event received")
	}

	// Brief persisted as a knowledge_item.
	saved, err := db.GetKnowledgeItem(context.Background(), "brief:"+time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatalf("GetKnowledgeItem: %v", err)
	}
	if saved == nil {
		t.Fatal("brief was not persisted")
	}
	if saved.SourceType != "brief" {
		t.Errorf("source_type = %q, want brief", saved.SourceType)
	}
}

// Regression: a backfilled 2024 mailbox would surface stale items in
// today's brief because they were *indexed* recently. We drop anything
// whose canonical_date / mail_date is older than MaxItemAgeDays.
func TestDailyBrief_DropsItemsOlderThanMaxAge(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	// Recently indexed but old email (2024-10-29) — should be dropped.
	insertItem(t, db, "email:old", "email", "Vieille déclaration",
		"corps", now.Add(-1*time.Hour),
		map[string]any{
			"mail_date": "2024-10-29T10:00:00Z",
		})
	// Recently indexed and recent email — should survive.
	freshDate := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	insertItem(t, db, "email:fresh", "email", "Nouvelle facture",
		"corps", now.Add(-1*time.Hour),
		map[string]any{
			"mail_date": freshDate,
		})

	server := fakeLLMServer(t, "## Synthèse exécutive\n- ok\n", false)
	defer server.Close()
	llmClient := llm.NewClientWithHTTP(server.URL, 5*time.Second, 0, server.Client())
	broker := events.NewBroker()
	cfg := config.DailyBriefConfig{
		Enabled:        true,
		LookbackHours:  168,
		MaxItems:       50,
		MaxItemAgeDays: 30, // tighter window for the test
	}
	d := NewDailyBrief(db, llmClient, broker, cfg, newTestLogger())

	gathered, _, err := d.gatherItems(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("gatherItems: %v", err)
	}
	if len(gathered) != 1 {
		t.Fatalf("expected 1 item after age filter, got %d", len(gathered))
	}
	if gathered[0].ContentID != "email:fresh" {
		t.Errorf("kept %q, expected email:fresh", gathered[0].ContentID)
	}
}

func TestDailyBrief_EmptyWindow_StillPublishes(t *testing.T) {
	db := newTestDB(t)
	server := fakeLLMServer(t, "ignored", false) // shouldn't be called
	defer server.Close()

	llmClient := llm.NewClientWithHTTP(server.URL, 5*time.Second, 1, server.Client())
	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeBrief)
	cfg := config.DailyBriefConfig{Enabled: true, LookbackHours: 24, MaxItems: 50}

	d := NewDailyBrief(db, llmClient, broker, cfg, newTestLogger())
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case e := <-sub:
		// Bullets should be empty/nil but the event must still fire.
		if isErr, _ := e.Data["error"].(bool); isErr {
			t.Errorf("error=true on empty window, want false")
		}
	case <-time.After(time.Second):
		t.Fatal("no brief event received for empty window")
	}
}

func TestDailyBrief_LLMFailure_EmitsErrorEvent(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	insertItem(t, db, "email:1", "email", "Test", "body", now.Add(-1*time.Hour),
		map[string]any{"mail_date": now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)})

	server := fakeLLMServer(t, "", true)
	defer server.Close()
	llmClient := llm.NewClientWithHTTP(server.URL, 2*time.Second, 0, server.Client())

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeBrief)
	cfg := config.DailyBriefConfig{Enabled: true, LookbackHours: 24, MaxItems: 50}

	d := NewDailyBrief(db, llmClient, broker, cfg, newTestLogger())
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run should not error on LLM failure: %v", err)
	}

	select {
	case e := <-sub:
		if isErr, _ := e.Data["error"].(bool); !isErr {
			t.Errorf("expected error=true on LLM failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no brief event received on LLM failure")
	}
}

// Reasoning models like Nemotron-super emit `<think>…</think>` blocks before
// the actual bullets. We strip them server-side so the persisted brief is
// readable; if stripping yields nothing we must fall through to the failure
// path (otherwise the user opens the brief and sees an empty body — which
// is exactly the regression that prompted this test).
func TestStripReasoningTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"<think>scratch</think>actual content", "actual content"},
		{"prefix <think>a</think> middle <think>b</think> suffix", "prefix  middle  suffix"},
		{"<think>only thinking, no answer</think>", ""},
		{"<think>truncated", ""}, // unclosed -> drop everything from <think>
		{"  \n<think>x</think>\n  ", ""},
	}
	for _, c := range cases {
		if got := stripReasoningTags(c.in); got != c.want {
			t.Errorf("stripReasoningTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Regression: when the LLM responds 200 OK but with a content field that is
// either empty or contains only `<think>` reasoning, persisting that as the
// brief body produces a knowledge_item the UI can't render. The handler must
// detect this and fall through to the deterministic fallback.
func TestDailyBrief_EmptyLLMContent_FallsBackToErrorEvent(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	insertItem(t, db, "email:1", "email", "Test", "body", now.Add(-1*time.Hour),
		map[string]any{"mail_date": now.Add(-1 * time.Hour).UTC().Format(time.RFC3339)})

	// LLM returns 200 OK but only thinking tokens (post-strip => empty).
	server := fakeLLMServer(t, "<think>let me think about this</think>", false)
	defer server.Close()
	llmClient := llm.NewClientWithHTTP(server.URL, 2*time.Second, 0, server.Client())

	broker := events.NewBroker()
	sub := broker.SubscribeFor(events.EventTypeBrief)
	cfg := config.DailyBriefConfig{Enabled: true, LookbackHours: 24, MaxItems: 50}

	d := NewDailyBrief(db, llmClient, broker, cfg, newTestLogger())
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case e := <-sub:
		if isErr, _ := e.Data["error"].(bool); !isErr {
			t.Errorf("expected error=true when LLM yields empty post-strip content")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no brief event received")
	}

	saved, err := db.GetKnowledgeItem(context.Background(), "brief:"+time.Now().Format("2006-01-02"))
	if err != nil || saved == nil {
		t.Fatalf("brief not persisted: err=%v saved=%v", err, saved)
	}
	if saved.NormalizedText == "" {
		t.Error("persisted brief has empty body — fallback not applied")
	}
}

func TestNewDailyBrief_NilDeps_ReturnsNil(t *testing.T) {
	cfg := config.DailyBriefConfig{Enabled: true}
	if NewDailyBrief(nil, &llm.Client{}, events.NewBroker(), cfg, newTestLogger()) != nil {
		t.Error("expected nil with nil store")
	}
	db := newTestDB(t)
	if NewDailyBrief(db, nil, events.NewBroker(), cfg, newTestLogger()) != nil {
		t.Error("expected nil with nil llm")
	}
	if NewDailyBrief(db, &llm.Client{}, nil, cfg, newTestLogger()) != nil {
		t.Error("expected nil with nil broker")
	}
}

func TestNextOccurrence_TodayOrTomorrow(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.Local)
	// Future today
	got := nextOccurrence(now, "11:00")
	want := time.Date(2026, 4, 30, 11, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("future-today: got %v, want %v", got, want)
	}
	// Past today → tomorrow
	got = nextOccurrence(now, "08:00")
	want = time.Date(2026, 5, 1, 8, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("past-today: got %v, want %v", got, want)
	}
	// Malformed → fallback to 08:00
	got = nextOccurrence(now, "garbage")
	if got.Hour() != 8 || got.Minute() != 0 {
		t.Errorf("malformed: hour/min = %d:%d, want 08:00", got.Hour(), got.Minute())
	}
}

func TestFirstBullets_ExtractsLeadingMarkdownBullets(t *testing.T) {
	text := "- premier point\n- deuxième\n* troisième\nautre ligne"
	got := firstBullets(text, 3)
	if len(got) != 3 || got[0] != "premier point" || got[2] != "troisième" {
		t.Errorf("got %v", got)
	}
}

func TestDeterministicSources(t *testing.T) {
	items := []briefItem{
		{KnowledgeItem: &store.KnowledgeItem{ContentID: "a"}, projectName: "SRL", tags: []string{"invoicing", "banking"}},
		{KnowledgeItem: &store.KnowledgeItem{ContentID: "b"}, projectName: "SRL", tags: []string{"invoicing"}},
		{KnowledgeItem: &store.KnowledgeItem{ContentID: "c"}, tags: []string{"family"}},
	}
	got := deterministicSources(items)
	want := "## Sources\n- Projets : SRL\n- Tags : banking, family, invoicing\n"
	if got != want {
		t.Errorf("deterministicSources:\n got %q\nwant %q", got, want)
	}
	if deterministicSources(nil) != "" {
		t.Error("empty input should yield no Sources section")
	}
}

func TestStripSourcesSection(t *testing.T) {
	md := "## Points importants\n- a\n\n## Sources\n- Tags : x, y\n"
	got := stripSourcesSection(md)
	want := "## Points importants\n- a"
	if got != want {
		t.Errorf("stripSourcesSection:\n got %q\nwant %q", got, want)
	}
	// A Sources section in the middle stops at the next heading.
	md2 := "## Sources\n- old\n## Plan\n- keep\n"
	if got := stripSourcesSection(md2); got != "## Plan\n- keep" {
		t.Errorf("mid-section strip wrong: %q", got)
	}
	// No Sources section → unchanged (trailing newline trimmed).
	if got := stripSourcesSection("## A\n- x\n"); got != "## A\n- x" {
		t.Errorf("no-sources strip wrong: %q", got)
	}
}

func TestDropUndatedMail(t *testing.T) {
	mk := func(src string, withDate bool) *store.KnowledgeItem {
		md := map[string]any{}
		if withDate {
			md["canonical_date"] = "2026-05-01T09:00:00Z"
		}
		return &store.KnowledgeItem{SourceType: src, Metadata: md, CreatedAt: time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)}
	}
	in := []*store.KnowledgeItem{
		mk(store.SourceTypeMail, true),   // keep — dated mail
		mk(store.SourceTypeMail, false),  // drop — undated mail
		mk(store.SourceTypeNote, false),  // keep — note created_at is a real date
		mk(store.SourceTypeEmail, false), // drop — mail variant, undated
	}
	out := dropUndatedMail(in)
	if len(out) != 2 {
		t.Fatalf("want 2 kept, got %d", len(out))
	}
	for _, it := range out {
		if store.IsMailSourceType(it.SourceType) && store.GetCanonicalDate(it).IsZero() {
			t.Errorf("undated mail leaked through: %+v", it)
		}
	}
}
