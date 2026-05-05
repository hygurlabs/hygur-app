// Package api provides the HTTP API server for the Hygur sidecar.
package api

import (
	"net/http"
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

// authMiddleware validates the authentication token from the request.
// Requests must include the X-Hygur-Token header with a valid token.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Hygur-Token")
		if token == "" {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing X-Hygur-Token header")
			return
		}

		if !auth.CompareTokens(token, s.token) {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeAuthError writes a JSON error response for authentication failures.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Using a simple format that matches the spec
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}

// corsMiddleware adds CORS headers for local development.
// Only allows requests from localhost origins.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Only allow localhost origins
		// http://localhost = 16 chars, http://localhost: = 17 chars
		// http://127.0.0.1 = 16 chars, http://127.0.0.1: = 17 chars
		allowed := origin == "http://localhost" || origin == "http://127.0.0.1"
		if !allowed && len(origin) > 17 {
			if len(origin) >= 17 && origin[:17] == "http://localhost:" {
				allowed = true
			} else if len(origin) >= 17 && origin[:17] == "http://127.0.0.1:" {
				allowed = true
			}
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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
