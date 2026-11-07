package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withRules(t *testing.T, content string) Client {
	t.Helper()
	c := client(t, JSON, "mcp.json", "")
	c.Rules = filepath.Join(filepath.Dir(c.Config), "rules", "AGENTS.md")
	if content != "" {
		os.MkdirAll(filepath.Dir(c.Rules), 0o755)
		if err := os.WriteFile(c.Rules, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestRulesAddedKeptAndRemoved(t *testing.T) {
	orig := "# My rules\n\nAlways use tabs.\n"
	c := withRules(t, orig)
	p, err := Check(c, "jot")
	if err != nil || p.State != Absent || !p.Rules {
		t.Fatalf("before: %+v %v", p, err)
	}
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	got := read(t, c.Rules)
	if !strings.HasPrefix(got, orig+"\n") || !strings.HasSuffix(got, RulesBlock) {
		t.Fatalf("rules file:\n%s", got)
	}
	if p, _ := Check(c, "jot"); p.State != Connected || p.Rules {
		t.Fatalf("after apply: %+v", p)
	}
	// Applying twice changes nothing.
	Apply(p)
	if read(t, c.Rules) != got {
		t.Fatal("second apply changed the file")
	}
	if read(t, c.Rules+".jot-backup") != orig {
		t.Fatal("original not backed up")
	}
	removed, err := Remove(c)
	if err != nil || !removed {
		t.Fatal("remove", err)
	}
	if read(t, c.Rules) != orig {
		t.Fatalf("remove didn't restore the file:\n%q", read(t, c.Rules))
	}
}

func TestRulesMissingMarksConnectedClientForUpdate(t *testing.T) {
	c := withRules(t, "")
	p, _ := Check(c, "jot")
	if err := applyServer(p); err != nil {
		t.Fatal(err)
	}
	p, _ = Check(c, "jot")
	if p.State != Different || !p.Rules {
		t.Fatalf("server only: %+v", p)
	}
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	if read(t, c.Rules) != RulesBlock {
		t.Fatalf("new file: %q", read(t, c.Rules))
	}
	Remove(c)
	if strings.TrimSpace(read(t, c.Rules)) != "" {
		t.Fatal("block left behind")
	}
}

func TestRulesOldBlockRefreshedInPlace(t *testing.T) {
	old := "top\n\n" + rulesStart + "\nold text\n" + rulesEnd + "\n\nbottom\n"
	c := withRules(t, old)
	if ok, _ := rulesCurrent(c); ok {
		t.Fatal("old block counted as current")
	}
	if err := applyRules(c); err != nil {
		t.Fatal(err)
	}
	if got := read(t, c.Rules); got != "top\n\n"+RulesBlock+"\nbottom\n" {
		t.Fatalf("refresh:\n%q", got)
	}
}

func TestClientWithoutRulesFile(t *testing.T) {
	c := client(t, JSON, "mcp.json", "")
	p, _ := Check(c, "jot")
	if p.Rules {
		t.Fatal("rules wanted for a client without a rules file")
	}
}
