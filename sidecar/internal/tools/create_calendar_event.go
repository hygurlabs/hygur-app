// Package tools provides callable tools for Hygur chat functionality.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CreateCalendarEventTool exposes the LLM-callable schema for creating a
// calendar event. The actual EventKit write happens on the macOS side because
// EKEventStore lives in the user's session — the sidecar only validates the
// request, normalises the timestamps, and emits a structured response so the
// macOS layer can surface a confirmation sheet before any side-effect.
// CreateCalendarEventTool keeps its OWN native macOS confirmation flow: it
// returns {requested:true} and the macOS app surfaces an EventKit sheet before
// any write. It therefore embeds NoSideEffect (SideEffect()==false) so the
// generic pending_action gate does NOT intercept it — that would break the
// desktop app's native sheet.
type CreateCalendarEventTool struct{ NoSideEffect }

// NewCreateCalendarEventTool returns a CreateCalendarEventTool. It takes no
// dependencies — by design, this tool does not touch any local state. The
// macOS app reads the structured response from the SSE tool_call event and
// performs the write after explicit user confirmation.
func NewCreateCalendarEventTool() *CreateCalendarEventTool {
	return &CreateCalendarEventTool{}
}

// CreateCalendarEventRequest is the LLM-supplied tool call payload.
// Times are ISO 8601 strings to keep the JSON schema simple and to match the
// model's preferred output format. We re-parse them in Run() to validate.
type CreateCalendarEventRequest struct {
	Title        string `json:"title"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Notes        string `json:"notes,omitempty"`
	CalendarName string `json:"calendar_name,omitempty"`
}

// CreateCalendarEventResponse is what the sidecar emits back to the macOS
// layer via the chat tool_call SSE event. The "Requested" flag is the contract
// signalling that no write has happened yet — the macOS app must present a
// confirmation sheet and call EKEventStore.save itself.
type CreateCalendarEventResponse struct {
	Requested    bool   `json:"requested"`
	Title        string `json:"title"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Notes        string `json:"notes,omitempty"`
	CalendarName string `json:"calendar_name,omitempty"`
}

// Validate rejects empty or malformed requests early so the macOS layer never
// has to render a confirmation sheet for a junk LLM call.
func (r *CreateCalendarEventRequest) Validate() error {
	if r.Title == "" {
		return fmt.Errorf("title is required")
	}
	if r.Start == "" {
		return fmt.Errorf("start is required")
	}
	if r.End == "" {
		return fmt.Errorf("end is required")
	}
	start, err := parseISO8601(r.Start)
	if err != nil {
		return fmt.Errorf("invalid start timestamp: %w", err)
	}
	end, err := parseISO8601(r.End)
	if err != nil {
		return fmt.Errorf("invalid end timestamp: %w", err)
	}
	if !end.After(start) {
		return fmt.Errorf("end must be after start")
	}
	return nil
}

// Run validates the request and returns the structured "pending native
// confirmation" response. It deliberately does NOT touch any calendar state.
func (t *CreateCalendarEventTool) Run(ctx context.Context, req CreateCalendarEventRequest) (*CreateCalendarEventResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}
	return &CreateCalendarEventResponse{
		Requested:    true,
		Title:        req.Title,
		Start:        req.Start,
		End:          req.End,
		Notes:        req.Notes,
		CalendarName: req.CalendarName,
	}, nil
}

// ToolDefinition returns the JSON schema exposed to the LLM during chat.
// The shape mirrors create_note: a top-level {type, function} envelope with a
// JSON-Schema "parameters" object listing required + optional fields.
func (t *CreateCalendarEventTool) ToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "create_calendar_event",
			"description": "Request the creation of a calendar event in the user's macOS Calendar. The event is NOT created automatically — the macOS app surfaces a confirmation sheet so the user can review and edit before saving.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Title of the event",
					},
					"start": map[string]any{
						"type":        "string",
						"description": "Start time as an ISO 8601 timestamp (e.g. 2026-05-08T10:00:00Z)",
					},
					"end": map[string]any{
						"type":        "string",
						"description": "End time as an ISO 8601 timestamp. Must be strictly after `start`.",
					},
					"notes": map[string]any{
						"type":        "string",
						"description": "Optional free-form notes to attach to the event",
					},
					"calendar_name": map[string]any{
						"type":        "string",
						"description": "Optional name of a specific calendar to add the event to. If omitted, the user's default calendar is used.",
					},
				},
				"required": []string{"title", "start", "end"},
			},
		},
	}
}

// ParseRequest unmarshals a JSON payload (typically the LLM's tool_call
// arguments string) into a typed request. Mirrors create_note's helper so the
// chat layer can call ParseRequest + Run uniformly across tools.
func (t *CreateCalendarEventTool) ParseRequest(jsonStr string) (*CreateCalendarEventRequest, error) {
	var req CreateCalendarEventRequest
	if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
		return nil, fmt.Errorf("failed to parse request: %w", err)
	}
	return &req, nil
}

// --- tools.Tool interface ---

// Name returns the function identifier the LLM sees in tool-calls.
func (t *CreateCalendarEventTool) Name() string {
	return "create_calendar_event"
}

// Description tells the model when to use this tool. Kept short so the
// tool-list payload stays cheap on context.
func (t *CreateCalendarEventTool) Description() string {
	return "Request the creation of a calendar event in the user's macOS Calendar. The event is NOT created automatically — the macOS app surfaces a confirmation sheet so the user can review and edit before saving."
}

// ParameterSchema mirrors the JSON Schema embedded in ToolDefinition. Kept in
// sync manually because Tool.OpenAIDefinitions() consumes it directly.
func (t *CreateCalendarEventTool) ParameterSchema() map[string]any {
	def := t.ToolDefinition()
	if fn, ok := def["function"].(map[string]any); ok {
		if params, ok := fn["parameters"].(map[string]any); ok {
			return params
		}
	}
	return map[string]any{"type": "object"}
}

// Execute parses the LLM-supplied arguments, validates them, and returns the
// "pending native confirmation" payload as JSON. The macOS app receives this
// on the SSE `tool_call` event and surfaces CreateCalendarEventSheet — the
// actual EKEventStore write only happens after explicit user confirmation.
func (t *CreateCalendarEventTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req CreateCalendarEventRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("failed to parse arguments: %w", err)
	}
	resp, err := t.Run(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

// parseISO8601 accepts the two flavours the LLM tends to emit: full RFC3339
// with offset (`...Z` or `+02:00`) and the date-only form for all-day events.
// We try them in order and bubble up the first error if both fail.
func parseISO8601(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected ISO 8601 timestamp, got %q", s)
}
