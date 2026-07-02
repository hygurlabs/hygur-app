package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/llm"
	"github.com/rs/zerolog"
)

// fakePinger drives pingLLMAsync without a network.
type fakePinger struct {
	ok  bool
	err error
}

func (f fakePinger) Ping(context.Context) (bool, error) { return f.ok, f.err }

// WP21 — the boot-time LLM ping must never block the boot path and a failure must be a
// logged warn, not a fatal. Exercise the extracted helper directly.
func TestPingLLMAsync_FailureIsWarnNotBlock(t *testing.T) {
	cases := []struct {
		name   string
		pinger fakePinger
	}{
		{"ping error", fakePinger{ok: false, err: errors.New("dial tcp: connection refused")}},
		{"not available", fakePinger{ok: false, err: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := zerolog.New(&buf)

			done := make(chan struct{})
			go func() {
				pingLLMAsync(context.Background(), tc.pinger, "http://model-host:1234", logger)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("pingLLMAsync blocked — the boot path must not wait on the model host")
			}
			if out := buf.String(); !strings.Contains(out, `"level":"warn"`) {
				t.Errorf("expected a warn log on ping failure, got: %s", out)
			}
		})
	}
}

// WP21 — a real client pointed at an unreachable endpoint returns promptly (a logged
// warn), so wiring `go pingLLMAsync(...)` at boot can never stall the listener bind.
func TestPingLLMAsync_UnreachableEndpointReturnsPromptly(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	// Nothing is listening here → the ping's Do() fails fast (returns false, nil).
	client := llm.NewClientWithHTTP("http://127.0.0.1:1", 2*time.Second, 0, http.DefaultClient)

	start := time.Now()
	pingLLMAsync(context.Background(), client, "http://127.0.0.1:1", logger)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ping took %v — must not block boot", elapsed)
	}
	if out := buf.String(); !strings.Contains(out, `"level":"warn"`) {
		t.Errorf("expected a warn on an unreachable endpoint, got: %s", out)
	}
}
