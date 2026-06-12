package caldav

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestUseCalDAV(t *testing.T) {
	cases := []struct {
		url, user string
		want      bool
	}{
		{"https://calendar.google.com/calendar/ical/x/private/basic.ics", "", false}, // direct .ics
		{"webcal://p1.icloud.com/x/cal.ics", "user", false},                          // webcal file
		{"https://caldav.icloud.com", "appleid@me.com", true},                         // server + creds
		{"https://caldav.icloud.com", "", false},                                      // server, no creds → GET
		{"https://nextcloud.example/remote.php/dav", "bob", true},                      // self-hosted CalDAV
	}
	for _, c := range cases {
		if got := useCalDAV(c.url, c.user); got != c.want {
			t.Errorf("useCalDAV(%q, user=%q) = %v, want %v", c.url, c.user, got, c.want)
		}
	}
}

// The PROPFIND/REPORT structs must parse a real-shaped Multi-Status, incl. the
// DAV: + CalDAV namespaces — this is what we can't test against live iCloud.
func TestMultistatusParsing(t *testing.T) {
	principalXML := `<?xml version="1.0" encoding="utf-8"?>
<multistatus xmlns="DAV:">
 <response>
  <href>/1234/principal/</href>
  <propstat>
   <prop><current-user-principal><href>/1234/principal/</href></current-user-principal></prop>
   <status>HTTP/1.1 200 OK</status>
  </propstat>
 </response>
</multistatus>`
	var ms multistatus
	if err := xml.Unmarshal([]byte(principalXML), &ms); err != nil {
		t.Fatalf("principal unmarshal: %v", err)
	}
	if len(ms.Responses) != 1 || ms.Responses[0].Propstat[0].Prop.CurrentUserPrincipal.Href != "/1234/principal/" {
		t.Fatalf("current-user-principal not parsed: %+v", ms)
	}

	homeXML := `<multistatus xmlns="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
 <response><href>/1234/principal/</href><propstat><prop>
  <c:calendar-home-set><href>/1234/calendars/</href></c:calendar-home-set>
 </prop></propstat></response>
</multistatus>`
	ms = multistatus{}
	if err := xml.Unmarshal([]byte(homeXML), &ms); err != nil {
		t.Fatalf("home unmarshal: %v", err)
	}
	if ms.Responses[0].Propstat[0].Prop.CalendarHomeSet.Href != "/1234/calendars/" {
		t.Fatalf("calendar-home-set not parsed: %+v", ms.Responses[0].Propstat[0].Prop)
	}

	listXML := `<multistatus xmlns="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
 <response><href>/1234/calendars/work/</href><propstat><prop>
   <resourcetype><collection/><c:calendar/></resourcetype>
   <displayname>Work</displayname>
 </prop></propstat></response>
 <response><href>/1234/calendars/inbox/</href><propstat><prop>
   <resourcetype><collection/></resourcetype>
 </prop></propstat></response>
</multistatus>`
	ms = multistatus{}
	if err := xml.Unmarshal([]byte(listXML), &ms); err != nil {
		t.Fatalf("list unmarshal: %v", err)
	}
	var calendars int
	for _, r := range ms.Responses {
		if r.Propstat[0].Prop.ResourceType.Calendar != nil {
			calendars++
		}
	}
	if calendars != 1 {
		t.Fatalf("expected 1 calendar collection, got %d", calendars)
	}

	reportXML := `<multistatus xmlns="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
 <response><href>/1234/calendars/work/e1.ics</href><propstat><prop>
  <c:calendar-data>BEGIN:VCALENDAR
BEGIN:VEVENT
UID:e1
SUMMARY:Demo
END:VEVENT
END:VCALENDAR</c:calendar-data>
 </prop></propstat></response>
</multistatus>`
	ms = multistatus{}
	if err := xml.Unmarshal([]byte(reportXML), &ms); err != nil {
		t.Fatalf("report unmarshal: %v", err)
	}
	if !strings.Contains(ms.Responses[0].Propstat[0].Prop.CalendarData, "SUMMARY:Demo") {
		t.Fatalf("calendar-data not parsed: %q", ms.Responses[0].Propstat[0].Prop.CalendarData)
	}
}
