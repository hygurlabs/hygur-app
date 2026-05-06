package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SSEEvent represents a Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry int
}

// processSSEStream reads an SSE stream and calls the handler for each chunk.
func processSSEStream(r io.Reader, handler StreamHandler) error {
	reader := bufio.NewReader(r)
	var currentEvent SSEEvent
	var usage *Usage

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// Stream ended without [DONE] marker
				// This can happen if the connection is closed unexpectedly
				return nil
			}
			return fmt.Errorf("error reading stream: %w", err)
		}

		line = strings.TrimSpace(line)

		// Empty line marks end of an event
		if line == "" {
			if currentEvent.Data != "" {
				if err := processSSEEvent(currentEvent, handler, &usage); err != nil {
					return err
				}
				currentEvent = SSEEvent{}
			}
			continue
		}

		// Parse SSE field
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			currentEvent.Data = data
		} else if strings.HasPrefix(line, "event:") {
			currentEvent.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "id:") {
			currentEvent.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
		// Ignore retry and other fields
	}
}

// processSSEEvent handles a single SSE event.
func processSSEEvent(event SSEEvent, handler StreamHandler, usage **Usage) error {
	data := event.Data

	// Check for stream end marker
	if data == "[DONE]" {
		return handler("", true, *usage)
	}

	// Parse the JSON chunk
	var chunk ChatResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("failed to parse SSE data: %w", err)
	}

	// Extract delta content
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]

		// Check if this is the final chunk with usage
		if chunk.Usage != nil {
			*usage = chunk.Usage
		}

		// Check for finish reason
		if choice.FinishReason != "" && choice.FinishReason != "null" {
			// Some servers send finish_reason without [DONE]
			// We still continue processing in case there's more data
		}

		// Extract content from delta
		if choice.Delta != nil && choice.Delta.Content != "" {
			if err := handler(choice.Delta.Content, false, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

// StreamReader provides a streaming reader interface for SSE events.
type StreamReader struct {
	reader      *bufio.Reader
	done        bool
	lastUsage   *Usage
	accumulated strings.Builder
}

// NewStreamReader creates a new stream reader from an io.Reader.
func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{
		reader: bufio.NewReader(r),
	}
}

// Next reads the next delta from the stream.
// Returns the content delta, whether the stream is done, any usage info, and an error if one occurred.
func (sr *StreamReader) Next() (string, bool, *Usage, error) {
	if sr.done {
		return "", true, sr.lastUsage, nil
	}

	for {
		line, err := sr.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				sr.done = true
				return "", true, sr.lastUsage, nil
			}
			return "", false, nil, fmt.Errorf("error reading stream: %w", err)
		}

		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Only process data lines
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		// Check for stream end
		if data == "[DONE]" {
			sr.done = true
			return "", true, sr.lastUsage, nil
		}

		// Parse the chunk
		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", false, nil, fmt.Errorf("failed to parse SSE data: %w", err)
		}

		// Store usage if present
		if chunk.Usage != nil {
			sr.lastUsage = chunk.Usage
		}

		// Extract and return delta content
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			content := chunk.Choices[0].Delta.Content
			if content != "" {
				sr.accumulated.WriteString(content)
				return content, false, nil, nil
			}
		}
	}
}

// Accumulated returns all content accumulated so far.
func (sr *StreamReader) Accumulated() string {
	return sr.accumulated.String()
}

// IsDone returns whether the stream has finished.
func (sr *StreamReader) IsDone() bool {
	return sr.done
}

// Usage returns the token usage if available.
func (sr *StreamReader) Usage() *Usage {
	return sr.lastUsage
}

// StreamEvent is the rich payload emitted to a StreamRichHandler. Each event
// describes one observation from the SSE stream. Most fields are optional —
// callers branch on which is populated:
//
//   - Delta: a text fragment of the assistant's content
//   - ToolCallDeltas: zero or more partial tool-call fragments (model emits
//     these in pieces; assemble by Index, concatenating Function.Arguments)
//   - FinishReason: the model's stop reason on the final chunk ("stop",
//     "tool_calls", "length", …)
//   - Done: true exactly once, on the [DONE] sentinel
//   - Usage: token totals; only set when Done is true
type StreamEvent struct {
	Delta          string
	ToolCallDeltas []ToolCallDelta
	FinishReason   string
	Done           bool
	Usage          *Usage
}

