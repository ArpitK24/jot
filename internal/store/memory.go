// Package store keeps memories as plain Markdown files with a small
// front-matter header, one file per memory.
package store

import (
	"bufio"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kinds a memory can be. Free text is allowed, these are the conventional ones.
const (
	KindFact       = "fact"
	KindPreference = "preference"
	KindDecision   = "decision"
	KindNote       = "note"
)

// Memory is one remembered thing.
type Memory struct {
	ID      string
	Kind    string
	Tags    []string
	Source  string // which agent or person wrote it (claude-code, cursor, cli...)
	Created time.Time
	Updated time.Time
	Body    string
	Scope   string // "global" or "project"; set on load, never written to disk
}

const fence = "---"

// Marshal renders the memory as Markdown with front matter.
func (m *Memory) Marshal() []byte {
	var b strings.Builder
	b.WriteString(fence + "\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	fmt.Fprintf(&b, "kind: %s\n", m.Kind)
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(m.Tags, ", "))
	if m.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", m.Source)
	}
	fmt.Fprintf(&b, "created: %s\n", m.Created.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", m.Updated.UTC().Format(time.RFC3339))
	b.WriteString(fence + "\n\n")
	b.WriteString(strings.TrimSpace(m.Body))
	b.WriteString("\n")
	return []byte(b.String())
}

// Parse reads a memory file. Files without front matter are accepted
// as plain notes so hand-written Markdown still works.
func Parse(data []byte) (*Memory, error) {
	m := &Memory{Kind: KindNote}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, fence+"\n") {
		m.Body = strings.TrimSpace(text)
		return m, nil
	}
	rest := text[len(fence)+1:]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return nil, fmt.Errorf("unterminated front matter")
	}
	header := rest[:end]
	body := rest[end+len(fence)+1:]
	sc := bufio.NewScanner(strings.NewReader(header))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			m.ID = v
		case "kind":
			m.Kind = v
		case "tags":
			m.Tags = parseList(v)
		case "source":
			m.Source = v
		case "created":
			m.Created, _ = time.Parse(time.RFC3339, v)
		case "updated":
			m.Updated, _ = time.Parse(time.RFC3339, v)
		}
	}
	m.Body = strings.TrimSpace(body)
	return m, nil
}

func parseList(v string) []string {
	v = strings.TrimPrefix(strings.TrimSuffix(v, "]"), "[")
	return NormalizeTags(strings.Split(v, ","))
}

// NormalizeTags lowercases, trims, dedupes and sorts tags.
func NormalizeTags(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		t = strings.Trim(t, `"'`)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Title is the first line of the body, trimmed, for listings.
func (m *Memory) Title() string {
	line, _, _ := strings.Cut(m.Body, "\n")
	line = strings.TrimSpace(strings.TrimLeft(line, "# "))
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return line
}
