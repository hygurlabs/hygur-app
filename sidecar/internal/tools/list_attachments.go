// Package tools provides AI-powered utilities for processing emails and other content.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hygur/sidecar/internal/mail"
)

// ListAttachmentsTool lists attachments from email threads.
type ListAttachmentsTool struct {
	// Read-only: embeds NoSideEffect so it is never gated by the confirmation flow.
	NoSideEffect
	connectors map[string]mail.MailConnector
}

// NewListAttachmentsTool creates a new ListAttachmentsTool with the given connectors.
func NewListAttachmentsTool(connectors map[string]mail.MailConnector) *ListAttachmentsTool {
	return &ListAttachmentsTool{
		connectors: connectors,
	}
}

// AttachmentInfo represents an attachment with metadata for API responses.
type AttachmentInfo struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// ListAttachmentsRequest contains the parameters for listing attachments.
type ListAttachmentsRequest struct {
	ThreadID string `json:"thread_id"`
	Source   string `json:"source"`
}

// ListAttachmentsResponse contains the list of attachments for a thread.
type ListAttachmentsResponse struct {
	Attachments []AttachmentInfo `json:"attachments"`
	ThreadID    string           `json:"thread_id"`
	Source      string           `json:"source"`
}

// Run lists all attachments from messages in the specified thread.
// It aggregates attachments from all messages within the thread.
func (t *ListAttachmentsTool) Run(ctx context.Context, req ListAttachmentsRequest) (*ListAttachmentsResponse, error) {
	if req.ThreadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	if req.Source == "" {
		return nil, fmt.Errorf("source is required")
	}

	connector, exists := t.connectors[req.Source]
	if !exists {
		return nil, fmt.Errorf("source not found: %s", req.Source)
	}

	if !connector.IsConnected() {
		return nil, fmt.Errorf("source is not connected: %s", req.Source)
	}

	// Fetch messages from the thread
	messages, err := connector.GetMessages(ctx, req.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	// Aggregate attachments from all messages
	var attachments []AttachmentInfo
	for _, msg := range messages {
		for _, att := range msg.Attachments {
			attachments = append(attachments, AttachmentInfo{
				ID:       att.ID,
				Filename: att.Filename,
				MIMEType: att.MimeType,
				Size:     att.Size,
			})
		}
	}

	// Ensure we return an empty slice instead of nil for JSON serialization
	if attachments == nil {
		attachments = []AttachmentInfo{}
	}

	return &ListAttachmentsResponse{
		Attachments: attachments,
		ThreadID:    req.ThreadID,
		Source:      req.Source,
	}, nil
}

// --- LLM tool adapter -------------------------------------------------------
// Exposes list_attachments to the chat path so the assistant can answer
// "what's attached to that email?" from a source citation, instead of guessing
// from the indexed body text.

// Name implements tools.Tool.
func (t *ListAttachmentsTool) Name() string { return "list_attachments" }

// Description implements tools.Tool.
func (t *ListAttachmentsTool) Description() string {
	return "List the file attachments of one of the user's email threads (filename, type, size)."
}

// ParameterSchema implements tools.Tool. thread_id is the id after \"email:\" in a
// source citation; source (the mailbox connector) is optional — omit to search
// every connected mailbox.
func (t *ListAttachmentsTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"thread_id": map[string]any{
				"type":        "string",
				"description": "The email thread id (the part after \"email:\" in a source citation).",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "Optional mailbox connector id (e.g. gmail, proton). Omit to search all connected mailboxes.",
			},
		},
		"required": []string{"thread_id"},
	}
}

// Execute implements tools.Tool: resolve the thread in the named source, or in
// every connected mailbox when source is omitted, and return its attachments.
func (t *ListAttachmentsTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var req ListAttachmentsRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if req.ThreadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	var sources []string
	if req.Source != "" {
		sources = []string{req.Source}
	} else {
		for name := range t.connectors {
			sources = append(sources, name)
		}
		sort.Strings(sources) // deterministic across runs
	}

	var lastErr error
	for _, src := range sources {
		conn, ok := t.connectors[src]
		if !ok || !conn.IsConnected() {
			continue
		}
		resp, err := t.Run(ctx, ListAttachmentsRequest{ThreadID: req.ThreadID, Source: src})
		if err != nil {
			lastErr = err // thread likely not in this mailbox — try the next
			continue
		}
		return json.Marshal(resp)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	// No connected mailbox had the thread → empty list, not an error.
	return json.Marshal(ListAttachmentsResponse{Attachments: []AttachmentInfo{}, ThreadID: req.ThreadID})
}
