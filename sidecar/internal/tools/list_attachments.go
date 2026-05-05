// Package tools provides AI-powered utilities for processing emails and other content.
package tools

import (
	"context"
	"fmt"

	"github.com/hygur/sidecar/internal/mail"
)

// ListAttachmentsTool lists attachments from email threads.
type ListAttachmentsTool struct {
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
