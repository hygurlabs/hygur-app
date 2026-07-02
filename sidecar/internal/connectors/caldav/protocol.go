package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hygur/sidecar/internal/netguard"
)

// This file implements just enough of the CalDAV protocol (RFC 4791) to read a
// real CalDAV server — principal/calendar-home discovery via PROPFIND, then a
// calendar-query REPORT for VEVENTs in a time window. Used for authenticated
// servers (iCloud, Nextcloud, Radicale) where a plain GET returns 403. The
// returned calendar-data is plain ICS, so it feeds straight into ParseICS.
//
// We deliberately keep this dependency-free (encoding/xml + net/http) rather
// than pulling a WebDAV library: the surface we need is small and stable.

// caldavWindowPast/Future bound the calendar-query so a multi-year calendar
// doesn't return everything. Recent + ~2y ahead covers the brief + calendar view.
const (
	caldavWindowPast   = 120 * 24 * time.Hour
	caldavWindowFuture = 730 * 24 * time.Hour
)

// multistatus mirrors the DAV: 207 Multi-Status envelope (only the bits we read).
type multistatus struct {
	XMLName   xml.Name      `xml:"DAV: multistatus"`
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Href     string        `xml:"DAV: href"`
	Propstat []davPropstat `xml:"DAV: propstat"`
}

type davPropstat struct {
	Status string  `xml:"DAV: status"`
	Prop   davProp `xml:"DAV: prop"`
}

type davProp struct {
	CurrentUserPrincipal hrefValue    `xml:"DAV: current-user-principal"`
	CalendarHomeSet      hrefValue    `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	DisplayName          string       `xml:"DAV: displayname"`
	ResourceType         resourceType `xml:"DAV: resourcetype"`
	CalendarData         string       `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
}

type hrefValue struct {
	Href string `xml:"DAV: href"`
}

type resourceType struct {
	// Present (non-nil) when the collection is a CalDAV calendar.
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

// syncCalDAV discovers the account's calendars and returns the union of their
// VEVENTs (as ICS text) within the configured window.
func (c *Connector) syncCalDAV(ctx context.Context, endpoint, username, password string) (string, error) {
	endpoint = normalizeCalURL(endpoint)

	principal, err := c.propfindHref(ctx, endpoint, username, password,
		`<d:prop><d:current-user-principal/></d:prop>`,
		func(p davProp) string { return p.CurrentUserPrincipal.Href })
	if err != nil {
		return "", fmt.Errorf("discover principal: %w", err)
	}
	principalURL := resolveRef(endpoint, principal)

	home, err := c.propfindHref(ctx, principalURL, username, password,
		`<d:prop><c:calendar-home-set/></d:prop>`,
		func(p davProp) string { return p.CalendarHomeSet.Href })
	if err != nil {
		return "", fmt.Errorf("discover calendar-home: %w", err)
	}
	homeURL := resolveRef(principalURL, home)

	calendars, err := c.listCalendars(ctx, homeURL, username, password)
	if err != nil {
		return "", fmt.Errorf("list calendars: %w", err)
	}
	if len(calendars) == 0 {
		return "", fmt.Errorf("no calendars found on the server")
	}

	now := time.Now().UTC()
	start := now.Add(-caldavWindowPast)
	end := now.Add(caldavWindowFuture)

	var b strings.Builder
	for _, calURL := range calendars {
		ics, rerr := c.reportEvents(ctx, calURL, username, password, start, end)
		if rerr != nil {
			c.log.Debug().Err(rerr).Str("calendar", calURL).Msg("caldav REPORT failed")
			continue // skip a bad calendar, keep the others
		}
		b.WriteString(ics)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// propfindHref runs a Depth:0 PROPFIND and extracts a single href from the first
// response's prop via pick.
func (c *Connector) propfindHref(ctx context.Context, target, username, password, propBody string, pick func(davProp) string) (string, error) {
	ms, err := c.davRequest(ctx, "PROPFIND", target, "0", username, password, propfindEnvelope(propBody))
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if h := pick(ps.Prop); h != "" {
				return h, nil
			}
		}
	}
	return "", fmt.Errorf("property not found in response")
}

// listCalendars PROPFINDs the calendar-home (Depth:1) and returns the URLs of
// child collections that are calendars.
func (c *Connector) listCalendars(ctx context.Context, home, username, password string) ([]string, error) {
	ms, err := c.davRequest(ctx, "PROPFIND", home, "1", username, password,
		propfindEnvelope(`<d:prop><d:resourcetype/><d:displayname/></d:prop>`))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if ps.Prop.ResourceType.Calendar != nil && r.Href != "" {
				out = append(out, resolveRef(home, r.Href))
			}
		}
	}
	return out, nil
}

