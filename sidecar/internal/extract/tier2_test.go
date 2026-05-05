package extract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

// fakeLLMServer returns an httptest.Server that replies with a fixed chat
// completion content. Used by Tier 2 tests to simulate a deterministic NER
// model without depending on a real LLM endpoint.
func fakeLLMServer(t *testing.T, responseContent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": responseContent,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func errorLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
}

func newTestClient(t *testing.T, srv *httptest.Server) *llm.Client {
	t.Helper()
	return llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())
}

func TestExtractTier2_BasicEntities(t *testing.T) {
	body := `{
		"persons": ["Jean Dupont", "Jean Dupont"],
		"organizations": ["Acme Compta", "SPF Finances"],
		"event_dates": [{"date":"2026-04-30","context":"déclaration TVA Q1"}],
		"projects": ["Hygur"],
		"topics": ["TVA", "facturation"]
	}`
	srv := fakeLLMServer(t, body)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "Some document text mentioning Jean and Hygur.")
	if err != nil {
		t.Fatalf("ExtractTier2: %v", err)
	}
	if got.Count() != 2+2+1+1+2 {
		t.Errorf("Count = %d, want 8", got.Count())
	}
	if got.Persons[0] != "Jean Dupont" {
		t.Errorf("Persons[0] = %q", got.Persons[0])
	}
	if got.Topics[0] != "tva" {
		t.Errorf("Topics should be lowercased; got %q", got.Topics[0])
	}
	if got.EventDates[0].Date != "2026-04-30" || got.EventDates[0].Context != "déclaration TVA Q1" {
		t.Errorf("EventDates[0] = %+v", got.EventDates[0])
	}
}

func TestExtractTier2_MalformedJSONReturnsError(t *testing.T) {
	srv := fakeLLMServer(t, "this is not json at all")
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "doc")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if got.Count() != 0 {
		t.Errorf("expected zero entities on parse error, got %d", got.Count())
	}
}

func TestExtractTier2_LLMErrorReturnsEmpty(t *testing.T) {
	srv := errorLLMServer(t)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "doc")
	if err == nil {
		t.Fatal("expected LLM error")
	}
	if got.Count() != 0 {
		t.Errorf("expected zero entities on LLM error, got %d", got.Count())
	}
}

func TestExtractTier2_MarkdownFencesStripped(t *testing.T) {
	wrapped := "```json\n{\"persons\":[\"Alice\"],\"topics\":[\"rh\"]}\n```"
	srv := fakeLLMServer(t, wrapped)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "doc")
	if err != nil {
		t.Fatalf("ExtractTier2: %v", err)
	}
	if len(got.Persons) != 1 || got.Persons[0] != "Alice" {
		t.Errorf("Persons = %v", got.Persons)
	}
}

func TestExtractTier2_ThinkBlockStripped(t *testing.T) {
	wrapped := "<think>let me reason</think>{\"persons\":[\"Bob\"]}"
	srv := fakeLLMServer(t, wrapped)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "doc")
	if err != nil {
		t.Fatalf("ExtractTier2: %v", err)
	}
	if len(got.Persons) != 1 || got.Persons[0] != "Bob" {
		t.Errorf("Persons = %v", got.Persons)
	}
}

func TestExtractTier2_BareArrayShape(t *testing.T) {
	wrapped := "[{\"persons\":[\"Carol\"],\"topics\":[\"juridique\"]}]"
	srv := fakeLLMServer(t, wrapped)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "doc")
	if err != nil {
		t.Fatalf("ExtractTier2: %v", err)
	}
	if len(got.Persons) != 1 || got.Persons[0] != "Carol" {
		t.Errorf("Persons = %v", got.Persons)
	}
}

func TestExtractTier2_EmptyDocReturnsEmpty(t *testing.T) {
	srv := fakeLLMServer(t, `{"persons":["should not be reached"]}`)
	defer srv.Close()
	client := newTestClient(t, srv)

	got, err := ExtractTier2(context.Background(), client, "   ")
	if err != nil {
		t.Fatalf("ExtractTier2: %v", err)
	}
	if got.Count() != 0 {
		t.Errorf("expected empty result for empty input, got %+v", got)
	}
}

func TestNormalizeTier2_DeduplicatesAndCaps(t *testing.T) {
	t2 := Tier2Entities{
		Persons: []string{"Alice", "alice", "ALICE", "Bob", "  ", ""},
		Topics:  []string{"TVA", "tva", "Facturation"},
	}
	got := normalizeTier2(t2)
	if len(got.Persons) != 2 {
		t.Errorf("Persons should dedupe case-insensitively, got %v", got.Persons)
	}
	if len(got.Topics) != 2 || got.Topics[0] != "tva" {
		t.Errorf("Topics should dedupe + lowercase, got %v", got.Topics)
	}
}

func TestMergeTier2IntoMetadata_WritesExpectedKeys(t *testing.T) {
	metadata := map[string]any{}
	t2 := Tier2Entities{
		Persons:       []string{"Alice"},
		Organizations: []string{"Acme"},
		EventDates:    []EventDate{{Date: "2026-04-30", Context: "deadline"}},
		Projects:      []string{"Hygur"},
		Topics:        []string{"rh"},
	}
	MergeTier2IntoMetadata(metadata, t2)

	for _, key := range []string{
		"extracted_persons", "extracted_orgs", "extracted_event_dates",
		"extracted_projects", "extracted_topics",
		"extracted_v2_at", "extracted_v2_version",
	} {
		if _, ok := metadata[key]; !ok {
			t.Errorf("metadata[%q] missing", key)
		}
	}
	if metadata["extracted_v2_version"] != Tier2Version {
		t.Errorf("version = %v, want %s", metadata["extracted_v2_version"], Tier2Version)
	}
}

func TestMergeTier2IntoMetadata_EmptyEntitiesStillStampVersion(t *testing.T) {
	metadata := map[string]any{}
	MergeTier2IntoMetadata(metadata, Tier2Entities{})

	// extracted_* lists should NOT be set
	for _, key := range []string{"extracted_persons", "extracted_orgs", "extracted_projects", "extracted_topics", "extracted_event_dates"} {
		if _, ok := metadata[key]; ok {
			t.Errorf("metadata[%q] should not be set for empty Tier2", key)
		}
	}
	// But the version stamp must be present, so the backfill CLI knows the doc
	// has been processed (and won't re-process it).
	if _, ok := metadata["extracted_v2_at"]; !ok {
		t.Error("extracted_v2_at should be set even when no entities found")
	}
}
