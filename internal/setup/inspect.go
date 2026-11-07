package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Entry is the jot server entry as it stands in a client's config.
type Entry struct {
	Found   bool
	Command string
	Args    []string
}

// Inspect reads the jot entry from c's config without changing anything.
// A missing config file is not an error; it just has no entry.
func Inspect(c Client) (Entry, error) {
	data, err := os.ReadFile(c.Config)
	if errors.Is(err, os.ErrNotExist) {
		return Entry{}, nil
	}
	if err != nil {
		return Entry{}, err
	}
	if c.Format == TOML {
		return tomlEntry(tomlBlock(string(data))), nil
	}
	o, err := parseObject(data)
	if err != nil {
		return Entry{}, fmt.Errorf("%s: %w", c.Config, err)
	}
	servers, err := parseObject(o.get("mcpServers"))
	if err != nil {
		return Entry{}, fmt.Errorf("%s: mcpServers: %w", c.Config, err)
	}
	raw := servers.get(Name)
	if raw == nil {
		return Entry{}, nil
	}
	var e serverEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{Found: true}, fmt.Errorf("%s: mcpServers.%s: %w", c.Config, Name, err)
	}
	return Entry{Found: true, Command: e.Command, Args: e.Args}, nil
}

// RulesCurrent reports whether c's instructions file holds jot's block as
// this version writes it. Tools without an instructions file count as current.
func RulesCurrent(c Client) (bool, error) { return rulesCurrent(c) }

// tomlEntry pulls command and args out of a [mcp_servers.jot] block. It
// understands the plain string and string-array forms jot and its docs use.
func tomlEntry(blk string) Entry {
	if strings.TrimSpace(blk) == "" {
		return Entry{}
	}
	e := Entry{Found: true}
	main := true
	for _, line := range strings.Split(blk, "\n") {
		l := strings.TrimSpace(line)
		if isHeader(l) {
			main = !strings.Contains(strings.ReplaceAll(l, " ", ""), "mcp_servers."+Name+".")
			continue
		}
		if !main {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "command":
			e.Command = tomlString(strings.TrimSpace(v))
		case "args":
			e.Args = tomlStrings(strings.TrimSpace(v))
		}
	}
	return e
}

func tomlString(v string) string {
	if strings.HasPrefix(v, "'") {
		return strings.Trim(v, "'")
	}
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	return strings.Trim(v, `"`)
}

func tomlStrings(v string) []string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v, "["), "]"))
	if v == "" {
		return []string{}
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, tomlString(p))
		}
	}
	return out
}
