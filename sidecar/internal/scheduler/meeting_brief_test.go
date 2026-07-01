package scheduler

import (
	"testing"
	"time"
)

// meetingContentID must be idempotent for the mail path: its When is set to now on
// every scheduler tick, so folding it into the id would mint a fresh id each run and
// pile up a duplicate brief every cycle. Calendar keeps per-occurrence identity.
func TestMeetingContentIDStability(t *testing.T) {
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

	mailA := MeetingInput{Kind: "mail", Key: "email:thread-1", Title: "Deadline X", When: base}
	mailB := MeetingInput{Kind: "mail", Key: "email:thread-1", Title: "Deadline X", When: base.Add(3 * time.Hour)}
	if meetingContentID(mailA) != meetingContentID(mailB) {
		t.Errorf("mail brief id must not depend on wall-clock When: %s vs %s",
			meetingContentID(mailA), meetingContentID(mailB))
	}

	other := MeetingInput{Kind: "mail", Key: "email:thread-2", Title: "Deadline X", When: base}
	if meetingContentID(mailA) == meetingContentID(other) {
		t.Error("different mails must yield different brief ids")
	}

	// Calendar keeps its per-occurrence start time in the id.
	calA := MeetingInput{Kind: "calendar", Key: "ev-1", When: base}
	calB := MeetingInput{Kind: "calendar", Key: "ev-1", When: base.Add(24 * time.Hour)}
	if meetingContentID(calA) == meetingContentID(calB) {
		t.Error("calendar brief id should distinguish occurrences by start time")
	}
}
