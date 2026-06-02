package caldav

import "testing"

const sample = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:abc-123\r\n" +
	"SUMMARY:Réunion budget\\, Q3\r\n" +
	"DESCRIPTION:Première ligne\\nSeconde ligne\r\n" +
	"LOCATION:Salle B\r\n" +
	"ORGANIZER;CN=Alice:mailto:alice@example.com\r\n" +
	"ATTENDEE:mailto:bob@example.com\r\n" +
	"DTSTART:20260615T090000Z\r\n" +
	"DTEND:20260615T100000Z\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:all-day-1\r\n" +
	"SUMMARY:Congés\r\n" +
	"DTSTART;VALUE=DATE:20260701\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	evs := ParseICS(sample)
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}

	e := evs[0]
	if e.UID != "abc-123" {
		t.Errorf("UID = %q", e.UID)
	}
	if e.Summary != "Réunion budget, Q3" {
		t.Errorf("Summary = %q (escaped comma not unescaped)", e.Summary)
	}
	if e.Description != "Première ligne\nSeconde ligne" {
		t.Errorf("Description = %q (escaped newline not unescaped)", e.Description)
	}
	if e.Location != "Salle B" {
		t.Errorf("Location = %q", e.Location)
	}
	if e.Organizer != "alice@example.com" {
		t.Errorf("Organizer = %q (mailto not stripped)", e.Organizer)
	}
	if len(e.Attendees) != 1 || e.Attendees[0] != "bob@example.com" {
		t.Errorf("Attendees = %v", e.Attendees)
	}
	if e.AllDay {
		t.Errorf("timed event flagged all-day")
	}
	if e.Start.IsZero() || e.End.IsZero() {
		t.Errorf("start/end not parsed: %v / %v", e.Start, e.End)
	}
	if got := e.Start.Format("2006-01-02T15:04:05Z"); got != "2026-06-15T09:00:00Z" {
		t.Errorf("Start = %q", got)
	}

	d := evs[1]
	if !d.AllDay {
		t.Errorf("date-only event not flagged all-day")
	}
	if got := d.Start.Format("2006-01-02"); got != "2026-07-01" {
		t.Errorf("all-day Start = %q", got)
	}
}

func TestUnfold(t *testing.T) {
	// RFC 5545 line folding: a CRLF immediately followed by a single
	// whitespace continues the previous line; unfolding removes BOTH the CRLF
	// and that one whitespace char (so a mid-word split reassembles seamlessly).
	in := "DESCRIPTION:this is a very lo\r\n ng folded line"
	lines := unfold(in)
	if len(lines) != 1 {
		t.Fatalf("expected 1 unfolded line, got %d: %v", len(lines), lines)
	}
	if lines[0] != "DESCRIPTION:this is a very long folded line" {
		t.Errorf("unfold = %q", lines[0])
	}
}

func TestSplitPropertyQuotedColon(t *testing.T) {
	// A ':' inside a quoted param value must not split the property.
	name, params, value := splitProperty(`ORGANIZER;CN="Doe: Jane":mailto:jane@example.com`)
	if name != "ORGANIZER" {
		t.Errorf("name = %q", name)
	}
	if params["CN"] != "Doe: Jane" {
		t.Errorf("CN param = %q", params["CN"])
	}
	if value != "mailto:jane@example.com" {
		t.Errorf("value = %q", value)
	}
}

func TestEventContentIDStable(t *testing.T) {
	ev := Event{UID: "x", Summary: "S"}
	a := eventContentID("https://cal/feed.ics", ev)
	b := eventContentID("https://cal/feed.ics", ev)
	if a != b {
		t.Errorf("content id not stable: %q != %q", a, b)
	}
	if eventContentID("https://other/feed.ics", ev) == a {
		t.Errorf("content id should differ per feed url")
	}
}

func TestNormalizeURL(t *testing.T) {
	if got := normalizeURL("webcal://example.com/c.ics"); got != "https://example.com/c.ics" {
		t.Errorf("normalizeURL = %q", got)
	}
	if got := normalizeURL("https://example.com/c.ics"); got != "https://example.com/c.ics" {
		t.Errorf("normalizeURL changed https url: %q", got)
	}
}
