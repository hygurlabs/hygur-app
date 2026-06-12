package contradict

import (
	"sort"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// Recurrence is a detected periodic pattern — a subject cluster whose items recur
// at a ~regular interval — with a predicted next occurrence. The seed of the
// conséquence (prospection) faculty: deterministic, from item dates only (no LLM,
// no clock). The caller filters NextAt to an "upcoming" window and phrases it.
type Recurrence struct {
	Subject    string   `json:"subject"`     // normalized subject cluster
	Title      string   `json:"title"`       // a representative human title
	Count      int      `json:"count"`       // distinct-day occurrences observed
	PeriodDays int      `json:"period_days"` // median interval, days
	LastAt     string   `json:"last_at"`     // RFC3339 of the latest occurrence
	NextAt     string   `json:"next_at"`     // predicted next = last + period
	SourceIDs  []string `json:"source_ids"`
}

// DetectRecurrence groups items by normalized subject and, for clusters with
// >= minCount dated occurrences whose gaps are ~regular (every consecutive gap
// within ±50% of the median gap), predicts the next occurrence. Pure +
// deterministic — reuses the same subject clustering as DetectClaimConflicts.
func DetectRecurrence(items []*store.KnowledgeItem, minCount int) []Recurrence {
	if minCount < 2 {
		minCount = 2
	}
	type occ struct {
		at        time.Time
		contentID string
		title     string
	}
	clusters := map[string][]occ{}
	for _, it := range items {
		if it == nil {
			continue
		}
		key := normalizeSubject(it.Title)
		if key == "" {
			continue
		}
		t := store.GetCanonicalDate(it)
		if t.IsZero() {
			continue
		}
		clusters[key] = append(clusters[key], occ{at: t.UTC(), contentID: it.ContentID, title: it.Title})
	}

	keys := make([]string, 0, len(clusters))
	for k := range clusters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Recurrence
	for _, k := range keys {
		occs := clusters[k]
		sort.Slice(occs, func(i, j int) bool { return occs[i].at.Before(occs[j].at) })
		// Collapse same-day occurrences so several mails in one day don't fake a 0-gap.
		var dates []time.Time
		var ids []string
		lastDay := ""
		for _, o := range occs {
			d := o.at.Format("2006-01-02")
			if d == lastDay {
				continue
			}
			lastDay = d
			dates = append(dates, o.at)
			ids = append(ids, o.contentID)
		}
		if len(dates) < minCount {
			continue
		}
		gaps := make([]float64, 0, len(dates)-1)
		for i := 1; i < len(dates); i++ {
			gaps = append(gaps, dates[i].Sub(dates[i-1]).Hours()/24)
		}
		med := medianFloat(gaps)
		if med <= 0 {
			continue
		}
		regular := true
		for _, g := range gaps {
			if g < med*0.5 || g > med*1.5 {
				regular = false
				break
			}
		}
		if !regular {
			continue
		}
		period := int(med + 0.5)
		last := dates[len(dates)-1]
		out = append(out, Recurrence{
			Subject:    k,
			Title:      occs[len(occs)-1].title,
			Count:      len(dates),
			PeriodDays: period,
			LastAt:     last.Format(time.RFC3339),
			NextAt:     last.AddDate(0, 0, period).Format(time.RFC3339),
			SourceIDs:  ids,
		})
	}
	return out
}

func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
