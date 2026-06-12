package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

type fakeKeyStore struct{ key string }

func (f *fakeKeyStore) DBKey() (string, bool) {
	if f.key == "" {
		return "", false
	}
	return f.key, true
}
func (f *fakeKeyStore) SetDBKey(k string) error { f.key = k; return nil }

func TestEncryptionHandler_EnableThenStatus(t *testing.T) {
	ks := &fakeKeyStore{}
	h := NewEncryptionHandler(ks, false, zerolog.Nop())
	router := chi.NewRouter()
	router.Get("/admin/db/encryption", h.Status)
	router.Post("/admin/db/encrypt", h.Enable)

	get := func() map[string]any {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/db/encryption", nil))
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}
	post := func() map[string]any {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/db/encrypt", nil))
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m
	}

	if st := get(); st["enabled"] != false {
		t.Fatalf("expected disabled initially, got %v", st)
	}

	en := post()
	if en["status"] != "staged" || en["restart_required"] != true {
		t.Fatalf("enable should stage + require restart, got %v", en)
	}
	if len(ks.key) != 64 { // 32 bytes hex
		t.Errorf("expected a 32-byte hex key (64 chars), got %d", len(ks.key))
	}

	if st := get(); st["enabled"] != true {
		t.Errorf("expected enabled after enable, got %v", st)
	}

	prev := ks.key
	if en := post(); en["status"] != "already_enabled" {
		t.Errorf("re-enable should be a no-op, got %v", en)
	}
	if ks.key != prev {
		t.Errorf("re-enable must not rotate the key")
	}
}

func TestEncryptionHandler_EnvManaged(t *testing.T) {
	ks := &fakeKeyStore{}
	h := NewEncryptionHandler(ks, true, zerolog.Nop())
	router := chi.NewRouter()
	router.Get("/admin/db/encryption", h.Status)
	router.Post("/admin/db/encrypt", h.Enable)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/db/encryption", nil))
	var st map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["enabled"] != true || st["env_managed"] != true {
		t.Fatalf("env-managed should report enabled + env_managed, got %v", st)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/db/encrypt", nil))
	if ks.key != "" {
		t.Errorf("env-managed enable must not write a local key")
	}
}
