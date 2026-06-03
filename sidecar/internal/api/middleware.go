// Package api provides the HTTP API server for the Hygur sidecar.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/hygur/sidecar/internal/auth"
	"github.com/rs/zerolog"
)

// loggerMiddleware creates a middleware that logs each request using zerolog.
// Output format: {"level":"info","method":"GET","path":"/health","status":200,"duration_ms":5}
func (s *Server) loggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the response writer to capture the status code
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		// Process request
		next.ServeHTTP(ww, r)

		// Calculate duration in milliseconds
		duration := time.Since(start)
		durationMs := float64(duration.Nanoseconds()) / 1e6

		// Get request ID if present
		requestID := middleware.GetReqID(r.Context())

		// Log the request
		event := s.logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Float64("duration_ms", durationMs).
			Int("bytes", ww.BytesWritten())

		if requestID != "" {
			event.Str("request_id", requestID)
		}

		if r.URL.RawQuery != "" {
			event.Str("query", r.URL.RawQuery)
		}

		// Log errors with additional context
		if ww.Status() >= 500 {
			event.Str("remote_addr", r.RemoteAddr)
		}

		event.Msg("request")
	})
}

// authMiddleware authenticates the request via the configured Authenticator
// (loopback single-token in local mode, per-device JWT in remote mode) and
// attaches the resolved Identity to the request context for downstream handlers
// and the per-identity store layer (P1.3).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.authenticator.Authenticate(r)
		if err != nil {
			// Preserve the established client-facing messages.
			if errors.Is(err, auth.ErrMissingToken) {
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-Hygur-Token header")
			} else {
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid token")
			}
			return
		}

		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
	})
}

// writeAuthError writes a JSON error response for authentication failures.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

// corsMiddleware adds CORS headers for local development.
// Only allows requests from localhost origins.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Allow loopback origins (the sidecar-served UI and vite dev) plus the
		// Tauri desktop shell, which serves its bundled UI from a custom scheme
		// (tauri://localhost on Apple, https://tauri.localhost on Windows) and
		// therefore calls this API cross-origin.
		allowed := origin == "http://localhost" || origin == "http://127.0.0.1" ||
			origin == "tauri://localhost" || origin == "https://tauri.localhost" ||
			strings.HasPrefix(origin, "http://localhost:") ||
			strings.HasPrefix(origin, "http://127.0.0.1:")

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Hygur-Token, X-Hygur-API")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		// Handle preflight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// recovererWithLogger creates a custom panic recoverer that logs using zerolog.
func (s *Server) recovererWithLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				requestID := middleware.GetReqID(r.Context())

				s.logger.Error().
					Str("request_id", requestID).
					Str("method", r.Method).
					Str("path", r.URL.Path).
					Interface("panic", rec).
					Msg("panic recovered")

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// requestLogger returns a zerolog logger enriched with request context.
func requestLogger(logger zerolog.Logger, r *http.Request) zerolog.Logger {
	requestID := middleware.GetReqID(r.Context())
	return logger.With().
		Str("request_id", requestID).
		Str("method", r.Method).
		Str("path", r.URL.Path).
		Logger()
}
