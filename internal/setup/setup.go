// Package setup connects jot to the AI tools installed on this machine by
// adding one MCP server entry to each tool's own config file. It never
// touches anything else in those files, and it can take the entry out again.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Name is the server name jot registers under in every client.
const Name = "jot"

// Format is the shape of a client's config file.
type Format int

const (
	// JSON configs keep servers under a top-level "mcpServers" object.
	JSON Format = iota
	// TOML is Codex's config.toml with [mcp_servers.<name>] tables.
	TOML
)

// Client is one AI tool jot knows how to connect.
type Client struct {
	ID     string // short id used on the command line and as JOT_SOURCE
	Name   string // display name
	Config string // absolute path to the config file
	Detect string // directory whose presence means the tool is installed
	Format Format
}

// Clients lists the tools jot can connect, with paths for this OS and user.
func Clients() ([]Client, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = filepath.Join(home, ".config")
	}
	desktop := filepath.Join(cfg, "Claude")
	return []Client{
		{ID: "claude-code", Name: "Claude Code", Config: filepath.Join(home, ".claude.json"), Detect: filepath.Join(home, ".claude"), Format: JSON},
		{ID: "cursor", Name: "Cursor", Config: filepath.Join(home, ".cursor", "mcp.json"), Detect: filepath.Join(home, ".cursor"), Format: JSON},
		{ID: "codex", Name: "Codex", Config: filepath.Join(home, ".codex", "config.toml"), Detect: filepath.Join(home, ".codex"), Format: TOML},
		{ID: "antigravity", Name: "Antigravity", Config: filepath.Join(home, ".gemini", "config", "mcp_config.json"), Detect: filepath.Join(home, ".gemini"), Format: JSON},
		{ID: "claude-desktop", Name: "Claude Desktop", Config: filepath.Join(desktop, "claude_desktop_config.json"), Detect: desktop, Format: JSON},
	}, nil
}

// State says where a client stands.
type State int

const (
	Missing   State = iota // tool not installed
	Absent                 // installed, jot not configured
	Different              // jot configured with another command or env
	Connected              // jot configured exactly as wanted
)

func (s State) String() string {
	return [...]string{"not installed", "will connect", "will update", "connected"}[s]
}

// Plan is the change setup would make to one client.
type Plan struct {
	Client  Client
	State   State
	Command string
	Snippet string // what gets written, for showing the user
}

// Installed reports whether the client's tool is on this machine.
func (c Client) Installed() bool {
	if fi, err := os.Stat(c.Detect); err == nil && fi.IsDir() {
		return true
	}
	_, err := os.Stat(c.Config)
	return err == nil
}

// Check works out what connecting c to command would change.
func Check(c Client, command string) (Plan, error) {
	p := Plan{Client: c, Command: command, Snippet: snippet(c, command)}
	if !c.Installed() {
		p.State = Missing
		return p, nil
	}
	data, err := os.ReadFile(c.Config)
	if errors.Is(err, os.ErrNotExist) {
		p.State = Absent
		return p, nil
	}
	if err != nil {
		return p, err
	}
	switch c.Format {
	case TOML:
		blk := tomlBlock(string(data))
		switch {
		case blk == "":
			p.State = Absent
		case strings.TrimSpace(blk) == strings.TrimSpace(p.Snippet):
			p.State = Connected
		default:
			p.State = Different
		}
	default:
		o, err := parseObject(data)
		if err != nil {
			return p, fmt.Errorf("%s: %w", c.Config, err)
		}
		servers, err := parseObject(o.get("mcpServers"))
		if err != nil {
			return p, fmt.Errorf("%s: mcpServers: %w", c.Config, err)
		}
		cur := servers.get(Name)
		switch {
		case cur == nil:
			p.State = Absent
		case jsonEqual(cur, entry(c, command)):
			p.State = Connected
		default:
			p.State = Different
		}
	}
	return p, nil
}

// Apply writes the jot entry into the client's config, creating the file
// if needed. A copy of the original is kept next to it as <file>.jot-backup
// the first time jot touches it.
func Apply(p Plan) error {
	c := p.Client
	data, err := os.ReadFile(c.Config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var out []byte
	switch c.Format {
	case TOML:
		text := removeTOMLBlock(string(data))
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		out = []byte(text + p.Snippet)
	default:
		o, err := parseObject(data)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Config, err)
		}
		servers, err := parseObject(o.get("mcpServers"))
		if err != nil {
			return fmt.Errorf("%s: mcpServers: %w", c.Config, err)
		}
		servers.set(Name, entry(c, p.Command))
		o.set("mcpServers", servers.bytes())
		out = pretty(o.bytes())
	}
	return write(c.Config, data, out)
}

// Remove takes the jot entry out of the client's config. It reports
// whether there was anything to remove.
func Remove(c Client) (bool, error) {
	data, err := os.ReadFile(c.Config)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var out []byte
	switch c.Format {
	case TOML:
		if tomlBlock(string(data)) == "" {
			return false, nil
		}
		out = []byte(strings.TrimRight(removeTOMLBlock(string(data)), "\n") + "\n")
	default:
		o, err := parseObject(data)
		if err != nil {
			return false, fmt.Errorf("%s: %w", c.Config, err)
		}
		servers, err := parseObject(o.get("mcpServers"))
		if err != nil {
			return false, err
		}
		if !servers.del(Name) {
			return false, nil
		}
		o.set("mcpServers", servers.bytes())
		out = pretty(o.bytes())
	}
	return true, write(c.Config, data, out)
}

func write(path string, old, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old != nil {
		bak := path + ".jot-backup"
		if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(bak, old, 0o600); err != nil {
				return err
			}
		}
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".jot-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func entry(c Client, command string) json.RawMessage {
	e := map[string]any{
		"command": command,
		"args":    []string{"serve"},
		"env":     map[string]string{"JOT_SOURCE": c.ID},
	}
	if c.ID == "claude-code" {
		e["type"] = "stdio"
	}
	b, _ := json.Marshal(e)
	return b
}

func snippet(c Client, command string) string {
	if c.Format == TOML {
		return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [\"serve\"]\n\n[mcp_servers.%s.env]\nJOT_SOURCE = %s\n",
			Name, tomlQuote(command), Name, tomlQuote(c.ID))
	}
	var b bytes.Buffer
	json.Indent(&b, entry(c, command), "", "  ")
	return fmt.Sprintf("%q: %s\n", Name, b.String())
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

func pretty(b []byte) []byte {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		return b
	}
	out.WriteByte('\n')
	return out.Bytes()
}