// reportEvents runs a calendar-query REPORT (Depth:1) for VEVENTs in [start,end]
// and concatenates the returned calendar-data (ICS) blocks.
func (c *Connector) reportEvents(ctx context.Context, calURL, username, password string, start, end time.Time) (string, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><c:calendar-data/></d:prop>` +
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">` +
		fmt.Sprintf(`<c:time-range start="%s" end="%s"/>`,
			start.Format("20060102T150405Z"), end.Format("20060102T150405Z")) +
		`</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
	ms, err := c.davRequest(ctx, "REPORT", calURL, "1", username, password, body)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			if d := strings.TrimSpace(ps.Prop.CalendarData); d != "" {
				b.WriteString(d)
				b.WriteByte('\n')
			}
		}
	}
	return b.String(), nil
}

// davRequest issues a WebDAV method (PROPFIND/REPORT) with Basic auth and parses
// the 207 Multi-Status XML body.
func (c *Connector) davRequest(ctx context.Context, method, target, depth, username, password, body string) (*multistatus, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", depth)
	req.Header.Set("User-Agent", "Hygur/1.0")
	if strings.TrimSpace(username) != "" {
		req.SetBasicAuth(strings.TrimSpace(username), password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("HTTP %d (check the username + app-specific password)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusMultiStatus && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var ms multistatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("invalid multistatus XML: %w", err)
	}
	return &ms, nil
}

func propfindEnvelope(prop string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` + prop + `</d:propfind>`
}

// resolveRef resolves a possibly-relative href against a base URL, so a server
// that returns absolute hrefs on a partition host (iCloud) is handled too.
func resolveRef(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// fetchCalendar returns the calendar's ICS text, choosing the access method from
// the URL + credentials: a direct .ics / webcal file is GETed; a server address
// with a username is read via the CalDAV protocol (PROPFIND discovery + REPORT),
// with a direct-GET fallback if the protocol path fails.
func (c *Connector) fetchCalendar(ctx context.Context, rawURL, username, password string) (string, error) {
	// Enforce the scheme allowlist (only http/https after webcal→https
	// normalization) and reject a non-public IP-literal target up front. The
	// dial-time guard (c.http = netguard.Client) is authoritative for hostnames
	// that resolve internal + every redirect/CalDAV-discovered hop; this rejects
	// obviously-bad URLs (file://, gopher://, http://169.254.169.254) before any
	// request leaves the host.
	if _, err := netguard.ValidateURL(normalizeCalURL(rawURL), c.allowPrivate, "http", "https"); err != nil {
		return "", err
	}
	if useCalDAV(rawURL, username) {
		if ics, err := c.syncCalDAV(ctx, rawURL, username, password); err == nil {
			return ics, nil
		} else {
			c.log.Debug().Err(err).Msg("caldav protocol failed; falling back to direct GET")
		}
	}
	return c.fetch(ctx, rawURL, username, password)
}

// useCalDAV decides whether to speak the CalDAV protocol: a direct calendar file
// (.ics / webcal) is always a plain GET; otherwise a server address with
// credentials is treated as a CalDAV server (iCloud, Nextcloud, Radicale).
func useCalDAV(rawURL, username string) bool {
	lo := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasSuffix(lo, ".ics") || strings.HasPrefix(lo, "webcal://") {
		return false
	}
	return strings.TrimSpace(username) != ""
}
