package caldav

import "testing"

func TestNormalizeCalURL(t *testing.T) {
	cases := map[string]string{
		"webcal://p1.icloud.com/x/cal.ics":  "https://p1.icloud.com/x/cal.ics",
		"WEBCAL://host/cal.ics":             "https://host/cal.ics",
		"caldav.icloud.com":                 "https://caldav.icloud.com",
		"https://calendar.google.com/x.ics": "https://calendar.google.com/x.ics",
		"  https://h/c.ics  ":               "https://h/c.ics",
		"nextcloud.example/dav/?export":     "https://nextcloud.example/dav/?export",
	}
	for in, want := range cases {
		if got := normalizeCalURL(in); got != want {
			t.Errorf("normalizeCalURL(%q) = %q, want %q", in, got, want)
		}
	}
}
