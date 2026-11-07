package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain lets the test binary stand in for a real jot: doctor starts
// `<binary> serve`, and with JOT_BE_JOT set this process acts as jot.
func TestMain(m *testing.M) {
	switch os.Getenv("JOT_BE_JOT") {
	case "1":
		if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jot:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "broken":
		fmt.Fprintln(os.Stderr, "jot: store is on fire")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	t.Setenv("JOT_HOME", filepath.Join(home, ".jot"))
	t.Setenv("JOT_EMBED", "off")
	chdir(t, t.TempDir())
	return home
}

func doctor(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"doctor"}, args...), strings.NewReader(""), &out)
	return out.String(), err
}

func TestDoctorFreshMachine(t *testing.T) {
	fakeHome(t)
	out, err := doctor(t, "--no-serve")
	if err != nil {
		t.Fatalf("a fresh machine has nothing broken yet, got %v:\n%s", err, out)
	}
	for _, want := range []string{"! global store", "not created yet", "none of the tools jot knows is installed", "semantic search  off"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDoctorConnectedAndBroken(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("JOT_BE_JOT", "1")
	me := self()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	jot(t, "", "init")
	jot(t, "", "setup", "-y", "--command", me)

	out, err := doctor(t)
	if err != nil {
		t.Fatalf("want all good, got %v:\n%s", err, out)
	}
	for _, want := range []string{"✓ Claude Code", "jot block in", "✓ Cursor", "answers initialize and tools/list", "5 tools", "All good."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	// An outdated instructions block, a command that no longer exists, and
	// a config that isn't valid JSON each get a fix line.
	os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("my rules\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"),
		[]byte(`{"mcpServers":{"jot":{"command":"/no/such/jot","args":["serve"]}}}`), 0o644)
	out, err = doctor(t, "--no-serve")
	if err == nil {
		t.Fatalf("want failure:\n%s", out)
	}
	for _, want := range []string{"jot block missing or outdated", "fix: jot setup claude-code", `command "/no/such/jot"`, "fix: jot setup cursor", "2 problems to fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{"mcpServers":`), 0o644)
	out, _ = doctor(t, "--no-serve")
	if !strings.Contains(out, "fix the JSON/TOML syntax") {
		t.Errorf("broken JSON not reported:\n%s", out)
	}
}

func TestDoctorServeFailure(t *testing.T) {
	fakeHome(t)
	t.Setenv("JOT_BE_JOT", "broken")
	c := serveCheck(self())
	if c.mark != markFail || !strings.Contains(c.detail, "store is on fire") || c.fix == "" {
		t.Fatalf("got %+v", c)
	}
}

func TestDoctorEmbedUnreachable(t *testing.T) {
	fakeHome(t)
	t.Setenv("JOT_EMBED", "on")
	t.Setenv("JOT_OLLAMA_URL", "http://127.0.0.1:1")
	c := embedCheck()
	if c.mark != markFail || !strings.Contains(c.fix, "jot embed off") {
		t.Fatalf("got %+v", c)
	}
}
