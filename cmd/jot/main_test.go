package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jot(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, strings.NewReader(stdin), &out); err != nil {
		t.Fatalf("jot %v: %v", args, err)
	}
	return out.String()
}

func TestCLIFlow(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "jot"))
	chdir(t, t.TempDir())

	if out := jot(t, "", "init"); !strings.Contains(out, "global store ready") {
		t.Fatalf("init: %q", out)
	}
	out := jot(t, "", "add", "-k", "preference", "-t", "js", "Use pnpm, never npm")
	id := strings.Fields(out)[1]
	jot(t, "Deploy on merge to main\n", "add", "-k", "decision", "-")

	if out := jot(t, "", "search", "package", "manager", "npm"); !strings.Contains(out, id) {
		t.Fatalf("search: %q", out)
	}
	if out := jot(t, "", "ls", "-k", "decision"); !strings.Contains(out, "Deploy on merge") || strings.Contains(out, "pnpm") {
		t.Fatalf("ls filter: %q", out)
	}
	if out := jot(t, "", "show", id); !strings.Contains(out, "kind: preference") {
		t.Fatalf("show: %q", out)
	}
	jot(t, "", "rm", id)
	if out := jot(t, "", "search", "pnpm"); !strings.Contains(out, "nothing found") {
		t.Fatalf("after rm: %q", out)
	}
}

func TestCLIErrors(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "jot"))
	chdir(t, t.TempDir())
	for _, args := range [][]string{{"bogus"}, {"search"}, {"show", "missing"}, {"add", "-p", "x"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("jot %v: want error", args)
		}
	}
}

// chdir moves into dir for one test (t.Chdir needs Go 1.24).
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)

	out := jot(t, "", "setup", "--dry-run", "--command", "/bin/jot")
	if !strings.Contains(out, "Cursor          will connect") || !strings.Contains(out, "Claude Desktop  not installed") {
		t.Fatalf("dry run:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err == nil {
		t.Fatal("dry run wrote a file")
	}
	if out := jot(t, "n\n", "setup", "--command", "/bin/jot"); !strings.Contains(out, "Nothing changed") {
		t.Fatalf("declined:\n%s", out)
	}
	t.Setenv("JOT_HOME", filepath.Join(home, ".jot"))
	if out := jot(t, "y\n", "setup", "--command", "/bin/jot"); !strings.Contains(out, "Codex           connected") || !strings.Contains(out, "Memory store    created") {
		t.Fatalf("apply:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".jot")); err != nil {
		t.Fatal("setup did not create the store")
	}
	if out := jot(t, "", "setup", "--command", "/bin/jot"); !strings.Contains(out, "Nothing to change") {
		t.Fatalf("second run:\n%s", out)
	}
	if out := jot(t, "", "setup", "--undo", "cursor"); !strings.Contains(out, "Cursor          removed") {
		t.Fatalf("undo:\n%s", out)
	}
	if err := run([]string{"setup", "vscode"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("want error for unknown tool")
	}
}

func TestDupesAndMerge(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "jot"))
	chdir(t, t.TempDir())
	a := strings.Fields(jot(t, "", "add", "-t", "js", "Use pnpm for every JS project. Never commit package-lock.json."))[1]
	b := strings.Fields(jot(t, "", "add", "-t", "node", "Use pnpm for every JS project. Pin the version with the packageManager field in package.json."))[1]
	jot(t, "", "add", "Deploy the web app on Vercel.")

	out := jot(t, "", "dupes")
	if !strings.Contains(out, a) || !strings.Contains(out, b) || !strings.Contains(out, "1 possible duplicate") {
		t.Fatalf("dupes:\n%s", out)
	}
	if out := jot(t, "", "merge", "--dry-run", b, a); !strings.Contains(out, "Never commit") {
		t.Fatalf("dry merge:\n%s", out)
	}
	jot(t, "", "merge", b, a)
	out = jot(t, "", "show", b)
	if !strings.Contains(out, "Never commit") || !strings.Contains(out, "tags: [js, node]") {
		t.Fatalf("merged:\n%s", out)
	}
	if err := run([]string{"show", a}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("dropped memory still exists")
	}
	if out := jot(t, "", "dupes"); !strings.Contains(out, "no duplicates") {
		t.Fatalf("after merge:\n%s", out)
	}
}

func TestFirstRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JOT_HOME", filepath.Join(home, ".jot"))
	chdir(t, t.TempDir())
	out := jot(t, "")
	for _, want := range []string{"jot setup", "jot doctor", filepath.Join(home, ".jot"), "jot help"} {
		if !strings.Contains(out, want) {
			t.Errorf("first run missing %q:\n%s", want, out)
		}
	}
	jot(t, "", "init")
	if out := jot(t, ""); !strings.Contains(out, "Usage:") {
		t.Errorf("after init, want usage:\n%s", out)
	}
}
