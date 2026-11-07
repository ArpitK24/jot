package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArpitK24/jot/internal/embed/embedtest"
)

func TestEmbedFlow(t *testing.T) {
	d := t.TempDir()
	t.Setenv("JOT_HOME", filepath.Join(d, "jot"))
	t.Setenv("XDG_CACHE_HOME", d)
	t.Setenv("LocalAppData", d)
	t.Setenv("JOT_EMBED", "")
	chdir(t, t.TempDir())
	srv := embedtest.Server(nil)
	defer srv.Close()

	jot(t, "", "init")
	jot(t, "", "add", "Login uses a JWT token.")
	jot(t, "", "add", "Package manager is pnpm, not npm.")
	if out := jot(t, "", "embed"); !strings.Contains(out, "off") {
		t.Fatalf("status: %q", out)
	}
	if out := jot(t, "", "search", "how do users sign in"); !strings.Contains(out, "nothing found") {
		t.Fatalf("keyword search should miss: %q", out)
	}
	if out := jot(t, "", "embed", "on", "--url", srv.URL); !strings.Contains(out, "indexed 2 memories") {
		t.Fatalf("embed on: %q", out)
	}
	if out := jot(t, "", "search", "how do users sign in"); !strings.Contains(out, "JWT") {
		t.Fatalf("semantic search: %q", out)
	}
	jot(t, "", "add", "Users sign in with an auth session.")
	if out := jot(t, "", "dupes"); !strings.Contains(out, "same meaning") {
		t.Fatalf("dupes: %q", out)
	}
	if out := jot(t, "", "where"); !strings.Contains(out, "semantic") {
		t.Fatalf("where: %q", out)
	}
	if out := jot(t, "", "embed", "off"); !strings.Contains(out, "off") {
		t.Fatalf("off: %q", out)
	}
	if out := jot(t, "", "search", "authentication flow"); !strings.Contains(out, "nothing found") {
		t.Fatalf("off should mean keyword only: %q", out)
	}
}
