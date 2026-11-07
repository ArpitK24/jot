package setup

import (
	"reflect"
	"testing"
)

func TestInspect(t *testing.T) {
	c := client(t, JSON, "mcp.json", `{"mcpServers":{"jot":{"command":"/opt/jot","args":["serve"],"env":{"JOT_SOURCE":"x"}}}}`)
	e, err := Inspect(c)
	if err != nil || !e.Found || e.Command != "/opt/jot" || !reflect.DeepEqual(e.Args, []string{"serve"}) {
		t.Fatalf("json: %+v %v", e, err)
	}

	c = client(t, JSON, "mcp.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	if e, err := Inspect(c); err != nil || e.Found {
		t.Fatalf("absent: %+v %v", e, err)
	}

	c = client(t, JSON, "missing.json", "")
	if e, err := Inspect(c); err != nil || e.Found {
		t.Fatalf("no file: %+v %v", e, err)
	}

	c = client(t, JSON, "mcp.json", `{"mcpServers":`)
	if _, err := Inspect(c); err == nil {
		t.Fatal("want error for broken JSON")
	}

	toml := "model = \"o3\"\n\n[mcp_servers.jot]\ncommand = \"C:\\\\bin\\\\jot.exe\"\nargs = [\"serve\"]\n\n[mcp_servers.jot.env]\nJOT_SOURCE = \"codex\"\n\n[other]\ncommand = \"nope\"\n"
	c = client(t, TOML, "config.toml", toml)
	e, err = Inspect(c)
	if err != nil || !e.Found || e.Command != `C:\bin\jot.exe` || !reflect.DeepEqual(e.Args, []string{"serve"}) {
		t.Fatalf("toml: %+v %v", e, err)
	}

	c = client(t, TOML, "config.toml", "[mcp_servers.jot]\ncommand = 'jot'\nargs = []\nenv = { JOT_SOURCE = \"codex\" }\n")
	e, _ = Inspect(c)
	if e.Command != "jot" || len(e.Args) != 0 {
		t.Fatalf("toml literal: %+v", e)
	}
}
