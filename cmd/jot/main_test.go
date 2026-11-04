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
