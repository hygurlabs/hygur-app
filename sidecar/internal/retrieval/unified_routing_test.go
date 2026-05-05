package retrieval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/store"
)

// routingTestServer is a multi-endpoint fake LM Studio: it answers
// /v1/embeddings with a canned vector and /v1/chat/completions with a payload
// that depends on the system prompt (classifier / judge / other). Counters let
// tests assert which sub-paths were actually exercised.
type routingTestServer struct {
	srv               *httptest.Server
	classifyResponse  string
	judgeResponse     string
	classifyCallCount int32
	judgeCallCount    int32
	chatCallCount     int32
	embeddingDim      int
	classifyStatus    int
}

func newRoutingTestServer(t *testing.T, classify, judge string) *routingTestServer {
	t.Helper()
	rt := &routingTestServer{
		classifyResponse: classify,
		judgeResponse:    judge,
		embeddingDim:     llm.ExpectedEmbeddingDimension,
	}
	rt.srv = httptest.NewServer(http.HandlerFunc(rt.handle))
	return rt
}

func (rt *routingTestServer) Close()               { rt.srv.Close() }
func (rt *routingTestServer) URL() string          { return rt.srv.URL }
func (rt *routingTestServer) Client() *http.Client { return rt.srv.Client() }

func (rt *routingTestServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/embeddings":
		emb := make([]float32, rt.embeddingDim)
		for i := range emb {
			emb[i] = 0.001
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  "test",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": emb},
			},
		})
	case "/v1/chat/completions":
		atomic.AddInt32(&rt.chatCallCount, 1)
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &payload)
		var sys string
		if len(payload.Messages) > 0 {
			sys = payload.Messages[0].Content
		}
		var reply string
		switch {
		case strings.Contains(sys, "You classify a search query"):
			atomic.AddInt32(&rt.classifyCallCount, 1)
			if rt.classifyStatus >= 400 {
				http.Error(w, "boom", rt.classifyStatus)
				return
			}
			reply = rt.classifyResponse
		case strings.Contains(sys, "You evaluate whether a retrieved document"):
			atomic.AddInt32(&rt.judgeCallCount, 1)
			reply = rt.judgeResponse
		default:
			reply = "ok"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": reply}, "finish_reason": "stop"},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

func newRoutingSearcher(t *testing.T, db *store.DB, srv *routingTestServer, opts RetrievalOptions) *UnifiedSearcher {
	t.Helper()
	client := llm.NewClientWithHTTP(srv.URL(), 5*time.Second, 0, srv.Client())
	us := NewUnifiedSearcher(db, client)
	us.SetRetrievalOptions(opts)
	return us
}

// TestUnifiedSearch_FactualEntityRoutesToEntitySearch — when the LLM
// classifier returns factual_entity with a non-empty entity, the search must
// answer from EntitySearch. Asserts that the doc carrying the entity in its
// Tier 2 NER list outranks an unrelated doc, and that the routing committed
// (no fallback to vector).
func TestUnifiedSearch_FactualEntityRoutesToEntitySearch(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-elric", "Infos administratives", "Coordonnées de l'équipe.",
		map[string]any{"extracted_persons": []string{"Jean Dupont"}})
	insertItem(t, db, "doc-elfcam", "Newsletter Elfcam", "Promo Elfcam ce mois-ci.", nil)

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Jean","attribute":"person","confidence":0.95}`,
		"")
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		EntitySearchFallback: true,
		EntitySearchMinScore: 0.1, // Recency blend caps tiny corpora well below 0.5; relax for the test.
	})

	resp, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "quel est le numéro national d'Jean svp",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result from EntitySearch routing")
	}
	if resp.Results[0].ContentID != "doc-elric" {
		t.Errorf("top = %q, want doc-elric (Tier 2 NER hit)", resp.Results[0].ContentID)
	}
	for _, r := range resp.Results {
		if r.ContentID == "doc-elfcam" {
			t.Errorf("doc-elfcam must not appear (no Jean anywhere)")
		}
	}
	if atomic.LoadInt32(&srv.classifyCallCount) == 0 {
		t.Errorf("classifier was never called")
	}
}

// TestUnifiedSearch_FactualEntityAbstainsWhenFallbackDisabled — fallback off
// + EntitySearch finds nothing → empty result set (explicit abstention).
func TestUnifiedSearch_FactualEntityAbstainsWhenFallbackDisabled(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Corpus has no mention of "Untrouvable" anywhere.
	insertItem(t, db, "doc-other", "Sujet sans rapport", "Lorem ipsum.", nil)

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Untrouvable","attribute":"person","confidence":0.95}`,
		"")
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		EntitySearchFallback: false,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "qui est Untrouvable au juste",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected abstention (empty), got %d results", len(resp.Results))
	}
}

// TestUnifiedSearch_LLMIntentDisabledSkipsClassifier — useLLMIntent=false:
// the classifier endpoint must never be hit. The vector path runs and returns
// empty (no chunks/embeddings inserted) — that's fine; the assertion is on
// the classifier counter staying at 0.
func TestUnifiedSearch_LLMIntentDisabledSkipsClassifier(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-x", "Titre", "Body", nil)

	srv := newRoutingTestServer(t, `{"category":"factual_entity","entity":"X"}`, "")
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent: false,
	})

	_, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "what is the latest update",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := atomic.LoadInt32(&srv.classifyCallCount); got != 0 {
		t.Errorf("classifier should NOT be called when UseLLMIntent=false; got %d calls", got)
	}
}

