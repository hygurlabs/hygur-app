package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/store"
)

// stubMeetingStore returns canned meeting nodes for "acme", ignoring norm resolution.
type stubMeetingStore struct{ nodes []store.MeetingNode }

func (s stubMeetingStore) ResolvePersonNorms(ctx context.Context, q string, n int) ([]string, error) {
	return []string{"acme"}, nil
}
func (s stubMeetingStore) MeetingNodesForEntities(ctx context.Context, norms []string) ([]store.MeetingNode, error) {
	return s.nodes, nil
}

func mday(h, m int) time.Time { return time.Date(2026, 7, 10, h, m, 0, 0, time.UTC) }
func mass(d, h int) time.Time { return time.Date(2026, 7, d, h, 0, 0, 0, time.UTC) }

func runMeeting(t *testing.T, nodes []store.MeetingNode) MeetingResponse {
	t.Helper()
	tool := NewLookupMeetingTool(stubMeetingStore{nodes: nodes})
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"entity":"Acme"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var out MeetingResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// Barrier fixture 1 through the tool — email(15:00, later) supersedes calendar(14:00): current time
// stated, contradiction surfaced (calendar stale), action offered.
func TestLookupMeeting_Contradiction(t *testing.T) {
	out := runMeeting(t, []store.MeetingNode{
		{ContentID: "cal-1", EntityNorm: "acme", When: mday(14, 0), Source: "calendar", AssertedAt: mass(1, 9), Title: "Acme sync"},
		{ContentID: "eml-1", EntityNorm: "acme", When: mday(15, 0), Source: "email", AssertedAt: mass(8, 10), Title: "Re: moved to 3pm"},
	})
	if out.Tier != "high" || out.Value == "" {
		t.Fatalf("expected a high-confidence current time, got %+v", out)
	}
	if !out.Contradiction || out.StaleSource != "calendar" {
		t.Fatalf("expected a calendar-stale contradiction, got %+v", out)
	}
	if out.Note == "" || out.Offer == "" {
		t.Fatalf("contradiction must carry a note AND an action offer, got %+v", out)
	}
	// The current ISO must be the 15:00 email time, never the stale 14:00.
	if got := out.CurrentISO; got == "" || got == out.StaleISO {
		t.Fatalf("current ISO must be the email time, distinct from stale: %+v", out)
	}
}

// Barrier fixture 2 — agreement → the time, no contradiction, no offer (no false alarm).
func TestLookupMeeting_Agreement(t *testing.T) {
	out := runMeeting(t, []store.MeetingNode{
		{ContentID: "cal-1", EntityNorm: "acme", When: mday(15, 0), Source: "calendar", AssertedAt: mass(1, 9)},
		{ContentID: "eml-1", EntityNorm: "acme", When: mday(15, 0), Source: "email", AssertedAt: mass(8, 10)},
	})
	if out.Tier != "high" || out.Value == "" {
		t.Fatalf("expected a confident time, got %+v", out)
	}
	if out.Contradiction || out.Note != "" || out.Offer != "" {
		t.Fatalf("agreement must NOT raise a contradiction/offer, got %+v", out)
	}
}

// Barrier fixture 3a — no meeting → decline, no invented time.
func TestLookupMeeting_NoMeeting(t *testing.T) {
	out := runMeeting(t, nil)
	if out.Tier != "none" || out.Value != "" {
		t.Fatalf("no meeting must decline with no time, got %+v", out)
	}
}

// Barrier fixture 3b — the name resolves to several distinct meetings → decline (never mix).
func TestLookupMeeting_AmbiguousSubjects(t *testing.T) {
	out := runMeeting(t, []store.MeetingNode{
		{ContentID: "eml-1", EntityNorm: "acme paris", When: mday(15, 0), Source: "email", AssertedAt: mass(8, 10)},
		{ContentID: "eml-2", EntityNorm: "acme london", When: mday(16, 0), Source: "email", AssertedAt: mass(8, 11)},
	})
	if out.Tier != "none" || out.Value != "" {
		t.Fatalf("ambiguous subjects must decline with no time, got %+v", out)
	}
}
