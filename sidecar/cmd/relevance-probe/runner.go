package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/hygur/sidecar/internal/retrieval"
	"github.com/hygur/sidecar/internal/store"
)

// runner executes a single query against multiple strategies and returns
// the per-strategy results for side-by-side display.
type runner struct {
	searcher          *retrieval.UnifiedSearcher
	authoritySearcher *retrieval.UnifiedSearcher // same pipeline + M2 authority re-score
	llm               *llm.Client
	db                *store.DB
	topK              int
	strategies        []string
}

// strategyReport holds the output of one strategy on one query.
type strategyReport struct {
	Name     string                  `json:"name"`
	Duration time.Duration           `json:"duration_ms"`
	Abstain  bool                    `json:"abstain"`
	Note     string                  `json:"note,omitempty"`
	Results  []strategyResult        `json:"results"`
	Error    string                  `json:"error,omitempty"`
	Verdict  *retrieval.JudgeVerdict `json:"-"`
}

// strategyResult is a single ranked item returned by a strategy.
type strategyResult struct {
	Rank       int     `json:"rank"`
	Title      string  `json:"title"`
	SourceType string  `json:"source_type"`
	Tier       string  `json:"tier,omitempty"`
	Validity   string  `json:"validity,omitempty"`
	Score      float64 `json:"score"`
	Excerpt    string  `json:"excerpt"`
	JudgeScore int     `json:"judge_score,omitempty"`
	JudgeNote  string  `json:"judge_note,omitempty"`
	ContentID  string  `json:"content_id,omitempty"`
}

// queryReport bundles all strategy outputs for one query.
type queryReport struct {
	Query   string           `json:"query"`
	Reports []strategyReport `json:"strategies"`
}

func (r *runner) run(ctx context.Context, query string) queryReport {
	report := queryReport{Query: query}

	// Step 1: always run the baseline since strategy A reuses its results.
	baseline := r.runBaseline(ctx, query)

	for _, name := range r.strategies {
		switch name {
		case "baseline":
			report.Reports = append(report.Reports, baseline)
		case "judge":
			report.Reports = append(report.Reports, r.runJudge(ctx, query, baseline))
		case "intent":
			report.Reports = append(report.Reports, r.runIntent(ctx, query, baseline))
		case "authority":
			report.Reports = append(report.Reports, r.runAuthority(ctx, query))
		}
	}
	return report
}

// runAuthority runs the production pipeline WITH the M2 authority re-score on, so
// it can be compared side-by-side with baseline. When no result carries authority
// (all capture/current) the order must match baseline exactly — the regression
// guarantee.
func (r *runner) runAuthority(ctx context.Context, query string) strategyReport {
	start := time.Now()
	resp, err := r.authoritySearcher.Search(ctx, retrieval.UnifiedSearchRequest{Query: query, TopK: r.topK})
	dur := time.Since(start)
	if err != nil {
		return strategyReport{Name: "authority", Duration: dur, Error: err.Error()}
	}
	out := strategyReport{Name: "authority", Duration: dur}
	for i, res := range resp.Results {
		out.Results = append(out.Results, strategyResult{
			Rank:       i + 1,
			Title:      preferredTitle(res),
			SourceType: res.SourceType,
			Tier:       string(res.Tier),
			Validity:   string(res.Validity),
			Score:      res.Score,
			Excerpt:    excerpt(res.Excerpt, 200),
			ContentID:  res.ContentID,
		})
	}
	return out
}

// runBaseline runs the production UnifiedSearcher with default options.
func (r *runner) runBaseline(ctx context.Context, query string) strategyReport {
	start := time.Now()
	resp, err := r.searcher.Search(ctx, retrieval.UnifiedSearchRequest{
		Query: query,
		TopK:  r.topK,
	})
	dur := time.Since(start)
	if err != nil {
		return strategyReport{Name: "baseline", Duration: dur, Error: err.Error()}
	}

	out := strategyReport{Name: "baseline", Duration: dur}
	for i, res := range resp.Results {
		out.Results = append(out.Results, strategyResult{
			Rank:       i + 1,
			Title:      preferredTitle(res),
			SourceType: res.SourceType,
			Tier:       string(res.Tier),
			Validity:   string(res.Validity),
			Score:      res.Score,
			Excerpt:    excerpt(res.Excerpt, 200),
			ContentID:  res.ContentID,
		})
	}
	return out
}

