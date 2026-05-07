package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewCreateCalendarEventTool(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	if tool == nil {
		t.Fatal("NewCreateCalendarEventTool() returned nil")
	}
}

func TestCreateCalendarEventTool_Run_Success(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	ctx := context.Background()

	req := CreateCalendarEventRequest{
		Title: "Project sync",
		Start: "2026-05-08T10:00:00Z",
		End:   "2026-05-08T11:00:00Z",
		Notes: "Discuss Q2 roadmap",
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if !resp.Requested {
		t.Error("Requested should be true (sentinel for native confirmation)")
	}
	if resp.Title != "Project sync" {
		t.Errorf("Title = %q, want %q", resp.Title, "Project sync")
	}
	if resp.Start != "2026-05-08T10:00:00Z" {
		t.Errorf("Start round-trip = %q", resp.Start)
	}
	if resp.End != "2026-05-08T11:00:00Z" {
		t.Errorf("End round-trip = %q", resp.End)
	}
	if resp.Notes != "Discuss Q2 roadmap" {
		t.Errorf("Notes = %q", resp.Notes)
	}
}

func TestCreateCalendarEventTool_Run_OptionalCalendarName(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	ctx := context.Background()

	req := CreateCalendarEventRequest{
		Title:        "Lunch",
		Start:        "2026-05-08T12:00:00Z",
		End:          "2026-05-08T13:00:00Z",
		CalendarName: "Personal",
	}

	resp, err := tool.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if resp.CalendarName != "Personal" {
		t.Errorf("CalendarName = %q, want %q", resp.CalendarName, "Personal")
	}
}

func TestCreateCalendarEventRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateCalendarEventRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid",
			req: CreateCalendarEventRequest{
				Title: "Sync", Start: "2026-05-08T10:00:00Z", End: "2026-05-08T11:00:00Z",
			},
			wantErr: false,
		},
		{
			name:    "empty title",
			req:     CreateCalendarEventRequest{Start: "2026-05-08T10:00:00Z", End: "2026-05-08T11:00:00Z"},
			wantErr: true,
			errMsg:  "title is required",
		},
		{
			name:    "empty start",
			req:     CreateCalendarEventRequest{Title: "X", End: "2026-05-08T11:00:00Z"},
			wantErr: true,
			errMsg:  "start is required",
		},
		{
			name:    "empty end",
			req:     CreateCalendarEventRequest{Title: "X", Start: "2026-05-08T10:00:00Z"},
			wantErr: true,
			errMsg:  "end is required",
		},
		{
			name: "end before start",
			req: CreateCalendarEventRequest{
				Title: "X", Start: "2026-05-08T11:00:00Z", End: "2026-05-08T10:00:00Z",
			},
			wantErr: true,
			errMsg:  "end must be after start",
		},
		{
			name: "end equal to start",
			req: CreateCalendarEventRequest{
				Title: "X", Start: "2026-05-08T10:00:00Z", End: "2026-05-08T10:00:00Z",
			},
			wantErr: true,
			errMsg:  "end must be after start",
		},
		{
			name: "garbage start timestamp",
			req: CreateCalendarEventRequest{
				Title: "X", Start: "tomorrow at 10", End: "2026-05-08T11:00:00Z",
			},
			wantErr: true,
			errMsg:  "invalid start timestamp",
		},
		{
			name: "date-only timestamps (all day)",
			req: CreateCalendarEventRequest{
				Title: "Holiday", Start: "2026-05-08", End: "2026-05-09",
			},
			wantErr: false,
		},
		{
			name: "RFC3339 with offset",
			req: CreateCalendarEventRequest{
				Title: "Sync", Start: "2026-05-08T10:00:00+02:00", End: "2026-05-08T11:00:00+02:00",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestCreateCalendarEventTool_Run_Validation(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	ctx := context.Background()

	_, err := tool.Run(ctx, CreateCalendarEventRequest{
		Title: "", Start: "2026-05-08T10:00:00Z", End: "2026-05-08T11:00:00Z",
	})
	if err == nil {
		t.Fatal("Run() should fail for empty title")
	}
	if !strings.Contains(err.Error(), "validation error") {
		t.Errorf("error should mention 'validation error', got: %v", err)
	}
}

func TestCreateCalendarEventTool_ToolDefinition(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	def := tool.ToolDefinition()

	if def["type"] != "function" {
		t.Errorf("type = %v, want %q", def["type"], "function")
	}
	fn, ok := def["function"].(map[string]any)
	if !ok {
		t.Fatal("function should be a map")
	}
	if fn["name"] != "create_calendar_event" {
		t.Errorf("name = %v, want %q", fn["name"], "create_calendar_event")
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("parameters should be a map")
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("required should be []string")
	}
	want := map[string]bool{"title": false, "start": false, "end": false}
	for _, r := range required {
		want[r] = true
	}
	for k, present := range want {
		if !present {
			t.Errorf("required field %q missing", k)
		}
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties should be a map")
	}
	for _, field := range []string{"title", "start", "end", "notes", "calendar_name"} {
		if _, exists := props[field]; !exists {
			t.Errorf("property %q should exist in tool definition", field)
		}
	}
}

func TestCreateCalendarEventTool_ToolDefinition_Serialization(t *testing.T) {
	tool := NewCreateCalendarEventTool()
	def := tool.ToolDefinition()

	jsonBytes, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("Failed to marshal ToolDefinition: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(jsonBytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal ToolDefinition: %v", err)
	}
	fn := parsed["function"].(map[string]any)
	if fn["name"] != "create_calendar_event" {
		t.Errorf("function.name round-trip mismatch: %v", fn["name"])
	}
}

func TestCreateCalendarEventTool_ParseRequest(t *testing.T) {
	tool := NewCreateCalendarEventTool()

	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(*CreateCalendarEventRequest) error
	}{
		{
			name:    "full payload",
			input:   `{"title":"Sync","start":"2026-05-08T10:00:00Z","end":"2026-05-08T11:00:00Z","notes":"agenda","calendar_name":"Work"}`,
			wantErr: false,
			check: func(req *CreateCalendarEventRequest) error {
				if req.Title != "Sync" {
					return &checkError{msg: "title mismatch"}
				}
				if req.CalendarName != "Work" {
					return &checkError{msg: "calendar_name mismatch"}
				}
				return nil
			},
		},
		{
			name:    "minimal payload",
			input:   `{"title":"Sync","start":"2026-05-08T10:00:00Z","end":"2026-05-08T11:00:00Z"}`,
			wantErr: false,
			check: func(req *CreateCalendarEventRequest) error {
				if req.Notes != "" || req.CalendarName != "" {
					return &checkError{msg: "optional fields should be empty"}
				}
				return nil
			},
		},
		{
			name:    "invalid JSON",
			input:   `{"title":"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := tool.ParseRequest(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRequest() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil && req != nil {
				if err := tt.check(req); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
