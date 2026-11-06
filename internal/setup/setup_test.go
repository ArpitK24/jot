package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func client(t *testing.T, f Format, name, content string) Client {
	t.Helper()
	dir := t.TempDir()
	c := Client{ID: "test", Name: "Test", Config: filepath.Join(dir, name), Detect: dir, Format: f}
	if content != "" {
		if err := os.WriteFile(c.Config, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func state(t *testing.T, c Client, cmd string) State {
	t.Helper()
	p, err := Check(c, cmd)
	if err != nil {
		t.Fatal(err)
	}
	return p.State
}

func TestJSONKeepsOtherKeysAndOrder(t *testing.T) {
	orig := `{"zeta": 1, "mcpServers": {"other": {"command": "x"}}, "alpha": {"keep": [1, 2]}}`
	c := client(t, JSON, "mcp.json", orig)

	if s := state(t, c, "/bin/jot"); s != Absent {
		t.Fatalf("state = %v", s)
	}
	p, _ := Check(c, "/bin/jot")
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, c.Config)
	for _, want := range []string{`"other"`, `"keep"`, `"JOT_SOURCE": "test"`, `"/bin/jot"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Index(got, "zeta") > strings.Index(got, "alpha") {
		t.Errorf("key order changed:\n%s", got)
	}
	if read(t, c.Config+".jot-backup") != orig {
		t.Error("backup does not match the original")
	}
	if s := state(t, c, "/bin/jot"); s != Connected {
		t.Fatalf("after apply state = %v", s)
	}
	if s := state(t, c, "/other/jot"); s != Different {
		t.Fatalf("other command state = %v", s)
	}

	removed, err := Remove(c)
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	got = read(t, c.Config)
	if strings.Contains(got, "JOT_SOURCE") || !strings.Contains(got, `"other"`) {
		t.Errorf("after remove:\n%s", got)
	}
	if removed, _ := Remove(c); removed {
		t.Error("second remove should find nothing")
	}
}

func TestJSONCreatesFile(t *testing.T) {
	c := client(t, JSON, filepath.Join("config", "mcp_config.json"), "")
	p, _ := Check(c, "jot")
	if p.State != Absent {
		t.Fatalf("state = %v", p.State)
	}
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Config + ".jot-backup"); err == nil {
		t.Error("no backup expected for a new file")
	}
	if s := state(t, c, "jot"); s != Connected {
		t.Fatalf("state = %v", s)
	}
}

func TestJSONRejectsBadFile(t *testing.T) {
	c := client(t, JSON, "mcp.json", `{"mcpServers": [}`)
	if _, err := Check(c, "jot"); err == nil {
		t.Fatal("want error for broken JSON")
	}
}

func TestTOML(t *testing.T) {
	orig := "model = \"o3\"\n\n[mcp_servers.other]\ncommand = \"x\"\n\n[mcp_servers.jot]\ncommand = \"old\"\n\n[mcp_servers.jot.env]\nA = \"1\"\n\n[profiles.fast]\nmodel = \"mini\"\n"
	c := client(t, TOML, "config.toml", orig)
	if s := state(t, c, `C:\bin\jot.exe`); s != Different {
		t.Fatalf("state = %v", s)
	}
	p, _ := Check(c, `C:\bin\jot.exe`)
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, c.Config)
	if strings.Count(got, "[mcp_servers.jot]") != 1 || strings.Contains(got, `"old"`) {
		t.Fatalf("old block not replaced:\n%s", got)
	}
	for _, want := range []string{`command = "C:\\bin\\jot.exe"`, "[profiles.fast]", "[mcp_servers.other]", `JOT_SOURCE = "test"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if s := state(t, c, `C:\bin\jot.exe`); s != Connected {
		t.Fatalf("after apply state = %v", s)
	}
	if removed, err := Remove(c); !removed || err != nil {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	got = read(t, c.Config)
	if strings.Contains(got, "jot") || !strings.Contains(got, "[profiles.fast]") {
		t.Errorf("after remove:\n%s", got)
	}
}

func TestMissingClient(t *testing.T) {
	c := Client{ID: "x", Config: filepath.Join(t.TempDir(), "nope", "c.json"), Detect: filepath.Join(t.TempDir(), "nope")}
	if s := state(t, c, "jot"); s != Missing {
		t.Fatalf("state = %v", s)
	}
}

func TestClientsUseHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cs, err := Clients()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 || !strings.HasPrefix(cs[0].Config, home) {
		t.Fatalf("clients = %+v", cs)
	}
}