// StreamRichHandler is the tool-aware streaming callback. Returning a non-nil
// error aborts the stream — the underlying HTTP body is closed by the caller.
type StreamRichHandler func(event StreamEvent) error

// processSSEStreamRich is the tool-aware variant of processSSEStream. It
// preserves the same SSE framing logic but emits StreamEvent so the caller
// can observe tool_calls and finish_reason without reparsing the protocol.
func processSSEStreamRich(r io.Reader, handler StreamRichHandler) error {
	reader := bufio.NewReader(r)
	var currentEvent SSEEvent
	var usage *Usage

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("error reading stream: %w", err)
		}

		line = strings.TrimSpace(line)

		if line == "" {
			if currentEvent.Data != "" {
				if err := processSSEEventRich(currentEvent, handler, &usage); err != nil {
					return err
				}
				currentEvent = SSEEvent{}
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			currentEvent.Data = data
		} else if strings.HasPrefix(line, "event:") {
			currentEvent.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "id:") {
			currentEvent.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
}

func processSSEEventRich(event SSEEvent, handler StreamRichHandler, usage **Usage) error {
	data := event.Data

	if data == "[DONE]" {
		return handler(StreamEvent{Done: true, Usage: *usage})
	}

	var chunk ChatResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("failed to parse SSE data: %w", err)
	}

	if chunk.Usage != nil {
		*usage = chunk.Usage
	}

	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]

	evt := StreamEvent{
		FinishReason: choice.FinishReason,
	}
	if choice.Delta != nil {
		evt.Delta = choice.Delta.Content
		evt.ToolCallDeltas = choice.Delta.ToolCalls
	}
	// Skip purely empty events — they happen on heartbeat/keepalive frames
	// and would just churn the handler. We always forward when there is
	// something to report, and the [DONE] branch above handles the terminal
	// signal separately.
	if evt.Delta == "" && len(evt.ToolCallDeltas) == 0 && evt.FinishReason == "" {
		return nil
	}
	return handler(evt)
}

// AssembleToolCalls merges streamed ToolCallDelta fragments into the final
// ToolCall list. Fragments are correlated by Index; arguments are
// concatenated in arrival order. Callers feed every observed delta from a
// single completion into one Assembler and read the result on FinishReason.
type ToolCallAssembler struct {
	byIndex map[int]*toolCallBuilder
	order   []int
}

type toolCallBuilder struct {
	id    string
	kind  string
	name  string
	args  strings.Builder
}

// NewToolCallAssembler returns a fresh assembler.
func NewToolCallAssembler() *ToolCallAssembler {
	return &ToolCallAssembler{byIndex: make(map[int]*toolCallBuilder)}
}

// Add ingests one streaming fragment. Safe to call with the empty/zero
// fragment — fields are merged only when non-empty so later chunks with just
// arguments don't overwrite the earlier id/name.
func (a *ToolCallAssembler) Add(delta ToolCallDelta) {
	b, ok := a.byIndex[delta.Index]
	if !ok {
		b = &toolCallBuilder{}
		a.byIndex[delta.Index] = b
		a.order = append(a.order, delta.Index)
	}
	if delta.ID != "" {
		b.id = delta.ID
	}
	if delta.Type != "" {
		b.kind = delta.Type
	}
	if delta.Function != nil {
		if delta.Function.Name != "" {
			b.name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			b.args.WriteString(delta.Function.Arguments)
		}
	}
}

// Finalize returns the assembled tool calls in the order their indices first
// appeared on the wire. Empty when no fragments were observed.
func (a *ToolCallAssembler) Finalize() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		b := a.byIndex[idx]
		kind := b.kind
		if kind == "" {
			kind = "function"
		}
		out = append(out, ToolCall{
			ID:   b.id,
			Type: kind,
			Function: ToolCallFunction{
				Name:      b.name,
				Arguments: b.args.String(),
			},
		})
	}
	return out
}

// Len returns the number of tool calls accumulated so far.
func (a *ToolCallAssembler) Len() int { return len(a.order) }
