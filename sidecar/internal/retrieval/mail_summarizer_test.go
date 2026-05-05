package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// TestSummarize_TemplatedFromEntities verifies the deterministic path: when
// org + amount + due_date are all present, no LLM call is made and the
// resulting line follows the typed gabarit. We pass a nil llm.Client so any
// call would deref-panic and fail the test.
func TestSummarize_TemplatedFromEntities(t *testing.T) {
	s := NewMailSummarizer(nil) // nil → LLM fallback would panic
	item := &store.KnowledgeItem{
		ContentID: "email:1",
		Title:     "Votre facture",
		Metadata: map[string]any{
			"extracted_amounts":   []string{"23.50 EUR"},
			"extracted_due_dates": []string{"2026-07-15"},
			"extracted_orgs":      []string{"Chargemap"},
		},
	}
	got, err := s.SummarizeMailOneLiner(context.Background(), item)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "Chargemap") || !strings.Contains(got, "23.50 EUR") || !strings.Contains(got, "2026-07-15") {
		t.Fatalf("templated output missing fields: %q", got)
	}
	if !strings.HasPrefix(got, "💰") {
		t.Fatalf("templated invoice should start with 💰: %q", got)
	}
}

// TestSummarize_TopicMeeting checks the "rdv" branch that uses topics + org +
// due_date but no amount.
func TestSummarize_TopicMeeting(t *testing.T) {
	s := NewMailSummarizer(nil)
	item := &store.KnowledgeItem{
		ContentID: "email:2",
		Title:     "Confirmation rendez-vous",
		Metadata: map[string]any{
			"extracted_due_dates": []string{"2026-06-12"},
			"extracted_orgs":      []string{"Cabinet Acme Compta"},
			"extracted_topics":    []string{"rendez-vous"},
		},
	}
	got, _ := s.SummarizeMailOneLiner(context.Background(), item)
	if !strings.HasPrefix(got, "📅") {
		t.Fatalf("expected meeting glyph, got %q", got)
	}
	if !strings.Contains(got, "Cabinet Acme Compta") {
		t.Fatalf("expected org in line, got %q", got)
	}
}

// TestSummarize_FallsBackToLLM exercises the LLM path when entities are
// incomplete. We stub LM Studio's chat endpoint and assert the cleaned body
// is returned verbatim.
func TestSummarize_FallsBackToLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		resp := llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: &llm.Message{Role: "assistant", Content: "💼 Note de frais : provision janvier"},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())
	s := NewMailSummarizer(client)
	item := &store.KnowledgeItem{
		ContentID:      "email:3",
		Title:          "Provision janvier",
		NormalizedText: "Salut, voici la provision pour janvier.",
		Metadata:       map[string]any{},
	}
	got, err := s.SummarizeMailOneLiner(context.Background(), item)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "💼 Note de frais : provision janvier" {
		t.Fatalf("got %q", got)
	}
}

// TestSummarize_FailSoftToSubject ensures the subject is used when both the
// templated path and the LLM are unavailable.
func TestSummarize_FailSoftToSubject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := llm.NewClientWithHTTP(srv.URL, 1*time.Second, 0, srv.Client())
	s := NewMailSummarizer(client)
	item := &store.KnowledgeItem{
		ContentID: "email:4",
		Title:     "Sujet brut",
		Metadata:  map[string]any{},
	}
	got, _ := s.SummarizeMailOneLiner(context.Background(), item)
	if !strings.HasPrefix(got, "📧") || !strings.Contains(got, "Sujet brut") {
		t.Fatalf("fail-soft should include subject, got %q", got)
	}
}

// TestSummarize_CachesSecondCall ensures repeat calls hit the cache (no
// second LLM call). We tear the server down after the first call and assert
// the same result still comes back.
func TestSummarize_CachesSecondCall(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: &llm.Message{Role: "assistant", Content: "🧾 Reçu"},
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	client := llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client())
	s := NewMailSummarizer(client)
	item := &store.KnowledgeItem{
		ContentID:      "email:5",
		Title:          "Reçu cb",
		NormalizedText: "Reçu de paiement Visa.",
		Metadata:       map[string]any{},
	}
	first, _ := s.SummarizeMailOneLiner(context.Background(), item)
	second, _ := s.SummarizeMailOneLiner(context.Background(), item)
	if first != second {
		t.Fatalf("cache miss: %q != %q", first, second)
	}
	if calls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", calls)
	}
}
