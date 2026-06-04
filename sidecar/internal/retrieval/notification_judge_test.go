package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

func TestParseJudgeJSON(t *testing.T) {
	cases := []struct {
		raw        string
		wantNotify bool
		wantLine   string
		wantOK     bool
	}{
		{`{"notify": true, "line": "💰 Facture : 200 € avant le 30"}`, true, "💰 Facture : 200 € avant le 30", true},
		{`{"notify": false, "line": "📧 Newsletter"}`, false, "📧 Newsletter", true},
		{"Voici: {\"notify\": true, \"line\": \"⚠️ Échéance\"} merci", true, "⚠️ Échéance", true},
		{"pas de json", false, "", false},
		{"", false, "", false},
	}
	for _, c := range cases {
		notify, line, ok := parseJudgeJSON(c.raw)
		if ok != c.wantOK || notify != c.wantNotify || line != c.wantLine {
			t.Errorf("parseJudgeJSON(%q) = (%v,%q,%v), want (%v,%q,%v)",
				c.raw, notify, line, ok, c.wantNotify, c.wantLine, c.wantOK)
		}
	}
}

// judgeServer stubs the chat endpoint to return a fixed JSON verdict.
func judgeServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.ChatResponse{Choices: []llm.Choice{{
			Message: &llm.Message{Role: "assistant", Content: content},
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestSummarizeForNotification_LLMVetoesNoise(t *testing.T) {
	srv := judgeServer(t, `{"notify": false, "line": "📰 Newsletter hebdo"}`)
	defer srv.Close()
	s := NewMailSummarizer(llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client()))

	item := &store.KnowledgeItem{
		ContentID:      "email:noise",
		Title:          "Notre newsletter de juin",
		NormalizedText: "Découvrez nos nouveautés...",
		Metadata:       map[string]any{},
	}
	line, notify := s.SummarizeForNotification(context.Background(), item)
	if notify {
		t.Errorf("expected notify=false for newsletter, got true (line=%q)", line)
	}
}

func TestSummarizeForNotification_LLMKeepsActionable(t *testing.T) {
	srv := judgeServer(t, `{"notify": true, "line": "📝 Contrat à signer avant vendredi"}`)
	defer srv.Close()
	s := NewMailSummarizer(llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client()))

	item := &store.KnowledgeItem{
		ContentID:      "email:action",
		Title:          "Contrat de prestation",
		NormalizedText: "Merci de signer le contrat ci-joint avant vendredi.",
		Metadata:       map[string]any{},
	}
	line, notify := s.SummarizeForNotification(context.Background(), item)
	if !notify || line != "📝 Contrat à signer avant vendredi" {
		t.Errorf("expected notify=true with the line, got (%q, %v)", line, notify)
	}
}

func TestSummarizeForNotification_TemplatedAlwaysNotifies(t *testing.T) {
	// An item with amount+due_date+org renders from entities (no LLM) and must
	// notify. Point the client at a server that would FAIL if hit, to prove the
	// templated path short-circuits before any LLM call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("LLM must not be called on the templated path")
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := NewMailSummarizer(llm.NewClientWithHTTP(srv.URL, 1*time.Second, 0, srv.Client()))

	item := &store.KnowledgeItem{
		ContentID: "email:facture",
		Title:     "Facture",
		Metadata: map[string]any{
			"extracted_amounts":   []any{"200 €"},
			"extracted_due_dates": []any{"2026-06-30"},
			"extracted_orgs":      []any{"Cabinet X"},
		},
	}
	_, notify := s.SummarizeForNotification(context.Background(), item)
	if !notify {
		t.Error("templated actionable mail must notify")
	}
}

func TestSummarizeForNotification_ParseFailureFallsBackToNotify(t *testing.T) {
	srv := judgeServer(t, "je ne sais pas répondre en JSON désolé")
	defer srv.Close()
	s := NewMailSummarizer(llm.NewClientWithHTTP(srv.URL, 5*time.Second, 0, srv.Client()))

	item := &store.KnowledgeItem{
		ContentID:      "email:weird",
		Title:          "Sujet brut",
		NormalizedText: "corps",
		Metadata:       map[string]any{},
	}
	line, notify := s.SummarizeForNotification(context.Background(), item)
	// Unparseable verdict → conservative fallback: notify with the subject.
	if !notify || line == "" {
		t.Errorf("parse failure should fall back to notify with subject, got (%q, %v)", line, notify)
	}
}
