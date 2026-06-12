package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebUIPublicServesIcons verifies every root static asset (favicon,
// home-screen icons, PWA manifest) is embedded in the build and served with a
// 200 and a content type — i.e. "Add to Home Screen" and the favicon resolve
// instead of 404ing. This guards the build (public/ copied into dist) and the
// serving path together.
func TestWebUIPublicServesIcons(t *testing.T) {
	pub := webUIPublic()
	for _, path := range webUIPublicFiles {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			pub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: status = %d, want 200 (is public/ built into dist?)", path, rec.Code)
			}
			if rec.Body.Len() == 0 {
				t.Fatalf("GET %s: empty body", path)
			}
			if ct := rec.Header().Get("Content-Type"); ct == "" {
				t.Fatalf("GET %s: missing Content-Type", path)
			}
			if strings.HasSuffix(path, ".webmanifest") {
				if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
					t.Fatalf("GET %s: Content-Type = %q, want application/manifest+json", path, ct)
				}
			}
		})
	}
}

// TestWebUIManifestReferencesServeableIcons parses the manifest and confirms
// every icon it points at is itself served — so an installed PWA never resolves
// a broken icon URL.
func TestWebUIManifestReferencesServeableIcons(t *testing.T) {
	pub := webUIPublic()

	rec := httptest.NewRecorder()
	pub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)

	var manifest struct {
		Icons []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(manifest.Icons) == 0 {
		t.Fatal("manifest declares no icons")
	}
	for _, icon := range manifest.Icons {
		r := httptest.NewRecorder()
		pub.ServeHTTP(r, httptest.NewRequest(http.MethodGet, icon.Src, nil))
		if r.Code != http.StatusOK {
			t.Errorf("manifest icon %s: status = %d, want 200", icon.Src, r.Code)
		}
	}
}
