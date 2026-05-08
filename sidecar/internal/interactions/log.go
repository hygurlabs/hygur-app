// Package interactions records user-facing signals (chat sent, brief opened,
// memory accepted, connector synced, …) into the append-only interaction_log
// table. Phase 1 of the pair-mode roadmap: without these signals, phases 2-5
// (recap slot detection, adaptive ranking, contradiction prioritisation,
// context awareness) have nothing to learn from.
//
// Append is the only public mutation. The log is intentionally append-only —
// no updates, no deletions — so coverage and histogram queries can trust the
// timeline.
package interactions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hygur/sidecar/internal/store"
)

// Kind enumerates the interaction kinds the sidecar accepts. Keep this in
// sync with the Swift InteractionLogger and any future phases that add
// signal sources.
type Kind string

const (
	KindChatMessageSent      Kind = "chat_message_sent"
	KindChatMessageReceived  Kind = "chat_message_received"
	KindBriefOpened          Kind = "brief_opened"
	KindBriefDismissed       Kind = "brief_dismissed"
	KindMemoryAccepted       Kind = "memory_accepted"
	KindMemoryDiscarded      Kind = "memory_discarded"
	KindMemorySuperseded     Kind = "memory_superseded"
	KindDocumentOpened       Kind = "document_opened"
	KindAgendaActionDone     Kind = "agenda_action_completed"
	KindConnectorSynced      Kind = "connector_synced"
	KindAppLaunched          Kind = "app_launched"
	KindFrontmostAppChanged  Kind = "frontmost_app_changed"
)

// allowedKinds is the validation set for inbound writes. Keeps the table
// from filling with typos or experimental kinds that would skew phase 2/3
// analytics.
var allowedKinds = map[Kind]struct{}{
	KindChatMessageSent:     {},
	KindChatMessageReceived: {},
	KindBriefOpened:         {},
	KindBriefDismissed:      {},
	KindMemoryAccepted:      {},
	KindMemoryDiscarded:     {},
	KindMemorySuperseded:    {},
	KindDocumentOpened:      {},
	KindAgendaActionDone:    {},
	KindConnectorSynced:     {},
	KindAppLaunched:         {},
	KindFrontmostAppChanged: {},
}

// Event is the input shape callers (HTTP handler, internal scheduler hooks)
// hand to Append.
type Event struct {
	Kind      Kind           `json:"kind"`
	RefKind   string         `json:"ref_kind,omitempty"`
	RefID     string         `json:"ref_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
}

// Logger appends interaction events to the store. Construct one per process
// — it holds no state beyond the DB handle, so a singleton is fine.
type Logger struct {
	db *store.DB
}

// NewLogger returns an interaction logger backed by the given store.
func NewLogger(db *store.DB) *Logger {
	return &Logger{db: db}
}

// Append validates the kind, marshals the optional payload, and writes a
// row to interaction_log. Errors fall back to the caller (HTTP handler logs
// + 5xx; in-process callers log and continue) — we never want a logging
// failure to break a user-visible flow.
func (l *Logger) Append(ctx context.Context, ev Event) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("interactions logger not initialised")
	}
	if _, ok := allowedKinds[ev.Kind]; !ok {
		return fmt.Errorf("unknown interaction kind %q", ev.Kind)
	}
	payload := ""
	if len(ev.Payload) > 0 {
		raw, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		payload = string(raw)
	}
	return l.db.AppendInteraction(ctx, string(ev.Kind), ev.RefKind, ev.RefID, payload, ev.SessionID)
}
