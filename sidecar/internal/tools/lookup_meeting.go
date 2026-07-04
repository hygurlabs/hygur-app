package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hygur/sidecar/internal/contradict"
	"github.com/hygur/sidecar/internal/fact"
	"github.com/hygur/sidecar/internal/rendezvous"
	"github.com/hygur/sidecar/internal/store"
)

// MeetingStore is the narrow slice of *store.DB the meeting lookup needs: resolve the meeting
// subject (the other party) to entity norms, then gather that subject's meeting-time nodes across
// sources. Reuses the SAME entity resolution the figure/identifier lookups use.
type MeetingStore interface {
	ResolvePersonNorms(ctx context.Context, query string, limit int) ([]string, error)
	MeetingNodesForEntities(ctx context.Context, norms []string) ([]store.MeetingNode, error)
}

// LookupMeetingTool resolves the CURRENT time of a meeting with a named party across the email
// thread and the calendar, and surfaces a cross-source CONTRADICTION when the calendar is stale
// (contradiction-aware rendez-vous). The time is engine-determined via internal/rendezvous (which
// reuses C7's supersession mechanism) — never the model's memory; the model only voices what the
// engine determined, or an honest decline.
type LookupMeetingTool struct {
	NoSideEffect
	store MeetingStore
}

// NewLookupMeetingTool builds the tool over the store.
func NewLookupMeetingTool(s MeetingStore) *LookupMeetingTool {
	return &LookupMeetingTool{store: s}
}

func (t *LookupMeetingTool) Name() string { return "lookup_meeting" }

func (t *LookupMeetingTool) Description() string {
	return "Get the CURRENT time of a meeting (rendez-vous) with a named person or organization, reconciled across the email thread and the calendar. Returns the deterministic current time (the latest assertion), and — when the calendar still shows a different, stale time — surfaces that cross-source contradiction so it can be corrected. Or an honest decline. Never a guessed time."
}

func (t *LookupMeetingTool) ParameterSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entity": map[string]any{
				"type":        "string",
				"description": "Whom the meeting is with — a person or organization name as the user refers to them (e.g. 'Acme', 'Dr. Miller').",
			},
		},
		"required": []string{"entity"},
	}
}

type meetingArgs struct {
	Entity string `json:"entity"`
}

// MeetingResponse is what the model receives — and, verbatim, what the chat handler turns into the
// authoritative determined_answer render. The engine PRODUCES the current time + any contradiction;
// the model only voices it. Value/Label are pre-composed for the cut-LLM-safe card.
type MeetingResponse struct {
	Label   string `json:"label,omitempty"`   // "Meeting with Acme"
	Subject string `json:"subject,omitempty"` // whom the meeting is with (display)
	Value   string `json:"value,omitempty"`   // current time display, e.g. "Fri Jul 10, 3:00 PM"
	// Note surfaces the cross-source contradiction ("Your calendar still shows … — that's stale.").
	Note string `json:"note,omitempty"`
	// Offer is the gated follow-up action offered ONLY when there is a contradiction to fix.
	Offer string `json:"offer,omitempty"`
	// Contradiction/StaleSource/CurrentISO/StaleISO carry the structured facts the draft action needs.
	Contradiction bool          `json:"contradiction"`
	CurrentISO    string        `json:"current_iso,omitempty"`
	StaleISO      string        `json:"stale_iso,omitempty"`
	StaleSource   string        `json:"stale_source,omitempty"`
	Tier          fact.Tier     `json:"confidence"`
	Reason        string        `json:"reason,omitempty"`
	Guidance      string        `json:"guidance"`
	Sources       []fact.Source `json:"sources,omitempty"`
}

func (t *LookupMeetingTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a meetingArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	a.Entity = strings.TrimSpace(a.Entity)
	if a.Entity == "" {
		return nil, fmt.Errorf("entity is required")
	}
	display := a.Entity

	// Resolve the meeting subject (the other party) to entity norms, then gather its meeting nodes.
	norms, _ := t.store.ResolvePersonNorms(ctx, a.Entity, 20)
	norms = append(norms, contradict.NormKey(a.Entity))
	rows, err := t.store.MeetingNodesForEntities(ctx, norms)
	if err != nil {
		return nil, err
	}

	// A meeting-time question is about ONE meeting subject. If the gathered nodes span several
	// distinct subjects (the name resolved ambiguously), decline rather than mix meetings.
	subjects := map[string]bool{}
	nodes := make([]rendezvous.Node, 0, len(rows))
	for _, r := range rows {
		subjects[r.EntityNorm] = true
		nodes = append(nodes, rendezvous.Node{
			Subject: r.EntityNorm, When: r.When, Source: r.Source,
			AssertedAt: r.AssertedAt, ContentID: r.ContentID, Title: r.Title,
		})
	}

	out := MeetingResponse{Subject: display, Label: "Meeting with " + display}
	if len(subjects) > 1 {
		out.Tier = fact.TierNone
		out.Reason = rendezvous.ReasonAmbiguous
		out.Guidance = "Several distinct meetings match that name. Do NOT state a time — ask the user which meeting they mean."
		return json.Marshal(out)
	}

	res := rendezvous.Resolve(nodes)
	if res.Reason != "" {
		out.Tier = fact.TierNone
		out.Reason = res.Reason
		switch res.Reason {
		case rendezvous.ReasonAmbiguous:
			out.Guidance = "The meeting times cannot be ordered into a single current time. Do NOT state a time — say the record is inconsistent."
		default: // no meeting
			out.Guidance = "You have no meeting time on record with that party. Do NOT invent one — say so honestly."
		}
		return json.Marshal(out)
	}

	out.Tier = fact.TierHigh
	out.Value = rendezvous.DisplayTime(res.Current)
	out.CurrentISO = res.Current.UTC().Format("2006-01-02T15:04:05Z07:00")
	out.Sources = res.Sources
	out.Guidance = "State this meeting time plainly as the answer and cite the source(s). Do NOT alter the time."
	if res.Contradiction {
		out.Contradiction = true
		out.StaleSource = res.StaleSource
		out.StaleISO = res.StaleWhen.UTC().Format("2006-01-02T15:04:05Z07:00")
		out.Note = "Your " + rendezvous.SourceLabel(res.StaleSource) + " still shows " +
			rendezvous.DisplayTime(res.StaleWhen) + " — that's a contradiction (stale)."
		out.Offer = "Want me to draft a confirmation email and update your " +
			rendezvous.SourceLabel(res.StaleSource) + "? I won't send or change anything without your OK."
		out.Guidance += " The " + rendezvous.SourceLabel(res.StaleSource) +
			" is stale — state the current time, mention the stale one exactly as in the note, and offer to fix it (draft + update) without acting."
	}
	return json.Marshal(out)
}
