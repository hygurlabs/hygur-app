package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func emitMarkdown(w io.Writer, reports []queryReport) {
	for i, qr := range reports {
		if i > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "---")
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "## Query: %s\n\n", qr.Query)
		for _, sr := range qr.Reports {
			fmt.Fprintf(w, "### %s  *(%s)*\n", sr.Name, sr.Duration.Round(1e6))
			if sr.Error != "" {
				fmt.Fprintf(w, "\n**Error:** %s\n\n", sr.Error)
				continue
			}
			if sr.Note != "" {
				fmt.Fprintf(w, "\n_%s_\n\n", sr.Note)
			}
			if len(sr.Results) == 0 {
				fmt.Fprintln(w, "\n_(no results)_")
				continue
			}
			writeResultsTable(w, sr)
			fmt.Fprintln(w)
		}
	}
}

func writeResultsTable(w io.Writer, sr strategyReport) {
	includeJudge := false
	for _, r := range sr.Results {
		if r.JudgeScore > 0 || r.JudgeNote != "" {
			includeJudge = true
			break
		}
	}

	if includeJudge {
		fmt.Fprintln(w, "| # | Source | Score | Judge | Title | Excerpt |")
		fmt.Fprintln(w, "|---|--------|-------|-------|-------|---------|")
	} else {
		fmt.Fprintln(w, "| # | Source | Score | Title | Excerpt |")
		fmt.Fprintln(w, "|---|--------|-------|-------|---------|")
	}

	for _, r := range sr.Results {
		title := mdEscape(r.Title)
		excerpt := mdEscape(r.Excerpt)
		if includeJudge {
			judgeCell := fmt.Sprintf("%d — %s", r.JudgeScore, mdEscape(r.JudgeNote))
			fmt.Fprintf(w, "| %d | %s | %.3f | %s | %s | %s |\n",
				r.Rank, r.SourceType, r.Score, judgeCell, title, excerpt)
		} else {
			fmt.Fprintf(w, "| %d | %s | %.3f | %s | %s |\n",
				r.Rank, r.SourceType, r.Score, title, excerpt)
		}
	}
}

// mdEscape replaces characters that would break markdown table rendering.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func emitJSON(w io.Writer, reports []queryReport) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(reports)
}
