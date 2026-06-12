package edge

import (
	"context"
	"testing"
)

func TestSpamMailboxClassifiers(t *testing.T) {
	cases := []struct {
		name        string
		spam, catch bool
	}{
		{"Spam", true, false},
		{"Junk", true, false},
		{"Labels/Spam", true, false},
		{"INBOX", false, false},
		{"Sent", false, false},
		{"All Mail", false, true},
		{"all mail", false, true},
		{"", false, true},
	}
	for _, c := range cases {
		if got := isSpamMailbox(c.name); got != c.spam {
			t.Errorf("isSpamMailbox(%q) = %v, want %v", c.name, got, c.spam)
		}
		if got := isCatchAllMailbox(c.name); got != c.catch {
			t.Errorf("isCatchAllMailbox(%q) = %v, want %v", c.name, got, c.catch)
		}
	}
}

// Fail-open: with no catch-all mailbox selected, the guard never touches the
// connector (nil-safe) and returns an empty set, so nothing is ever skipped.
func TestSpamThreadIDsNoCatchAll(t *testing.T) {
	ms := &MailSync{}
	got := ms.spamThreadIDs(context.Background(), nil, []string{"INBOX", "Sent"})
	if len(got) != 0 {
		t.Errorf("expected empty set with no catch-all, got %d", len(got))
	}
}