// runJudge takes the baseline results, runs the LLM judge, drops irrelevant
// items, and emits abstention when nothing scores above the threshold.
func (r *runner) runJudge(ctx context.Context, query string, baseline strategyReport) strategyReport {
	if baseline.Error != "" {
		return strategyReport{Name: "judge", Error: "baseline failed: " + baseline.Error}
	}

	// Reconstruct UnifiedResults from the baseline report — but the runner
	// only kept the projected fields. Re-run the search to get full results;
	// cheap because the embedding is cached on the LLM side and the work is
	// in-memory cosine.
	start := time.Now()
	resp, err := r.searcher.Search(ctx, retrieval.UnifiedSearchRequest{
		Query: query,
		TopK:  r.topK,
	})
	if err != nil {
		return strategyReport{Name: "judge", Duration: time.Since(start), Error: err.Error()}
	}

	verdict, err := retrieval.Judge(ctx, r.llm, query, resp.Results)
	dur := time.Since(start)
	if err != nil {
		return strategyReport{Name: "judge", Duration: dur, Error: err.Error()}
	}

	out := strategyReport{Name: "judge", Duration: dur, Verdict: verdict, Abstain: verdict.Abstain}
	if verdict.Abstain {
		out.Note = "no result reached the relevance threshold (best score ≤ 2)"
		// Still surface what was dropped for visibility.
		for i, jr := range verdict.Dropped {
			out.Results = append(out.Results, strategyResult{
				Rank:       i + 1,
				Title:      preferredTitle(jr.Result),
				SourceType: jr.Result.SourceType,
				Score:      jr.Result.Score,
				Excerpt:    excerpt(jr.Result.Excerpt, 200),
				JudgeScore: jr.Score,
				JudgeNote:  jr.Reason,
				ContentID:  jr.Result.ContentID,
			})
		}
		return out
	}

	for i, jr := range verdict.Kept {
		out.Results = append(out.Results, strategyResult{
			Rank:       i + 1,
			Title:      preferredTitle(jr.Result),
			SourceType: jr.Result.SourceType,
			Score:      jr.Result.Score,
			Excerpt:    excerpt(jr.Result.Excerpt, 200),
			JudgeScore: jr.Score,
			JudgeNote:  jr.Reason,
			ContentID:  jr.Result.ContentID,
		})
	}
	return out
}

// runIntent classifies the query, then routes:
//   - factual_entity → EntitySearch on the structured fields
//   - topic / temporal / conversational → falls back to the baseline pipeline
//
// The classification (category, entity, attribute) is surfaced in Note for
// observability so we can see whether the LLM mis-classifies a query before
// blaming the retriever.
func (r *runner) runIntent(ctx context.Context, query string, baseline strategyReport) strategyReport {
	start := time.Now()
	intent, err := retrieval.ClassifyQuery(ctx, r.llm, query)
	if err != nil {
		return strategyReport{Name: "intent", Duration: time.Since(start), Error: "classify: " + err.Error()}
	}

	classification := fmt.Sprintf("category=%s entity=%q attribute=%q confidence=%.2f",
		intent.Category, intent.Entity, intent.Attribute, intent.Confidence)

	switch intent.Category {
	case retrieval.IntentFactualEntity:
		if intent.Entity == "" {
			out := strategyReport{
				Name:     "intent",
				Duration: time.Since(start),
				Abstain:  true,
				Note:     classification + " — factual_entity without entity name → abstention",
			}
			return out
		}
		results, err := retrieval.EntitySearch(ctx, r.db, intent, retrieval.EntitySearchOptions{
			TopK: r.topK,
		})
		dur := time.Since(start)
		if err != nil {
			return strategyReport{Name: "intent", Duration: dur, Error: "entity search: " + err.Error()}
		}
		out := strategyReport{Name: "intent", Duration: dur, Note: classification}
		if len(results) == 0 {
			out.Abstain = true
			out.Note = classification + " — no document mentions the entity → abstention"
			return out
		}
		for i, res := range results {
			out.Results = append(out.Results, strategyResult{
				Rank:       i + 1,
				Title:      preferredTitle(res),
				SourceType: res.SourceType,
				Score:      res.Score,
				Excerpt:    excerpt(res.Excerpt, 200),
				ContentID:  res.ContentID,
			})
		}
		return out

	default:
		// topic / temporal / conversational → reuse the baseline. We re-emit it
		// under the "intent" name so the comparison table stays aligned, and we
		// annotate it with the routing decision.
		out := strategyReport{
			Name:     "intent",
			Duration: time.Since(start) + baseline.Duration,
			Note:     classification + " — routed to baseline pipeline",
			Error:    baseline.Error,
		}
		out.Results = append(out.Results, baseline.Results...)
		return out
	}
}

func preferredTitle(r retrieval.UnifiedResult) string {
	if r.Title != "" {
		return r.Title
	}
	if r.MailSubject != "" {
		return r.MailSubject
	}
	return "(untitled)"
}

func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
