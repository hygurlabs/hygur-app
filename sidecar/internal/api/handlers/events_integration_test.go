package handlers

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/events"
	"github.com/rs/zerolog"
)

// TestEvents_NewPayloadEventsSurfaceOverSSE proves end-to-end that an event
// constructed via the new typed-payload helpers (Lot A) survives the full
// path through the broker, the /events SSE handler, and JSON line-framed
// transport — same shape the macOS app's EventStreamService consumes.
func TestEvents_NewPayloadEventsSurfaceOverSSE(t *testing.T) {
	broker := events.NewBroker()
	handler := NewEventsHandler(broker, zerolog.New(io.Discard))

	server := httptest.NewServer(http.HandlerFunc(handler.Handle))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Read line by line; the first event is the "connection" hello, then
	// our published events.
	reader := bufio.NewReader(resp.Body)

	// Drain the connection hello.
	if _, err := readSSELine(reader); err != nil {
		t.Fatalf("read hello: %v", err)
	}

	// Publish a priority_mail event.
	go func() {
		// Tiny delay so the subscription is established before publish.
		time.Sleep(50 * time.Millisecond)
		broker.Publish(events.NewPriorityMailEvent(events.PriorityMailPayload{
			ContentID: "email:test-1",
			Title:     "Déclaration TVA",
			From:      "compta@example.test",
			Amount:    "7421.85 EUR",
			DueDate:   "25 avril 2026",
			IBAN:      "BE68539007547034",
		}))
	}()

	got, err := readSSELine(reader)
	if err != nil {
		t.Fatalf("read priority_mail: %v", err)
	}
	if got["type"] != "priority_mail" {
		t.Errorf("type = %v, want priority_mail", got["type"])
	}
	data, _ := got["data"].(map[string]any)
	if data["amount"] != "7421.85 EUR" {
		t.Errorf("data.amount = %v, want 7421.85 EUR", data["amount"])
	}
	if data["iban"] != "BE68539007547034" {
		t.Errorf("data.iban = %v, want BE68539007547034", data["iban"])
	}
	if data["due_date"] != "25 avril 2026" {
		t.Errorf("data.due_date = %v, want 25 avril 2026", data["due_date"])
	}

	// Publish a brief event and confirm bullets array round-trips.
	go func() {
		time.Sleep(50 * time.Millisecond)
		broker.Publish(events.NewBriefEvent(events.BriefPayload{
			Date:      "2026-04-30",
			ContentID: "brief:2026-04-30",
			Bullets:   []string{"Paiement TVA 7421.85 €", "Note ajoutée: contrat X"},
			ItemCount: 12,
		}))
	}()

	got, err = readSSELine(reader)
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	if got["type"] != "brief" {
		t.Errorf("type = %v, want brief", got["type"])
	}
	briefData, _ := got["data"].(map[string]any)
	bullets, ok := briefData["bullets"].([]any)
	if !ok || len(bullets) != 2 {
		t.Fatalf("bullets shape: got %v (%T)", briefData["bullets"], briefData["bullets"])
	}
	if bullets[0] != "Paiement TVA 7421.85 €" {
		t.Errorf("bullets[0] = %v", bullets[0])
	}
}

// readSSELine reads one full `data: {...}\n\n` event from the SSE stream
// and returns it as a parsed map.
func readSSELine(reader *bufio.Reader) (map[string]any, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, "data: ") {
			continue // skip blank lines / comments
		}
		payload := strings.TrimPrefix(line, "data: ")
		var out map[string]any
		if err := json.Unmarshal([]byte(payload), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}
