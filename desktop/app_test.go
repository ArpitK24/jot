package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("JOT_HOME", filepath.Join(home, ".jot"))
	cli := filepath.Join(home, "bin", "jot")
	a := NewApp(filepath.Join(home, "prefs.json"))
	a.cliPath = func() string { return cli }
	a.opener = func(string) error { return nil }
	return a, home
}

func TestNotesLifecycle(t *testing.T) {
	a, _ := testApp(t)
	n, err := a.Create("global", "Use pnpm for every JS project", "preference", []string{"js"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Source != "desktop" || n.Title == "" || n.Store != "global" {
		t.Fatalf("bad note: %+v", n)
	}
	if _, err := a.Create("global", "   ", "note", nil); err == nil {
		t.Fatal("empty note accepted")
	}
	n, err = a.SetPinned("global", n.ID, true)
	if err != nil || !n.Pinned {
		t.Fatalf("pin: %+v %v", n, err)
	}
	n, err = a.SetPinned("global", n.ID, false)
	if err != nil || n.Pinned || len(n.Tags) != 1 {
		t.Fatalf("unpin: %+v %v", n, err)
	}
	n, err = a.Save("global", n.ID, "Use pnpm everywhere", "preference", nil)
	if err != nil || n.Body != "Use pnpm everywhere" || len(n.Tags) != 0 {
		t.Fatalf("save: %+v %v", n, err)
	}
	if hits := a.Search("pnpm"); len(hits) != 1 || hits[0].ID != n.ID {
		t.Fatalf("search: %+v", hits)
	}
	notes, _ := a.Notes()
	if len(notes) != 1 {
		t.Fatalf("want 1 note, got %d", len(notes))
	}
	if err := a.Delete("global", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.find("nope"); err == nil {
		t.Fatal("unknown store accepted")
	}
}

func TestProjectsAndDuplicates(t *testing.T) {
	a, home := testApp(t)
	proj := filepath.Join(home, "code", "forge")
	os.MkdirAll(proj, 0o755)
	if err := a.AddProjectDir(proj); err != nil {
		t.Fatal(err)
	}
	a.AddProjectDir(proj)
	if len(a.prefs.Projects) != 1 {
		t.Fatalf("project added twice: %v", a.prefs.Projects)
	}
	if _, err := os.Stat(filepath.Join(proj, ".jot")); err != nil {
		t.Fatal("project store not created")
	}
	x, _ := a.Create(proj, "Every API error returns problem json with a stable type URI", "decision", nil)
	y, _ := a.Create(proj, "Every API error returns problem json with a stable type URI and field errors", "decision", nil)
	pairs := a.Duplicates()
	if len(pairs) != 1 {
		t.Fatalf("want 1 pair, got %d", len(pairs))
	}
	if !strings.Contains(pairs[0].MergedBody, "stable type URI") {
		t.Fatalf("merged body lost text: %q", pairs[0].MergedBody)
	}
	if err := a.KeepBoth(x.ID, y.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.Duplicates()) != 0 {
		t.Fatal("dismissed pair came back")
	}
	again := loadPrefs(a.prefs.path)
	if len(again.Dismissed) != 1 || len(again.Projects) != 1 {
		t.Fatalf("prefs not saved: %+v", again)
	}
	p := pairs[0]
	if _, err := a.Merge(proj, p.Keep.ID, p.Drop.ID, p.MergedBody, p.MergedTags); err != nil {
		t.Fatal(err)
	}
	notes, _ := a.Notes()
	if len(notes) != 1 || notes[0].Scope != "forge" {
		t.Fatalf("after merge: %+v", notes)
	}
	a.RemoveProject(proj)
	if notes, _ := a.Notes(); len(notes) != 0 {
		t.Fatal("removed project still listed")
	}
}

func TestToolsConnect(t *testing.T) {
	a, home := testApp(t)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	var cursor Tool
	for _, tl := range a.Tools() {
		if tl.ID == "cursor" {
			cursor = tl
		}
	}
	if cursor.State != "absent" || !strings.Contains(cursor.Snippet, "jot") {
		t.Fatalf("cursor before: %+v", cursor)
	}
	tools, err := a.Connect([]string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.ID == "cursor" && tl.State != "connected" {
			t.Fatalf("cursor after: %+v", tl)
		}
	}
	if s := a.State(); s.Connected != 1 || s.Onboarded {
		t.Fatalf("state: %+v", s)
	}
	if _, err := a.Disconnect("cursor"); err != nil {
		t.Fatal(err)
	}
	if s := a.State(); s.Connected != 0 {
		t.Fatalf("still connected: %+v", s)
	}
	a.FinishOnboarding()
	if !loadPrefs(a.prefs.path).Onboarded {
		t.Fatal("onboarding not saved")
	}
}
