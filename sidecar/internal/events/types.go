// Package events provides an event broker for background operations.
package events

import (
	"time"
)

// EventType identifies the kind of event being broadcast.
type EventType string

const (
	// EventTypeSync signals a sync operation has started or completed.
	EventTypeSync EventType = "sync"
	// EventTypeIngest signals an ingest operation has started or completed.
	EventTypeIngest EventType = "ingest"
	// EventTypeMail signals a mail operation has started or completed.
	EventTypeMail EventType = "mail"
	// EventTypeConnectors signals a connector operation has started or completed.
	EventTypeConnectors EventType = "connectors"

	// EventTypeLMStudio signals a change in LM Studio reachability. Emitted
	// only on a status flip (up→down or down→up), never on every health tick.
	EventTypeLMStudio EventType = "lm_studio"
	// EventTypePriorityMail signals that a freshly-indexed email is both
	// flagged high_priority and carries an actionable amount or due date.
	EventTypePriorityMail EventType = "priority_mail"
	// EventTypeBrief signals that the daily-brief task has produced a new
	// digest of recent activity.
	EventTypeBrief EventType = "brief"

	// EventTypeIngestStart signals that the ingest pipeline began processing
	// a single document. Carries an IngestPayload with the path being parsed.
	EventTypeIngestStart EventType = "ingest_start"
	// EventTypeIngestProgress signals incremental progress during a long
	// ingest cycle. Throttled by emitters (max ~1 Hz) to avoid SSE spam.
	EventTypeIngestProgress EventType = "ingest_progress"
	// EventTypeIngestComplete signals that a document finished ingesting,
	// with the resulting content_id and total duration.
	EventTypeIngestComplete EventType = "ingest_complete"
	// EventTypeMailDigest signals that a mail sync cycle finished and one or
	// more priority items were identified and summarised into one-liners.
	EventTypeMailDigest EventType = "mail_digest"

	// EventTypeAgendaAlert signals that a high-priority action has a deadline
	// within the next 48 hours. Emitted by the agenda scheduler.
	EventTypeAgendaAlert EventType = "agenda_alert"
)

// AgendaAlertPayload carries the details of an imminent agenda deadline.
type AgendaAlertPayload struct {
	What     string `json:"what"`
	Deadline string `json:"deadline_iso"`
	SourceID string `json:"source_id"`
	Priority string `json:"priority"`
}

// Status indicates the current state of an event.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Event represents a broadcastable event.
type Event struct {
	Type      EventType      `json:"type"`
	Source    string         `json:"source"`
	Status    Status         `json:"status"`
	Message   string         `json:"message,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
