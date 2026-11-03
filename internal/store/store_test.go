package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	m := &Memory{ID: "x1", Kind: KindPreference, Tags: []string{"go", "style"}, Source: "test",
		Created: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), Updated: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		Body: "Prefers table-driven tests.\n\nSecond paragraph."}
	got, err := Parse(m.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.Kind != m.Kind || got.Source != m.Source || got.Body != m.Body ||
		!got.Created.Equal(m.Created) || len(got.Tags) != 2 || got.Tags[0] != "go" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestParsePlainMarkdown(t *testing.T) {
	m, err := Parse([]byte("# Hand written\nsome note"))
	if err != nil || m.Kind != KindNote || m.Title() != "Hand written" {
		t.Fatalf("got %+v, %v", m, err)
	}
}

func TestStoreCRUD(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	s := Open(dir, "global")
	m, err := s.Add("Uses pnpm, never npm", KindPreference, []string{"JS", "js", " tooling "}, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != 2 {
		t.Fatalf("tags not normalized: %v", m.Tags)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); hasGit() && err != nil {
		t.Fatal("expected git repo")
	}
	if _, err := s.Update(m.ID, "Uses pnpm everywhere", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(m.ID)
	if err != nil || got.Body != "Uses pnpm everywhere" || got.Kind != KindPreference {
		t.Fatalf("update failed: %+v %v", got, err)
	}
	list, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("want 1, got %d", len(list))
	}
	if hasGit() {
		if log, err := s.Log(10); err != nil || log == "" {
			t.Fatalf("log: %q %v", log, err)
		}
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); err != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestRejectsBadIDs(t *testing.T) {
	s := Open(t.TempDir(), "global")
	for _, id := range []string{"", "../x", "a/b", ".git"} {
		if _, err := s.Get(id); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
	if _, err := s.Add("   ", "", nil, ""); err == nil {
		t.Error("empty body accepted")
	}
}

func TestFindProjectDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JOT_HOME", filepath.Join(root, "home", ".jot"))
	os.MkdirAll(filepath.Join(root, "repo", ".jot"), 0o755)
	os.MkdirAll(filepath.Join(root, "repo", "a", "b"), 0o755)
	got, ok := FindProjectDir(filepath.Join(root, "repo", "a", "b"))
	if !ok || got != filepath.Join(root, "repo", ".jot") {
		t.Fatalf("got %q %v", got, ok)
	}
	if len(Stores(filepath.Join(root, "repo"))) != 2 {
		t.Fatal("want project + global")
	}
}
