package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hygur/sidecar/internal/config"
	"github.com/hygur/sidecar/internal/version"
	"github.com/rs/zerolog"
)

func newVersionTestServer() *Server {
	return NewServer(&config.Config{}, zerolog.Nop(), "tok")
}

func TestAPIVersionMiddleware(t *testing.T) {
	srv := newVersionTestServer()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := srv.apiVersionMiddleware(next)

	serverV := strconv.Itoa(version.APIVersion)

	cases := []struct {
		name       string
		clientV    string // X-Hygur-API request header ("" = absent)
		wantStatus int
	}{
		{"absent header passes", "", http.StatusOK},
		{"current version passes", serverV, http.StatusOK},
		{"newer client passes", strconv.Itoa(version.APIVersion + 1), http.StatusOK},
		{"too-old client rejected", strconv.Itoa(version.MinClientAPIVersion - 1), http.StatusUpgradeRequired},
		{"malformed header ignored", "not-a-number", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.clientV != "" {
				req.Header.Set(APIVersionHeader, tc.clientV)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			// The server always advertises its API version on the response.
			if got := rec.Header().Get(APIVersionHeader); got != serverV {
				t.Fatalf("response %s = %q, want %q", APIVersionHeader, got, serverV)
			}
			if tc.wantStatus == http.StatusUpgradeRequired {
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode 426 body: %v", err)
				}
				if body["code"] != "CLIENT_TOO_OLD" {
					t.Fatalf("426 body code = %v, want CLIENT_TOO_OLD", body["code"])
				}
			}
		})
	}
}

func TestHandleVersion(t *testing.T) {
	srv := newVersionTestServer()
	rec := httptest.NewRecorder()
	srv.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// JSON numbers decode to float64.
	if int(body["api"].(float64)) != version.APIVersion {
		t.Fatalf("api = %v, want %d", body["api"], version.APIVersion)
	}
	if body["app"] != version.Version {
		t.Fatalf("app = %v, want %q", body["app"], version.Version)
	}
}
