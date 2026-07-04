package rendezvous

import (
	"testing"
	"time"
)

// day is the fixed meeting day; the times differ, the assertion timestamps order supersession.
var day = time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)

func at(hour, min int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute)
}

// asserted builds an assertion timestamp (when a source stated a time).
func asserted(mon, d, h int) time.Time {
	return time.Date(2026, time.Month(mon), d, h, 0, 0, 0, time.UTC)
}

// Barrier fixture 1 — email(15:00, LATER assertion) + calendar(14:00, EARLIER) → current = 15:00,
// contradiction surfaced (calendar 14:00 stale), so the action can be offered.
func TestResolve_EmailSupersedesCalendar_Contradiction(t *testing.T) {
	nodes := []Node{
		{Subject: "acme", When: at(14, 0), Source: SourceCalendar, AssertedAt: asserted(7, 1, 9), ContentID: "cal-1", Title: "Calendar: Acme sync"},
		{Subject: "acme", When: at(15, 0), Source: SourceEmail, AssertedAt: asserted(7, 8, 10), ContentID: "eml-1", Title: "Re: Acme sync — moved to 3pm"},
	}
	r := Resolve(nodes)
	if r.Reason != "" {
		t.Fatalf("expected a confident result, declined: %s", r.Reason)
	}
	if !r.Current.Equal(at(15, 0)) {
		t.Fatalf("current = %s, want 15:00", r.Current)
	}
	if r.CurrentSource != SourceEmail {
		t.Fatalf("current source = %s, want email", r.CurrentSource)
	}
	if !r.Contradiction {
		t.Fatalf("expected a cross-source contradiction (calendar stale)")
	}
	if r.StaleSource != SourceCalendar || !r.StaleWhen.Equal(at(14, 0)) {
		t.Fatalf("stale = %s@%s, want calendar@14:00", r.StaleSource, r.StaleWhen)
	}
	// The current time cites the email that moved it — never the stale calendar.
	if len(r.Sources) != 1 || r.Sources[0].ContentID != "eml-1" {
		t.Fatalf("sources = %+v, want only eml-1", r.Sources)
	}
}

// Barrier fixture 2 — email time == calendar time → just the time, NO contradiction, no false alarm.
func TestResolve_Agreement_NoContradiction(t *testing.T) {
	nodes := []Node{
		{Subject: "acme", When: at(15, 0), Source: SourceCalendar, AssertedAt: asserted(7, 1, 9), ContentID: "cal-1"},
		{Subject: "acme", When: at(15, 0), Source: SourceEmail, AssertedAt: asserted(7, 8, 10), ContentID: "eml-1"},
	}
	r := Resolve(nodes)
	if r.Reason != "" {
		t.Fatalf("expected a confident result, declined: %s", r.Reason)
	}
	if !r.Current.Equal(at(15, 0)) {
		t.Fatalf("current = %s, want 15:00", r.Current)
	}
	if r.Contradiction {
		t.Fatalf("false alarm: agreement must NOT surface a contradiction (stale=%s@%s)", r.StaleSource, r.StaleWhen)
	}
}

// Barrier fixture 3a — no meeting found → decline (no invented time).
func TestResolve_NoMeeting_Declines(t *testing.T) {
	r := Resolve(nil)
	if r.Reason != ReasonNoMeeting {
		t.Fatalf("reason = %q, want %q", r.Reason, ReasonNoMeeting)
	}
	if !r.Current.IsZero() {
		t.Fatalf("declined result must carry NO time, got %s", r.Current)
	}
}

// Barrier fixture 3b — ambiguous: two DIFFERENT times asserted at the SAME instant cannot be
// ordered → decline (never guess which is current).
func TestResolve_UnorderableTimes_Declines(t *testing.T) {
	same := asserted(7, 8, 10)
	nodes := []Node{
		{Subject: "acme", When: at(14, 0), Source: SourceEmail, AssertedAt: same, ContentID: "eml-1"},
		{Subject: "acme", When: at(15, 0), Source: SourceEmail, AssertedAt: same, ContentID: "eml-2"},
	}
	r := Resolve(nodes)
	if r.Reason != ReasonAmbiguous {
		t.Fatalf("reason = %q, want %q", r.Reason, ReasonAmbiguous)
	}
	if !r.Current.IsZero() {
		t.Fatalf("declined result must carry NO time, got %s", r.Current)
	}
}

// A single source (only the calendar, say) is a confident answer with no contradiction — nothing to
// conflict with.
func TestResolve_SingleSource_NoContradiction(t *testing.T) {
	nodes := []Node{
		{Subject: "acme", When: at(14, 0), Source: SourceCalendar, AssertedAt: asserted(7, 1, 9), ContentID: "cal-1"},
	}
	r := Resolve(nodes)
	if r.Reason != "" || r.Contradiction || !r.Current.Equal(at(14, 0)) {
		t.Fatalf("single-source: got current=%s contradiction=%v reason=%q", r.Current, r.Contradiction, r.Reason)
	}
}
