package api

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hygur/sidecar/internal/config"
	"github.com/rs/zerolog"
)

// recordingUpstream stands in for the cloud tenant: it records the path + the
// X-Hygur-Token / Host it received, and answers 200.
func recordingUpstream(t *testing.T) (*httptest.Server, *upstreamHit) {
	t.Helper()
	hit := &upstreamHit{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.mu.Lock()
		hit.count++
		hit.path = r.URL.Path
		hit.rawQuery = r.URL.RawQuery
		hit.token = r.Header.Get("X-Hygur-Token")
		hit.host = r.Host
		hit.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv, hit
}

type upstreamHit struct {
	mu       sync.Mutex
	count    int
	path     string
	rawQuery string
	token    string
	host     string
}

func newCloudServer(t *testing.T, upstream, token string) *Server {
	t.Helper()
	s := NewServer(&config.Config{}, zerolog.Nop(), "local-tok")
	if err := s.SetCloudProxy(upstream, token); err != nil {
		t.Fatalf("SetCloudProxy: %v", err)
	}
	return s
}

func TestCloudProxy_ForwardsDataRouteWithToken(t *testing.T) {
	up, hit := recordingUpstream(t)
	front := httptest.NewServer(newCloudServer(t, up.URL, "device-jwt").Router())
	t.Cleanup(front.Close)

	// A data route the SPA would call — must reach the upstream, with the device
	// token injected and the query string preserved (the purge relied on this).
	resp, err := http.Get(front.URL + "/knowledge/items?limit=200&source_type=file")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	hit.mu.Lock()
	defer hit.mu.Unlock()
	if hit.count != 1 {
		t.Fatalf("upstream hit %d times, want 1", hit.count)
	}
	if hit.path != "/knowledge/items" {
		t.Errorf("upstream path = %q, want /knowledge/items", hit.path)
	}
	if hit.rawQuery != "limit=200&source_type=file" {
		t.Errorf("upstream query = %q, want preserved", hit.rawQuery)
	}
	if hit.token != "device-jwt" {
		t.Errorf("upstream X-Hygur-Token = %q, want device-jwt", hit.token)
	}
	// Host must be the tenant's, not the loopback front (tenant vhost + Host-guard).
	if hit.host != strings.TrimPrefix(up.URL, "http://") {
		t.Errorf("upstream Host = %q, want %q", hit.host, strings.TrimPrefix(up.URL, "http://"))
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body not relayed from upstream: %q", body)
	}
}

func TestCloudProxy_KeepsLocalRoutes(t *testing.T) {
	up, hit := recordingUpstream(t)
	front := httptest.NewServer(newCloudServer(t, up.URL, "device-jwt").Router())
	t.Cleanup(front.Close)

	// /health and /version are local liveness of THIS sidecar — never proxied.
	for _, p := range []string{"/health", "/version"} {
		resp, err := http.Get(front.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (served locally)", p, resp.StatusCode)
		}
	}
	hit.mu.Lock()
	defer hit.mu.Unlock()
	if hit.count != 0 {
		t.Errorf("local routes hit the upstream %d times, want 0", hit.count)
	}
}

func TestCloudProxy_DisabledIsLocal(t *testing.T) {
	// Empty upstream → local mode: /version is served by this sidecar.
	front := httptest.NewServer(newCloudServer(t, "", "").Router())
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /version = %d, want 200", resp.StatusCode)
	}
}

func TestCloudProxy_UpstreamDownReturns502(t *testing.T) {
	// Point at a closed port: the proxy's ErrorHandler must answer 502, not hang
	// or 500. (Reserve a port via a listener we immediately close.)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing listens there now

	front := httptest.NewServer(newCloudServer(t, deadURL, "device-jwt").Router())
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/knowledge/items")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

// TestCloudProxy_StreamsSSE proves the proxy flushes incrementally (FlushInterval
// = -1) rather than buffering the whole response: the upstream writes the first
// SSE chunk, then BLOCKS until the test has read it, then writes the second.
// If the proxy buffered, reading the first chunk would deadlock.
func TestCloudProxy_StreamsSSE(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream ResponseWriter is not a Flusher")
			return
		}
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-release // block until the test confirms it received the first chunk
		_, _ = io.WriteString(w, "data: second\n\n")
		fl.Flush()
	}))
	t.Cleanup(up.Close)

	front := httptest.NewServer(newCloudServer(t, up.URL, "device-jwt").Router())
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream (header passthrough)", ct)
	}

	br := bufio.NewReader(resp.Body)
	first := readChunk(t, br, "first chunk")
	if !strings.Contains(first, "first") {
		t.Fatalf("first chunk = %q, want it to contain 'first'", first)
	}
	close(release) // upstream now writes the second chunk
	second := readChunk(t, br, "second chunk")
	if !strings.Contains(second, "second") {
		t.Fatalf("second chunk = %q, want it to contain 'second'", second)
	}
}

// readChunk reads the next non-blank SSE line (skipping the blank frame
// separators) with a deadline so a buffering regression fails fast instead of
// hanging the suite.
func readChunk(t *testing.T, br *bufio.Reader, what string) string {
	t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "" {
				ch <- res{line, err}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading %s: %v", what, r.err)
		}
		return r.line
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out reading %s — proxy is buffering, not streaming", what)
		return ""
	}
}
