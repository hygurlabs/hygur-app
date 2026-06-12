package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManager_DefaultMapsToBasePath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "hygur.db")
	m := NewManager(base)
	defer m.Close()

	// Default and the "local" key resolve to the same base file and the same
	// cached handle — single-user behaviour is unchanged.
	def, err := m.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	local, err := m.For(DefaultIdentityKey)
	if err != nil {
		t.Fatalf("For(local): %v", err)
	}
	if def != local {
		t.Fatal("Default() and For(local) should return the same cached handle")
	}
	if got := m.pathFor(DefaultIdentityKey); got != base {
		t.Fatalf("default path = %q, want base %q", got, base)
	}
}

func TestManager_CachesPerKey(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "hygur.db"))
	defer m.Close()

	a1, err := m.For("alice")
	if err != nil {
		t.Fatalf("For(alice): %v", err)
	}
	a2, _ := m.For("alice")
	if a1 != a2 {
		t.Fatal("For(alice) should be cached and return the same handle")
	}
	b, _ := m.For("bob")
	if a1 == b {
		t.Fatal("different identities must get different handles")
	}
}

func TestManager_FileLevelIsolation(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "hygur.db"))
	defer m.Close()
	ctx := context.Background()

	alice, err := m.For("alice")
	if err != nil {
		t.Fatalf("For(alice): %v", err)
	}
	bob, err := m.For("bob")
	if err != nil {
		t.Fatalf("For(bob): %v", err)
	}

	// A row written to alice's database must not be visible in bob's.
	if err := alice.CreateTag(ctx, &Tag{Name: "private-to-alice"}); err != nil {
		t.Fatalf("create tag in alice: %v", err)
	}

	aliceTags, err := alice.ListTags(ctx)
	if err != nil {
		t.Fatalf("list alice: %v", err)
	}
	if len(aliceTags) != 1 {
		t.Fatalf("alice should have 1 tag, got %d", len(aliceTags))
	}

	bobTags, err := bob.ListTags(ctx)
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(bobTags) != 0 {
		t.Fatalf("bob must not see alice's data, got %d tags", len(bobTags))
	}

	// Per-identity files live under users/<key>.db, isolated from the base.
	if want := filepath.Join(dir, "users", "alice.db"); m.pathFor("alice") != want {
		t.Fatalf("alice path = %q, want %q", m.pathFor("alice"), want)
	}
}

func TestSanitizeKey(t *testing.T) {
	cases := map[string]string{
		"alice":            "alice",
		"u-1_2":            "u-1_2",
		"../etc/passwd":    "___etc_passwd",
		"a b/c":            "a_b_c",
		"user@example.com": "user_example_com",
	}
	for in, want := range cases {
		if got := sanitizeKey(in); got != want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}
