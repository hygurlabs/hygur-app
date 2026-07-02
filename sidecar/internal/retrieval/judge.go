package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/llm"
)

const (
	judgeMaxTokens = 1024
	judgeTimeout   = 30 * time.Second
	// JudgeAbstainThreshold is the score below which a result is considered
	// non-pertinent. If the best result scores at or below this, the judge
	// recommends abstention ("aucun résultat pertinent").
	JudgeAbstainThreshold = 2
	// JudgeKeepThreshold is the minimum score required to keep a result in
	// the final output. Results scoring below this are dropped.
	JudgeKeepThreshold = 3
)

// JudgedResult wraps a UnifiedResult with the LLM-assigned relevance score
// and the explanation produced by the judge.
type JudgedResult struct {
	Result UnifiedResult `json:"result"`
	Score  int           `json:"score"` // 1..5
	Reason string        `json:"reason"`
}

// JudgeVerdict is the outcome of judging a list of results.
type JudgeVerdict struct {
	Kept      []JudgedResult `json:"kept"`
	Dropped   []JudgedResult `json:"dropped"`
	Abstain   bool           `json:"abstain"`
	BestScore int            `json:"best_score"`
}

const judgeSystemPrompt = `You evaluate whether a retrieved document actually answers the user's question.
For each document, output an integer score from 1 to 5 and a one-sentence reason.

Scoring scale:
1 = totally unrelated to the question
2 = same topic but does not contain the answer
3 = mentions the subject but answer is incomplete
4 = answers partially
5 = answers the question directly and completely

Respond ONLY with valid JSON, no commentary, no markdown fences. Schema:
{"scores":[{"id":"<doc_id>","score":<1-5>,"reason":"<short>"}, ...]}`

// Judge asks the LLM to score each result's relevance to the query, then
// drops irrelevant results and recommends abstention when nothing is
// pertinent. Falls back to returning the input unchanged on LLM error so
// the caller can degrade gracefully.
func Judge(ctx context.Context, client *llm.Client, query string, results []UnifiedResult) (*JudgeVerdict, error) {
	if len(results) == 0 {
		return &JudgeVerdict{Abstain: true}, nil
	}
	if client == nil {
		return nil, fmt.Errorf("judge: nil llm client")
	}

	userPrompt := buildJudgePrompt(query, results)

	jctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	resp, err := client.Chat(jctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: judgeSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: llm.Temp(0),
		TopP:        llm.Temp(1),
		Seed:        llm.SeedOf(42),
		MaxTokens:   judgeMaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("judge: chat failed: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return nil, fmt.Errorf("judge: empty response")
	}

	// Reasoning-capable backends route the answer to `reasoning` when the
	// whole turn is treated as a thinking block. Fall back to it so the judge
	// still works on those models even if /no_think isn't honored.
	rawAnswer := resp.Choices[0].Message.Content
	if strings.TrimSpace(rawAnswer) == "" {
		rawAnswer = resp.Choices[0].Message.Reasoning
	}
	scores, err := parseJudgeResponse(rawAnswer)
	if err != nil {
		return nil, fmt.Errorf("judge: parse: %w", err)
	}

	return assemble(results, scores), nil
}

func buildJudgePrompt(query string, results []UnifiedResult) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(query)
	b.WriteString("\n\nDocuments:\n")
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.MailSubject
		}
		excerpt := r.Excerpt
		if len(excerpt) > 600 {
			excerpt = excerpt[:600] + "…"
		}
		fmt.Fprintf(&b, "\n[id=%d] source=%s title=%q\n%s\n", i, r.SourceType, title, excerpt)
	}
	b.WriteString("\nReturn the JSON now.")
	return b.String()
}

type judgeScoreEntry struct {
	ID     string `json:"id"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

type judgeResponse struct {
	Scores []judgeScoreEntry `json:"scores"`
}

// unmarshalJudgeEntries accepts either {"scores":[...]} or a bare [...].
func unmarshalJudgeEntries(raw []byte) ([]judgeScoreEntry, error) {
	trimmed := strings.TrimLeft(string(raw), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var arr []judgeScoreEntry
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var wrapped judgeResponse
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Scores, nil
}

func parseJudgeResponse(raw string) (map[int]judgeScoreEntry, error) {
	raw = strings.TrimSpace(raw)
	// Strip markdown fences if the model added them despite instructions.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Some reasoning models prefix output with <think>...</think>; strip it.
	if i := strings.Index(raw, "</think>"); i >= 0 {
		raw = strings.TrimSpace(raw[i+len("</think>"):])
	}

	// Accept both the documented wrapper {"scores":[...]} and the bare array
	// [...] that some models emit despite the schema instruction.
	entries, err := unmarshalJudgeEntries([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid json: %w (raw=%q)", err, truncate(raw, 200))
	}

	out := make(map[int]judgeScoreEntry, len(entries))
	for _, s := range entries {
		var idx int
		if _, err := fmt.Sscanf(s.ID, "%d", &idx); err != nil {
			continue
		}
		if s.Score < 1 {
			s.Score = 1
		}
		if s.Score > 5 {
			s.Score = 5
		}
		out[idx] = s
	}
	return out, nil
}

func assemble(results []UnifiedResult, scores map[int]judgeScoreEntry) *JudgeVerdict {
	v := &JudgeVerdict{}
	for i, r := range results {
		entry, ok := scores[i]
		if !ok {
			// No score returned → conservative: drop with score 1.
			entry = judgeScoreEntry{Score: 1, Reason: "no score returned by judge"}
		}
		jr := JudgedResult{Result: r, Score: entry.Score, Reason: entry.Reason}
		if entry.Score > v.BestScore {
			v.BestScore = entry.Score
		}
		if entry.Score >= JudgeKeepThreshold {
			v.Kept = append(v.Kept, jr)
		} else {
			v.Dropped = append(v.Dropped, jr)
		}
	}
	v.Abstain = v.BestScore <= JudgeAbstainThreshold
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// JudgeAndFilter scores each result with the LLM judge and returns only those
// at or above `threshold` (1..5). On LLM/parse error the input is returned
// unchanged so the search degrades gracefully — abstention from the judge must
// never hide otherwise good results behind a transient backend hiccup.
//
// threshold ≤ 0 or > 5 falls back to JudgeKeepThreshold (3). Pass 2 to keep
// "topic match" results that don't directly answer the question; pass 4 for a
// strict "must directly answer" mode.
func JudgeAndFilter(ctx context.Context, client *llm.Client, query string, results []UnifiedResult, threshold int) ([]UnifiedResult, error) {
	if len(results) == 0 {
		return results, nil
	}
	if client == nil {
		return results, nil
	}
	if threshold < 1 || threshold > 5 {
		threshold = JudgeKeepThreshold
	}

	verdict, err := Judge(ctx, client, query, results)
	if err != nil {
		return results, err
	}

	// Walk Kept then Dropped so the returned slice preserves the relative
	// "judge prefers these" ordering. Caller may re-sort by its own score.
	out := make([]UnifiedResult, 0, len(verdict.Kept)+len(verdict.Dropped))
	for _, jr := range verdict.Kept {
		if jr.Score >= threshold {
			out = append(out, jr.Result)
		}
	}
	for _, jr := range verdict.Dropped {
		if jr.Score >= threshold {
			out = append(out, jr.Result)
		}
	}
	return out, nil
}
