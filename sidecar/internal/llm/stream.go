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