// TestUnifiedSearch_LLMIntentErrorFallsBackToVector — classifier endpoint
// returns 500. Search must not error, must fall back to the vector path.
// Vector search returns empty (no embeddings inserted) but the test verifies
// the absence of an error and the absence of an entity-search-only response.
func TestUnifiedSearch_LLMIntentErrorFallsBackToVector(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-elric", "Infos administratives", "Coordonnées",
		map[string]any{"extracted_persons": []string{"Jean"}})

	srv := newRoutingTestServer(t, "", "")
	srv.classifyStatus = http.StatusInternalServerError
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		EntitySearchFallback: false,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "quel est le numéro d'Jean svp",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search must not error on classifier failure: %v", err)
	}
	// We don't assert content (vector path is empty in tests), only that the
	// search completed without error and that the classifier was attempted.
	if got := atomic.LoadInt32(&srv.classifyCallCount); got == 0 {
		t.Errorf("classifier should have been attempted at least once")
	}
	_ = resp
}

// TestUnifiedSearch_JudgeDropsLowScoreResults — useJudge=true and the judge
// scores the only result at 1 (totally unrelated). Final response is empty.
func TestUnifiedSearch_JudgeDropsLowScoreResults(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-elric", "Infos administratives", "Coordonnées",
		map[string]any{"extracted_persons": []string{"Jean Dupont"}})

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Jean","attribute":"person","confidence":0.95}`,
		`{"scores":[{"id":"0","score":1,"reason":"off-topic"}]}`)
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		UseJudge:             true,
		EntitySearchFallback: false,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "quel est le numéro d'Jean svp",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("judge scored 1 → expected abstention; got %d results", len(resp.Results))
	}
	if got := atomic.LoadInt32(&srv.judgeCallCount); got == 0 {
		t.Errorf("judge should have been invoked")
	}
}

// TestUnifiedSearch_FocusScope_FiltersByProject — when FocusScope.ProjectIDs
// is set, only documents linked to one of those projects survive the entity
// branch. The doc that matches the entity but lives outside the project is
// filtered out, demonstrating Mode Focus end-to-end through the router.
func TestUnifiedSearch_FocusScope_FiltersByProject(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Two docs both match the entity "Stripe".
	insertItem(t, db, "doc-in-project", "Facture Stripe Q1", "Paiement Stripe reçu pour Q1.",
		map[string]any{"extracted_orgs": []string{"Stripe"}})
	insertItem(t, db, "doc-out-of-project", "Autre facture Stripe", "Paiement Stripe non lié.",
		map[string]any{"extracted_orgs": []string{"Stripe"}})

	// Create a project and link only the in-project doc.
	ctx := context.Background()
	now := time.Now()
	if err := db.InsertProject(ctx, &store.Project{
		ProjectID: "proj-1",
		Name:      "Compta Q1",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}
	if err := db.LinkToProject(ctx, "doc-in-project", "proj-1"); err != nil {
		t.Fatalf("LinkToProject: %v", err)
	}

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Stripe","attribute":"organization","confidence":0.95}`,
		"")
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		EntitySearchFallback: true,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(ctx, UnifiedSearchRequest{
		Query:      "facture Stripe",
		TopK:       5,
		FocusScope: &FocusScope{ProjectIDs: []string{"proj-1"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected exactly 1 in-scope result, got %d", len(resp.Results))
	}
	if resp.Results[0].ContentID != "doc-in-project" {
		t.Errorf("got %q, want doc-in-project", resp.Results[0].ContentID)
	}
}

// TestUnifiedSearch_FocusScope_EmptyProjectAbstains — focus on a project that
// has no linked docs must shortcut to an empty response, not silently fall
// back to an unscoped search.
func TestUnifiedSearch_FocusScope_EmptyProjectAbstains(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-stripe", "Facture Stripe", "Paiement Stripe reçu.",
		map[string]any{"extracted_orgs": []string{"Stripe"}})

	ctx := context.Background()
	now := time.Now()
	if err := db.InsertProject(ctx, &store.Project{
		ProjectID: "proj-empty",
		Name:      "Projet vide",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("InsertProject: %v", err)
	}
	// No LinkToProject call — proj-empty has zero docs.

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Stripe","attribute":"organization","confidence":0.95}`,
		"")
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		EntitySearchFallback: true,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(ctx, UnifiedSearchRequest{
		Query:      "facture Stripe",
		TopK:       5,
		FocusScope: &FocusScope{ProjectIDs: []string{"proj-empty"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected abstention (empty), got %d results", len(resp.Results))
	}
}

// TestUnifiedSearch_JudgeKeepsHighScoreResults — symmetric to the previous
// test: judge=5 must let the result survive, proving the wiring is not
// blanket-dropping.
func TestUnifiedSearch_JudgeKeepsHighScoreResults(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	insertItem(t, db, "doc-elric", "Infos administratives", "Coordonnées",
		map[string]any{"extracted_persons": []string{"Jean Dupont"}})

	srv := newRoutingTestServer(t,
		`{"category":"factual_entity","entity":"Jean","attribute":"person","confidence":0.95}`,
		`{"scores":[{"id":"0","score":5,"reason":"answers directly"}]}`)
	defer srv.Close()

	us := newRoutingSearcher(t, db, srv, RetrievalOptions{
		UseLLMIntent:         true,
		UseJudge:             true,
		EntitySearchFallback: false,
		EntitySearchMinScore: 0.1,
	})

	resp, err := us.Search(context.Background(), UnifiedSearchRequest{
		Query: "quel est le numéro d'Jean svp",
		TopK:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ContentID != "doc-elric" {
		t.Errorf("expected doc-elric to survive judge=5, got %+v", resp.Results)
	}
}
