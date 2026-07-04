package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DraftMeetingConfirmationTool drafts a confirmation email AND a calendar update to reconcile a
// stale meeting time (the gated action of the contradiction-aware rendez-vous). It is a SIDE-EFFECT
// tool: sending the email and modifying the calendar mutate the user's world, so the registry gates
// it behind the WP3 confirmation card — nothing is sent or changed until the user confirms. The
// DRAFT text is voice; the TIMES in it are engine-filled (passed verbatim from the deterministic
// resolution), so the confirmation cannot state a wrong time.
type DraftMeetingConfirmationTool struct{}

// NewDraftMeetingConfirmationTool returns the tool. It takes no dependencies: the actual send +
// calendar write happen on the client after confirmation; the tool composes the payload.
func NewDraftMeetingConfirmationTool() *DraftMeetingConfirmationTool {
	return &DraftMeetingConfirmationTool{}
}

func (t *DraftMeetingConfirmationTool) Name() string { return "draft_meeting_confirmation" }

func (t *DraftMeetingConfirmationTool) Description() string {
	return "Draft a confirmation email to a meeting party AND prepare a calendar update to the current time, to fix a stale calendar entry. Use ONLY after the current meeting time and the stale time have been determined. The email is NOT sent and the calendar is NOT changed automatically — the user must confirm first. Times must be passed exactly as determined (ISO 8601)."
}

// SideEffect gates this tool behind the confirmation card: it sends an email and updates a calendar.
func (t *DraftMeetingConfirmationTool) SideEffect() bool { return true }

func (t *DraftMeetingConfirmationTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "Whom the meeting is with (person or organization).",
			},
			"current_time": map[string]any{
				"type":        "string",
				"description": "The CURRENT (correct) meeting time as an ISO 8601 timestamp — pass exactly as determined by lookup_meeting (current_iso).",
			},
			"stale_time": map[string]any{
				"type":        "string",
				"description": "The stale time the calendar still shows, as an ISO 8601 timestamp (stale_iso). Optional.",
			},
		},
		"required": []string{"subject", "current_time"},
	}
}

// DraftMeetingConfirmationRequest is the LLM-supplied payload.
type DraftMeetingConfirmationRequest struct {
	Subject     string `json:"subject"`
	CurrentTime string `json:"current_time"`
	StaleTime   string `json:"stale_time,omitempty"`
}

// DraftMeetingConfirmationResponse is the composed, still-un-sent draft the client renders after the
// user confirms. Requested=true mirrors create_calendar_event: no send/write has happened yet.
type DraftMeetingConfirmationResponse struct {
	Requested         bool   `json:"requested"`
	Subject           string `json:"subject"`
	EmailSubject      string `json:"email_subject"`
	EmailBody         string `json:"email_body"`
	CalendarUpdateISO string `json:"calendar_update_iso"`
}

// Preview is the one-liner for the confirmation card.
func (t *DraftMeetingConfirmationTool) Preview(args json.RawMessage) string {
	var req DraftMeetingConfirmationRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return "Draft a meeting confirmation and update the calendar"
	}
	when := displayMeetingISO(req.CurrentTime)
	subj := strings.TrimSpace(req.Subject)
	if subj == "" {
		return fmt.Sprintf("Draft a confirmation for %s and update the calendar", when)
	}
	return fmt.Sprintf("Draft a confirmation to %s for %s and update the calendar", subj, when)
}

func (t *DraftMeetingConfirmationTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req DraftMeetingConfirmationRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %w", err)
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.CurrentTime = strings.TrimSpace(req.CurrentTime)
	if req.Subject == "" || req.CurrentTime == "" {
		return nil, fmt.Errorf("subject and current_time are required")
	}
	when := displayMeetingISO(req.CurrentTime)

	body := fmt.Sprintf("Hi,\n\nJust confirming our meeting is now set for %s. I've updated my side accordingly — please let me know if that no longer works for you.\n\nBest regards", when)
	resp := DraftMeetingConfirmationResponse{
		Requested:         true,
		Subject:           req.Subject,
		EmailSubject:      "Confirming our meeting time",
		EmailBody:         body,
		CalendarUpdateISO: req.CurrentTime,
	}
	return json.Marshal(resp)
}

// displayMeetingISO renders an ISO 8601 timestamp as "Fri Jul 10, 3:00 PM", falling back to the raw
// string when it doesn't parse (so the draft never loses the time).
func displayMeetingISO(iso string) string {
	iso = strings.TrimSpace(iso)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if tm, err := time.Parse(layout, iso); err == nil {
			return tm.Format("Mon Jan 2, 3:04 PM")
		}
	}
	return iso
}
