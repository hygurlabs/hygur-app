// Package caldav implements a plugin.Connector that syncs an online calendar
// (a CalDAV per-calendar ICS export or any public iCal/webcal .ics URL) into
// the knowledge base as source_type="event" items.
package caldav

import (
	"strings"
	"time"
)

// Event is one parsed VEVENT.
type Event struct {
	UID         string
	Summary     string
	Description string
	Location    string
	Organizer   string
	Attendees   []string
	Start       time.Time
	End         time.Time
	AllDay      bool
}

// ParseICS parses an iCalendar document and returns its VEVENTs. It is
// deliberately lenient: unknown properties are ignored, and a malformed event
// is skipped rather than failing the whole feed.
func ParseICS(data string) []Event {
	lines := unfold(data)
	var events []Event
	var cur *Event
	for _, line := range lines {
		switch {
		case line == "BEGIN:VEVENT":
			cur = &Event{}
		case line == "END:VEVENT":
			if cur != nil {
				events = append(events, *cur)
				cur = nil
			}
		case cur != nil:
			name, params, value := splitProperty(line)
			switch name {
			case "UID":
				cur.UID = value
			case "SUMMARY":
				cur.Summary = unescapeText(value)
			case "DESCRIPTION":
				cur.Description = unescapeText(value)
			case "LOCATION":
				cur.Location = unescapeText(value)
			case "ORGANIZER":
				cur.Organizer = cleanMailto(value)
			case "ATTENDEE":
				if a := cleanMailto(value); a != "" {
					cur.Attendees = append(cur.Attendees, a)
				}
			case "DTSTART":
				cur.Start, cur.AllDay = parseICSTime(params, value)
			case "DTEND":
				cur.End, _ = parseICSTime(params, value)
			}
		}
	}
	return events
}

// unfold joins RFC 5545 continuation lines (a line beginning with a space or
// tab continues the previous one) and splits on CRLF/LF.
func unfold(data string) []string {
	raw := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	var out []string
	for _, l := range raw {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// splitProperty splits "NAME;PARAM=v;PARAM2=v:VALUE" into name, params map and
// value. The split point is the first ':' that is not inside a quoted string.
func splitProperty(line string) (name string, params map[string]string, value string) {
	colon := -1
	inQuote := false
	for i, r := range line {
		if r == '"' {
			inQuote = !inQuote
		} else if r == ':' && !inQuote {
			colon = i
			break
		}
	}
	if colon < 0 {
		return strings.ToUpper(line), nil, ""
	}
	head := line[:colon]
	value = line[colon+1:]
	parts := strings.Split(head, ";")
	name = strings.ToUpper(parts[0])
	if len(parts) > 1 {
		params = make(map[string]string, len(parts)-1)
		for _, p := range parts[1:] {
			if eq := strings.IndexByte(p, '='); eq >= 0 {
				params[strings.ToUpper(p[:eq])] = strings.Trim(p[eq+1:], `"`)
			}
		}
	}
	return name, params, value
}

// parseICSTime parses a DTSTART/DTEND value. Returns the time and whether it is
// an all-day (date-only) value. TZID parameters are not resolved (treated as
// local) — adequate for indexing/agenda use.
func parseICSTime(params map[string]string, value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if params["VALUE"] == "DATE" || len(value) == 8 {
		if t, err := time.Parse("20060102", value); err == nil {
			return t, true
		}
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, false
		}
	}
	return time.Time{}, false
}

func unescapeText(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(s)
}

func cleanMailto(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "mailto:")
}
