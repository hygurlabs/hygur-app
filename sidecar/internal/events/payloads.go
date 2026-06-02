package events

import (
	"time"
)

// Payload structs are POD types meant to be marshalled into Event.Data via
// the dedicated constructors below. Wire format is `data: { ...fields }`
// — the constructors flatten the struct into Event.Data using explicit
// keys, so the SSE consumer reads stable field names without reflection.

// LMStudioStatus is the value carried by EventTypeLMStudio events.
type LMStudioStatus string

const (
	LMStudioStatusUp      LMStudioStatus = "up"
	LMStudioStatusDown    LMStudioStatus = "down"
	LMStudioStatusUnknown LMStudioStatus = "unknown"
)

// LMStudioStatusPayload describes a transition in LM Studio reachability.
type LMStudioStatusPayload struct {
	Status    LMStudioStatus
	URL       string
	LatencyMs int64
}

// PriorityMailPayload describes a freshly-indexed mail flagged as actionable
// (high_priority + extracted amount or due date present).
type PriorityMailPayload struct {
	ContentID string
	Title     string
	From      string
	Amount    string // first extracted_amount, formatted as "1234.56 EUR" — empty if none
	DueDate   string // first extracted_due_date — empty if none
	IBAN      string // first extracted_iban — empty if none
}

// BriefPayload describes the daily brief that was just persisted.
type BriefPayload struct {
	Date      string   // YYYY-MM-DD in the configured local time zone
	ContentID string   // knowledge_item id where the full markdown lives
	Bullets   []string // short list of headline bullets pulled from the brief
	ItemCount int      // how many source items the brief drew from
	Error     bool     // true when the LLM call failed and the brief is a placeholder
}

// NewLMStudioEvent constructs an Event with EventTypeLMStudio and a fully
// populated Data map. Caller must supply the previous status so we can encode
// the transition explicitly.
func NewLMStudioEvent(p LMStudioStatusPayload) Event {
	status := StatusCompleted
	if p.Status == LMStudioStatusDown {
		status = StatusFailed
	}
	return Event{
		Type:    EventTypeLMStudio,
		Source:  p.URL,
		Status:  status,
		Message: "LM Studio is " + string(p.Status),
		Data: map[string]any{
			"status":     string(p.Status),
			"url":        p.URL,
			"latency_ms": p.LatencyMs,
		},
		CreatedAt: time.Now(),
	}
}

// NewPriorityMailEvent constructs an Event for a freshly-indexed actionable email.
func NewPriorityMailEvent(p PriorityMailPayload) Event {
	return Event{
		Type:    EventTypePriorityMail,
		Source:  p.ContentID,
		Status:  StatusCompleted,
		Message: p.Title,
		Data: map[string]any{
			"content_id": p.ContentID,
			"title":      p.Title,
			"from":       p.From,
			"amount":     p.Amount,
			"due_date":   p.DueDate,
			"iban":       p.IBAN,
		},
		CreatedAt: time.Now(),
	}
}

// IngestPayload describes a single ingest pipeline event. Status field on
// the Event itself encodes start/running/completed/failed; this payload
// carries the per-document context (path, content_id, duration).
type IngestPayload struct {
	ContentID  string // empty for "start" events (id not yet allocated)
	Path       string // absolute path being ingested
	SourceType string // markdown, pdf, docx, ... — empty until parser identified
	DurationMs int64  // populated on complete events
	ErrorMsg   string // populated on failed events
}

// MailDigestItem is one entry in the per-cycle mail digest. The one_liner
// is produced upstream by the mail summarizer (templated when entities are
// complete, LLM otherwise, fallback to subject on failure).
type MailDigestItem struct {
	ContentID string `json:"content_id"`
	OneLiner  string `json:"one_liner"`
}

// MailDigestPayload describes the actionable mails identified during the
// just-finished sync cycle. Count >= len(Items) when summarisation skipped
// some items (rate-limited or too low priority); the consumer uses Count as
// authoritative for "X new important messages".
type MailDigestPayload struct {
	Count int
	Items []MailDigestItem
}

// NewIngestEvent constructs an Event for an ingest pipeline transition.
// `evtType` must be one of EventTypeIngestStart / Progress / Complete; the
// matching status is derived to keep the wire format consistent (start →
// running, progress → running, complete → completed, error → failed).
func NewIngestEvent(evtType EventType, p IngestPayload) Event {
	status := StatusRunning
	switch evtType {
	case EventTypeIngestComplete:
		status = StatusCompleted
		if p.ErrorMsg != "" {
			status = StatusFailed
		}
	}
	source := p.ContentID
	if source == "" {
		source = p.Path
	}
	msg := p.Path
	if p.ErrorMsg != "" {
		msg = p.ErrorMsg
	}
	return Event{
		Type:    evtType,
		Source:  source,
		Status:  status,
		Message: msg,
		Data: map[string]any{
			"content_id":  p.ContentID,
			"path":        p.Path,
			"source_type": p.SourceType,
			"duration_ms": p.DurationMs,
			"error":       p.ErrorMsg,
		},
		CreatedAt: time.Now(),
	}
}

// NewMailDigestEvent constructs an Event announcing the priority mails of a
// just-finished sync cycle. Always EventTypeMailDigest with StatusCompleted.
func NewMailDigestEvent(p MailDigestPayload) Event {
	items := make([]map[string]any, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, map[string]any{
			"content_id": it.ContentID,
			"one_liner":  it.OneLiner,
		})
	}
	return Event{
		Type:    EventTypeMailDigest,
		Source:  "mail",
		Status:  StatusCompleted,
		Message: "mail digest",
		Data: map[string]any{
			"count": p.Count,
			"items": items,
		},
		CreatedAt: time.Now(),
	}
}

// MeetingBriefingPayload describes a briefing generated ahead of an event or
// deadline. Kind is "calendar" or "mail".
type MeetingBriefingPayload struct {
	Kind      string
	ContentID string
	Title     string
	WhenISO   string
	Bullets   []string
}

// NewMeetingBriefingEvent constructs an Event announcing a meeting/deadline
// briefing. Always EventTypeMeetingBriefing with StatusCompleted.
func NewMeetingBriefingEvent(p MeetingBriefingPayload) Event {
	return Event{
		Type:    EventTypeMeetingBriefing,
		Source:  p.ContentID,
		Status:  StatusCompleted,
		Message: p.Title,
		Data: map[string]any{
			"kind":       p.Kind,
			"content_id": p.ContentID,
			"title":      p.Title,
			"when":       p.WhenISO,
			"bullets":    p.Bullets,
		},
		CreatedAt: time.Now(),
	}
}

// NewBriefEvent constructs an Event announcing a freshly-generated daily brief.
func NewBriefEvent(p BriefPayload) Event {
	status := StatusCompleted
	if p.Error {
		status = StatusFailed
	}
	return Event{
		Type:    EventTypeBrief,
		Source:  p.ContentID,
		Status:  status,
		Message: "Daily brief — " + p.Date,
		Data: map[string]any{
			"date":       p.Date,
			"content_id": p.ContentID,
			"bullets":    p.Bullets,
			"item_count": p.ItemCount,
			"error":      p.Error,
		},
		CreatedAt: time.Now(),
	}
}
