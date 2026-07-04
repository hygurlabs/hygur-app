package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDraftMeetingConfirmation_IsGated proves the draft-confirmation tool declares a side effect, so
// the registry holds it behind the confirmation gate — it sends an email and updates a calendar,
// neither of which may happen without the user's OK.
func TestDraftMeetingConfirmation_IsGated(t *testing.T) {
	tool := NewDraftMeetingConfirmationTool()
	if !tool.SideEffect() {
		t.Fatal("draft_meeting_confirmation must be a SideEffect tool (gated by pending_action)")
	}
}

// TestDraftMeetingConfirmation_Execute proves the draft carries the engine-supplied time verbatim in
// the calendar update AND renders it in the (voice) body — the times are engine-filled, not invented.
func TestDraftMeetingConfirmation_Execute(t *testing.T) {
	tool := NewDraftMeetingConfirmationTool()
	args := json.RawMessage(`{"subject":"Acme","current_time":"2026-07-10T15:00:00Z","stale_time":"2026-07-10T14:00:00Z"}`)
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var resp DraftMeetingConfirmationResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Requested {
		t.Error("draft must be marked requested (not yet sent/written)")
	}
	if resp.CalendarUpdateISO != "2026-07-10T15:00:00Z" {
		t.Errorf("calendar update must use the engine time verbatim, got %q", resp.CalendarUpdateISO)
	}
	if !strings.Contains(resp.EmailBody, "3:00 PM") {
		t.Errorf("draft body should render the current time, got %q", resp.EmailBody)
	}

	// Preview is glanceable and names the party.
	if p := tool.Preview(args); !strings.Contains(p, "Acme") {
		t.Errorf("preview should name the party, got %q", p)
	}
}
