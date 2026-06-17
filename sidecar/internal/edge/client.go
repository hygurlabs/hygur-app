// Package edge is the Hygur edge agent (C7): the same Go binary in --mode=edge
// runs DEVICE-local connectors (Files first), extracts text locally, and PUSHES
// only the text to a central server via POST /knowledge/ingest-text with a device
// token. No KB, no LLM, no embeddings on the device — only text crosses the wire.
package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiVersion must satisfy the server's minimum (X-Hygur-API; mirrors the WebUI).
const apiVersion = "1"

// Client pushes pre-extracted text to a central Hygur server (a cloud tenant or a
// self-hosted server) using a device token.
type Client struct {
	base  string // e.g. https://cloud.hygur.ai
	token string // device JWT (Acc = tenant)
	http  *http.Client
}

// NewClient builds a push client for server (trailing slash trimmed) + token.
func NewClient(server, token string) *Client {
	return &Client{
		base:  strings.TrimRight(server, "/"),
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

// IngestText is the /knowledge/ingest-text request body (pre-extracted text).
type IngestText struct {
	Title      string         `json:"title"`
	Text       string         `json:"text"`
	SourceType string         `json:"source_type"`
	SourceRef  string         `json:"source_ref"` // idempotency key, e.g. "files:/abs/path"
	URL        string         `json:"url,omitempty"`
	Author     string         `json:"author,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"` // RFC3339; source date (e.g. mail date)
}

// PushText POSTs one document. Returns the server's status ("indexed"|"updated"|
// "duplicate"). Idempotent server-side by SourceRef.
func (c *Client) PushText(ctx context.Context, in IngestText) (string, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/knowledge/ingest-text", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hygur-Token", c.token)
	req.Header.Set("X-Hygur-API", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("ingest-text HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Status, nil
}

// ReconcileResult is the center's reconcile-pass outcome.
type ReconcileResult struct {
	Status   string `json:"status"`
	Recycled int    `json:"recycled"`
	Restored int    `json:"restored"`
	Purged   int    `json:"purged"`
}

// Reconcile posts the authoritative set of message refs currently present on the
// server for one provider; the center recycles KB items no longer present and
// restores any that reappeared. complete attests the enumeration was whole — the
// center only takes the destructive path when it is true. graceMisses defers the
// physical purge (0 → server default).
func (c *Client) Reconcile(ctx context.Context, provider string, seenRefs []string, complete bool, graceMisses int) (ReconcileResult, error) {
	body, err := json.Marshal(map[string]any{
		"provider":     provider,
		"seen_refs":    seenRefs,
		"complete":     complete,
		"grace_misses": graceMisses,
	})
	if err != nil {
		return ReconcileResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/knowledge/reconcile", bytes.NewReader(body))
	if err != nil {
		return ReconcileResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hygur-Token", c.token)
	req.Header.Set("X-Hygur-API", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return ReconcileResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return ReconcileResult{}, fmt.Errorf("reconcile HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out ReconcileResult
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, nil
}

// Health checks the server is reachable + the token works at the unauthenticated
// /health (cheap reachability probe before a sync).
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health HTTP %d", resp.StatusCode)
	}
	return nil
}
