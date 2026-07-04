// Package rendezvous resolves the CURRENT time of a meeting (rendez-vous) across two sources — the
// email thread and the calendar — and surfaces a CROSS-SOURCE CONTRADICTION when they disagree.
//
// It is the meeting-time analogue of the C7 figure engine: a meeting time is a DETERMINED TEMPORAL
// FACT — a node {entity (whom the meeting is with), datetime, source, assertion-timestamp}. The
// resolution REUSES C7's supersession mechanism verbatim (figure.ResolveTemporal, "latest assertion
// wins"): the meeting time asserted MOST RECENTLY is the current one; an older, superseded time
// becomes the surfaced contradiction. On top of that supersession, this package adds the ONE thing a
// meeting needs beyond a figure: it names WHICH source is stale (typically the calendar, when the
// latest email moved the time and the calendar event was never updated), so the voice can say
// "your calendar still shows 2:00 PM — that's a contradiction" and offer to fix it.
package rendezvous

import (
	"sort"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/figure"
	"github.com/hygur/sidecar/internal/store"
)

// Source is where a meeting-time assertion came from. The two channels a rendez-vous lives on.
const (
	SourceEmail    = "email"
	SourceCalendar = "calendar"
)

// Reason codes explain WHY a resolution declined, so the voice asks the right question instead of
// inventing a time. Empty on a confident result.
const (
	ReasonNoMeeting = "no_meeting" // no meeting-time node for the subject
	ReasonAmbiguous = "ambiguous"  // times cannot be ordered (no assertion dates / a tie at the latest)
)

// meetingLabel is the fixed figure-node label under which meeting times are stored/resolved — the
// analogue of "vat"/"dose". One label: a meeting time is a meeting time.
const meetingLabel = "meeting"

// Node is one meeting-time assertion: WHO the meeting is with, WHEN it is, WHICH source asserted it,
// and WHEN that assertion was made. It reuses the figure-node/context-edge model — the value is the
// datetime, the entity edge is the meeting subject, the source edge is the document, and the
// assertion-timestamp is the document date C7 orders supersession by.
type Node struct {
	Subject    string    // entity edge — folded person/org norm the meeting is with
	When       time.Time // the value node — the meeting datetime
	Source     string    // source channel — SourceEmail | SourceCalendar
	AssertedAt time.Time // assertion timestamp — when this source stated this time (orders supersession)
	ContentID  string    // source document (email message / calendar event)
	Title      string    // source display title, for citation
}

// Result is the outcome of a meeting-time resolution: the CURRENT time (latest assertion), the
// cross-source contradiction if the other source is stale, the citing source(s), or an honest
// decline. The times are ENGINE-DETERMINED — never voiced by the LLM.
type Result struct {
	Subject       string    // display subject (whom the meeting is with)
	Current       time.Time // the resolved current meeting time
	CurrentSource string    // which source asserts the current time (email | calendar)
	AssertedAt    time.Time // when the current time was asserted (for "per {source, date}")
	Contradiction bool      // true when another source holds a DIFFERENT (stale) time
	StaleWhen     time.Time // the stale time the other source still shows
	StaleSource   string    // the source that is stale (typically calendar)
	Reason        string    // decline reason (empty on success)
	Sources       []fact.Source
}

// Resolve determines the current meeting time from a set of assertions (from possibly several
// sources) that all concern the SAME meeting subject. It REUSES figure.ResolveTemporal for the
// supersession core — the latest-asserted time wins — then, if a DIFFERENT source still holds an
// older time, it surfaces that as a cross-source contradiction (the stale calendar). Declines
// honestly on no meeting or an unorderable set — never invents a time.
func Resolve(nodes []Node) Result {
	if len(nodes) == 0 {
		return Result{Reason: ReasonNoMeeting}
	}

	// Map each assertion to a figure NODE so C7's mechanism resolves it unchanged: the datetime is the
	// value (canonical RFC3339 key), the assertion timestamp is the document date supersession orders
	// by, the source document is the content id.
	fnodes := make([]store.FigureNode, 0, len(nodes))
	byKey := map[string][]Node{} // canonical time key -> the assertions holding it (for source lookup)
	for _, n := range nodes {
		key := canonicalKey(n.When)
		fnodes = append(fnodes, store.FigureNode{
			ContentID:  n.ContentID,
			EntityNorm: n.Subject,
			Label:      meetingLabel,
			Value:      key,
			Raw:        displayTime(n.When),
			DocDate:    n.AssertedAt,
		})
		byKey[key] = append(byKey[key], n)
	}

	pick, _, reason := figure.ResolveTemporal(fnodes)
	if reason != "" {
		return Result{Reason: ReasonAmbiguous}
	}

	currentKey := pick.Value
	current := byKey[currentKey][0] // any assertion of the current time (they share When/Subject)

	res := Result{
		Subject:       current.Subject,
		Current:       current.When,
		CurrentSource: current.Source,
		AssertedAt:    current.AssertedAt,
	}

	// Cross-source contradiction: does ANOTHER source still hold a DIFFERENT time? The stale one is
	// the assertion whose time differs from the current one — in the demo, the calendar that the
	// latest email superseded. Pick the most recent stale assertion for the citation/fix.
	var stale *Node
	for i := range nodes {
		if canonicalKey(nodes[i].When) == currentKey {
			continue
		}
		if stale == nil || nodes[i].AssertedAt.After(stale.AssertedAt) {
			stale = &nodes[i]
		}
	}
	if stale != nil {
		res.Contradiction = true
		res.StaleWhen = stale.When
		res.StaleSource = stale.Source
	}

	// Sources: the document(s) asserting the CURRENT time (deduped, stable order).
	seen := map[string]bool{}
	for _, n := range byKey[currentKey] {
		if n.ContentID == "" || seen[n.ContentID] {
			continue
		}
		seen[n.ContentID] = true
		res.Sources = append(res.Sources, fact.Source{ContentID: n.ContentID, Title: n.Title})
	}
	sort.Slice(res.Sources, func(i, j int) bool { return res.Sources[i].ContentID < res.Sources[j].ContentID })
	return res
}

// canonicalKey is the supersession discriminant for a meeting time — minute precision in UTC, so two
// assertions of the same instant collapse and two different times separate.
func canonicalKey(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04")
}

// displayTime renders a meeting time for a grand-public English answer: "Fri Jul 10, 3:00 PM".
func displayTime(t time.Time) string {
	return t.Format("Mon Jan 2, 3:04 PM")
}

// SourceLabel renders a source channel for the voice ("email" / "calendar").
func SourceLabel(source string) string {
	switch source {
	case SourceEmail:
		return "email"
	case SourceCalendar:
		return "calendar"
	}
	return strings.TrimSpace(source)
}

// DisplayTime is the exported time formatter, so the tool/answer layer renders times identically to
// the resolver (one place decides how a meeting time reads).
func DisplayTime(t time.Time) string { return displayTime(t) }
