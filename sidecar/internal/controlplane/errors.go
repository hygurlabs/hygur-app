package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ClientError is a first-party, cookieless client error report (web/desktop) —
// captured by the app's error boundary + global handlers and POSTed to the
// console, so the operator sees crashes without any third-party tracking SDK.
type ClientError struct {
	ID         int64  `json:"id"`
	OccurredAt string `json:"occurred_at"`
	Message    string `json:"message"`
	Stack      string `json:"stack,omitempty"`
	URL        string `json:"url,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	Origin     string `json:"origin,omitempty"`
}

const clientErrorsKeep = 1000 // ring-buffer cap on stored reports

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// InsertClientError stores one report (truncating fields) and prunes to the cap.
func (s *Store) InsertClientError(e ClientError) error {
	if _, err := s.db.Exec(
		`INSERT INTO client_errors (occurred_at, message, stack, url, app_version, user_agent, origin) VALUES (?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(rfc), trunc(e.Message, 2000), trunc(e.Stack, 8000),
		trunc(e.URL, 500), trunc(e.AppVersion, 100), trunc(e.UserAgent, 300), trunc(e.Origin, 200),
	); err != nil {
		return err
	}
	_, _ = s.db.Exec(
		`DELETE FROM client_errors WHERE id NOT IN (SELECT id FROM client_errors ORDER BY id DESC LIMIT ?)`,
		clientErrorsKeep)
	return nil
}

// ListRecentErrors returns the most recent reports, newest first.
func (s *Store) ListRecentErrors(limit int) ([]ClientError, error) {
	rows, err := s.db.Query(
		`SELECT id, occurred_at, message, stack, url, app_version, user_agent, origin
		 FROM client_errors ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientError{}
	for rows.Next() {
		var e ClientError
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Message, &e.Stack, &e.URL, &e.AppVersion, &e.UserAgent, &e.Origin); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RegisterErrorIngest mounts the PUBLIC, cookieless ingest endpoint. No auth: the
// app reports anonymously; the body is size-capped and fields truncated, and the
// store rings at clientErrorsKeep. Malformed bodies are swallowed (204) so a
// reporting bug never turns into client-side error noise.
func RegisterErrorIngest(r chi.Router, store *Store) {
	limiter := newRateLimiter(30, time.Minute) // 30 reports / minute / IP
	r.Post("/errors", func(w http.ResponseWriter, req *http.Request) {
		if !limiter.allow(clientIP(req)) {
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 16*1024)
		var body struct {
			Message    string `json:"message"`
			Stack      string `json:"stack"`
			URL        string `json:"url"`
			AppVersion string `json:"app_version"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = store.InsertClientError(ClientError{
			Message: body.Message, Stack: body.Stack, URL: body.URL,
			AppVersion: body.AppVersion, UserAgent: req.UserAgent(), Origin: req.Header.Get("Origin"),
		})
		w.WriteHeader(http.StatusNoContent)
	})
}
