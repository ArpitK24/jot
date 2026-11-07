package store

import (
	"os"
	"path/filepath"
	"strings"
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

func TestMerge(t *testing.T) {
	s := Open(t.TempDir(), "global")
	a, err := s.Add("Use pnpm", KindPreference, []string{"js"}, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Add("Use pnpm, pin it in package.json", KindPreference, []string{"node"}, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(a.ID, a.ID, "", nil); err == nil {
		t.Fatal("self merge should fail")
	}
	m, err := s.Merge(b.ID, a.ID, "Use pnpm and pin it", []string{"js", "node"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Body != "Use pnpm and pin it" || len(m.Tags) != 2 {
		t.Fatalf("merged = %+v", m)
	}
	if _, err := s.Get(a.ID); err != ErrNotFound {
		t.Fatalf("dropped memory still there: %v", err)
	}
	if _, err := s.Merge(b.ID, "missing", "", nil); err != ErrNotFound {
		t.Fatalf("missing drop: %v", err)
	}
}

func TestHistory(t *testing.T) {
	if !hasGit() {
		t.Skip("git not installed")
	}
	s := Open(filepath.Join(t.TempDir(), "mem"), "global")
	a, _ := s.Add("first memory body", KindNote, nil, "cli")
	b, _ := s.Add("second memory body", KindNote, nil, "cli")
	if _, err := s.Update(a.ID, "first memory, edited", "", nil); err != nil {
		t.Fatal(err)
	}
	h, err := s.History(a.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].Hash == "" || h[0].Date.IsZero() {
		t.Fatalf("want 2 changes for a, got %+v", h)
	}
	if h, _ := s.History(b.ID, 10); len(h) != 1 {
		t.Fatalf("want 1 change for b, got %+v", h)
	}
	if _, err := s.History("../x", 10); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestSubfolderFilesAreNotMemories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	s := Open(dir, "global")
	if _, err := s.Add("A real note", KindNote, nil, "cli"); err != nil {
		t.Fatal(err)
	}
	chat := []byte("---\nid: c1\ntitle: a chat\n---\n\n### You\n\nhello\n")
	if err := s.WriteFile("chats/c1.md", chat, "ask c1"); err != nil {
		t.Fatal(err)
	}
	ms, err := s.List()
	if err != nil || len(ms) != 1 || ms[0].Body != "A real note" {
		t.Fatalf("List saw chat files: %+v %v", ms, err)
	}
	if _, err := s.Get("chats/c1"); err == nil {
		t.Fatal("Get reached into a subfolder")
	}
	got, err := s.ReadFile("chats/c1.md")
	if err != nil || string(got) != string(chat) {
		t.Fatalf("ReadFile: %q %v", got, err)
	}
	if hasGit() {
		if log, _ := s.Log(5); !strings.Contains(log, "ask c1") {
			t.Fatalf("chat write not committed: %s", log)
		}
	}
	for _, bad := range []string{"", "c1.md", "../x/c1.md", "chats/../c1.md", ".git/config", "chats/.hidden", "/etc/passwd", "chats//"} {
		if err := s.WriteFile(bad, []byte("x"), "bad"); err == nil {
			t.Errorf("WriteFile(%q) allowed", bad)
		}
	}
	if err := s.RemoveFile("chats/c1.md", "rm c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFile("chats/c1.md", "rm c1"); err != ErrNotFound {
		t.Fatalf("second remove: %v", err)
	}
}

func TestNewID(t *testing.T) {
	id := NewID(time.Date(2026, 11, 8, 1, 2, 3, 0, time.FixedZone("IST", 19800)))
	if !strings.HasPrefix(id, "20261107-193203-") || len(id) != len("20261107-193203-abcd") {
		t.Fatalf("id %q", id)
	}
}
