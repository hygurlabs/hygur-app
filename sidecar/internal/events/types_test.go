package events

import (
	"encoding/json"
	"testing"
)

func TestNewLMStudioEvent_PopulatesDataAndStatus(t *testing.T) {
	cases := []struct {
		name       string
		payload    LMStudioStatusPayload
		wantStatus Status
	}{
		{
			name:       "up flips to completed",
			payload:    LMStudioStatusPayload{Status: LMStudioStatusUp, URL: "http://localhost:1234", LatencyMs: 42},
			wantStatus: StatusCompleted,
		},
		{
			name:       "down flips to failed",
			payload:    LMStudioStatusPayload{Status: LMStudioStatusDown, URL: "http://localhost:1234"},
			wantStatus: StatusFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := NewLMStudioEvent(tc.payload)
			if evt.Type != EventTypeLMStudio {
				t.Errorf("Type = %q, want %q", evt.Type, EventTypeLMStudio)
			}
			if evt.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", evt.Status, tc.wantStatus)
			}
			if evt.Data["status"] != string(tc.payload.Status) {
				t.Errorf("Data[status] = %v, want %q", evt.Data["status"], tc.payload.Status)
			}
			if evt.CreatedAt.IsZero() {
				t.Error("CreatedAt was not set")
			}
		})
	}
}

func TestNewPriorityMailEvent_PopulatesAllFields(t *testing.T) {
	p := PriorityMailPayload{
		ContentID: "email:abc",
		Title:     "Déclaration TVA",
		From:      "compta@example.test",
		Amount:    "7421.85 EUR",
		DueDate:   "25 avril 2026",
		IBAN:      "BE68539007547034",
	}
	evt := NewPriorityMailEvent(p)
	if evt.Type != EventTypePriorityMail {
		t.Errorf("Type = %q, want %q", evt.Type, EventTypePriorityMail)
	}
	if evt.Source != p.ContentID {
		t.Errorf("Source = %q, want %q", evt.Source, p.ContentID)
	}
	if evt.Data["amount"] != p.Amount {
		t.Errorf("Data[amount] = %v, want %q", evt.Data["amount"], p.Amount)
	}
	if evt.Data["due_date"] != p.DueDate {
		t.Errorf("Data[due_date] = %v, want %q", evt.Data["due_date"], p.DueDate)
	}
}

func TestNewIngestEvent_StatusFollowsType(t *testing.T) {
	cases := []struct {
		name    string
		evtType EventType
		payload IngestPayload
		want    Status
	}{
		{"start is running", EventTypeIngestStart, IngestPayload{Path: "/x.md"}, StatusRunning},
		{"progress is running", EventTypeIngestProgress, IngestPayload{Path: "/x.md"}, StatusRunning},
		{"complete is completed", EventTypeIngestComplete, IngestPayload{ContentID: "abc", DurationMs: 12}, StatusCompleted},
		{"complete with error is failed", EventTypeIngestComplete, IngestPayload{ContentID: "abc", ErrorMsg: "boom"}, StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := NewIngestEvent(tc.evtType, tc.payload)
			if evt.Type != tc.evtType {
				t.Errorf("Type = %q, want %q", evt.Type, tc.evtType)
			}
			if evt.Status != tc.want {
				t.Errorf("Status = %q, want %q", evt.Status, tc.want)
			}
			if evt.Data["path"] != tc.payload.Path {
				t.Errorf("Data[path] = %v, want %q", evt.Data["path"], tc.payload.Path)
			}
		})
	}
}

func TestNewMailDigestEvent_FlattensItems(t *testing.T) {
	p := MailDigestPayload{
		Count: 2,
		Items: []MailDigestItem{
			{ContentID: "email:1", OneLiner: "💰 Facture 23,50 €"},
			{ContentID: "email:2", OneLiner: "📅 RDV jeudi"},
		},
	}
	evt := NewMailDigestEvent(p)
	if evt.Type != EventTypeMailDigest {
		t.Fatalf("Type = %q", evt.Type)
	}
	count, _ := evt.Data["count"].(int)
	if count != 2 {
		t.Errorf("count = %v want 2", evt.Data["count"])
	}
	items, ok := evt.Data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items shape: %T", evt.Data["items"])
	}
	if len(items) != 2 {
		t.Fatalf("len items = %d", len(items))
	}
	if items[0]["one_liner"] != "💰 Facture 23,50 €" {
		t.Errorf("item[0].one_liner = %v", items[0]["one_liner"])
	}
}

func TestNewBriefEvent_ErrorFlagControlsStatus(t *testing.T) {
	ok := NewBriefEvent(BriefPayload{Date: "2026-04-30", ContentID: "brief:1", Bullets: []string{"a", "b"}, ItemCount: 5})
	if ok.Status != StatusCompleted {
		t.Errorf("ok.Status = %q, want %q", ok.Status, StatusCompleted)
	}

	failed := NewBriefEvent(BriefPayload{Date: "2026-04-30", Error: true})
	if failed.Status != StatusFailed {
		t.Errorf("failed.Status = %q, want %q", failed.Status, StatusFailed)
	}
}

// TestEvent_JSONRoundTrip locks the wire format that the macOS app and any
// other consumer depend on. The `data` field must round-trip through JSON
// without loss, even when it carries nested slices.
func TestEvent_JSONRoundTrip(t *testing.T) {
	original := NewBriefEvent(BriefPayload{
		Date:      "2026-04-30",
		ContentID: "brief:1",
		Bullets:   []string{"Paiement TVA 7421.85 €", "Note ajoutée: contrat client X"},
		ItemCount: 12,
	})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != original.Type {
		t.Errorf("Type round-trip failed: got %q want %q", decoded.Type, original.Type)
	}
	if decoded.Source != original.Source {
		t.Errorf("Source round-trip failed: got %q want %q", decoded.Source, original.Source)
	}
	if decoded.Data["date"] != "2026-04-30" {
		t.Errorf("Data[date] round-trip lost: got %v", decoded.Data["date"])
	}
	// JSON deserializes []string as []any
	bullets, ok := decoded.Data["bullets"].([]any)
	if !ok {
		t.Fatalf("Data[bullets] type after round-trip: got %T", decoded.Data["bullets"])
	}
	if len(bullets) != 2 || bullets[0] != "Paiement TVA 7421.85 €" {
		t.Errorf("bullets round-trip mismatch: %v", bullets)
	}
	// JSON serializes int as float64; test the numeric value rather than the type.
	if itemCount, _ := decoded.Data["item_count"].(float64); itemCount != 12 {
		t.Errorf("item_count round-trip mismatch: got %v", decoded.Data["item_count"])
	}
}
