// Package ask is jot's chat with your notes: it keeps conversations as
// Markdown in the global store, recalls the notes and earlier exchanges
// relevant to each question, and streams the answer from a model.
package ask

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ArpitK24/jot/internal/store"
)

// Folder is the store subfolder conversations live in. Files there are not
// memories, so notes, dedup and MCP recall never see them.
const Folder = "chats"

// Roles of a turn.
const (
	User      = "user"
	Assistant = "assistant"
)

// Turn is one message in a conversation.
type Turn struct {
	Role    string    `json:"role"`
	Model   string    `json:"model,omitempty"` // model ref, for assistant turns
	At      time.Time `json:"at"`
	Content string    `json:"content"`
	Notes   []string  `json:"notes,omitempty"` // ids of the notes given to the model
	Chats   []string  `json:"chats,omitempty"` // ids of earlier chats it was reminded of
	Stopped bool      `json:"stopped,omitempty"`
}

// Conversation is one chat, stored as chats/<id>.md.
type Conversation struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Models  []string  `json:"models"`
	Turns   []Turn    `json:"turns"`
}

// Model shows the model name in a heading: "qwen2.5:0.5b", not the ref.
func shortModel(ref string) string {
	for _, p := range []string{"ollama/", "openai/", "anthropic/"} {
		if strings.HasPrefix(ref, p) {
			return ref[len(p):]
		}
	}
	if ref == "" {
		return "Assistant"
	}
	return ref
}

const turnTag = "<!-- jot:turn "

// Marshal renders the conversation as Markdown. Each turn is a "### You"
// or "### <model>" heading followed by an HTML comment that carries the
// turn's details; the comment is what marks a real turn boundary, so a
// heading inside a reply never splits it.
func (c *Conversation) Marshal() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", c.ID)
	fmt.Fprintf(&b, "title: %s\n", oneLine(c.Title))
	fmt.Fprintf(&b, "created: %s\n", c.Created.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", c.Updated.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "models: [%s]\n", strings.Join(c.Models, ", "))
	b.WriteString("---\n")
	for _, t := range c.Turns {
		head := "You"
		if t.Role == Assistant {
			head = shortModel(t.Model)
		}
		fmt.Fprintf(&b, "\n### %s\n%srole=%s", oneLine(head), turnTag, t.Role)
		if t.Model != "" {
			fmt.Fprintf(&b, " model=%s", strings.ReplaceAll(t.Model, " ", "_"))
		}
		fmt.Fprintf(&b, " at=%s", t.At.UTC().Format(time.RFC3339))
		if len(t.Notes) > 0 {
			fmt.Fprintf(&b, " notes=%s", strings.Join(t.Notes, ","))
		}
		if len(t.Chats) > 0 {
			fmt.Fprintf(&b, " chats=%s", strings.Join(t.Chats, ","))
		}
		if t.Stopped {
			b.WriteString(" stopped=true")
		}
		b.WriteString(" -->\n\n")
		b.WriteString(strings.TrimSpace(t.Content))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// ParseConversation reads a chat file.
func ParseConversation(data []byte) (*Conversation, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, errors.New("missing front matter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, errors.New("unterminated front matter")
	}
	c := &Conversation{Models: []string{}, Turns: []Turn{}}
	sc := bufio.NewScanner(strings.NewReader(text[4 : 4+end]))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "id":
			c.ID = v
		case "title":
			c.Title = v
		case "created":
			c.Created, _ = time.Parse(time.RFC3339, v)
		case "updated":
			c.Updated, _ = time.Parse(time.RFC3339, v)
		case "models":
			for _, m := range strings.Split(strings.Trim(v, "[]"), ",") {
				if m = strings.TrimSpace(m); m != "" {
					c.Models = append(c.Models, m)
				}
			}
		}
	}
	body := text[4+end+4:]
	lines := strings.Split(body, "\n")
	var cur *Turn
	var buf []string
	flush := func() {
		if cur != nil {
			cur.Content = strings.TrimSpace(strings.Join(buf, "\n"))
			c.Turns = append(c.Turns, *cur)
		}
		buf = nil
	}
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "### ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], turnTag) {
			flush()
			cur = parseTurnTag(lines[i+1])
			i++
			continue
		}
		buf = append(buf, lines[i])
	}
	flush()
	return c, nil
}

func parseTurnTag(line string) *Turn {
	t := &Turn{}
	inner := strings.TrimSuffix(strings.TrimPrefix(line, turnTag), "-->")
	for _, f := range strings.Fields(inner) {
		k, v, _ := strings.Cut(f, "=")
		switch k {
		case "role":
			t.Role = v
		case "model":
			t.Model = v
		case "at":
			t.At, _ = time.Parse(time.RFC3339, v)
		case "notes":
			t.Notes = strings.Split(v, ",")
		case "chats":
			t.Chats = strings.Split(v, ",")
		case "stopped":
			t.Stopped = v == "true"
		}
	}
	if t.Role != Assistant {
		t.Role = User
	}
	return t
}

// TitleFrom makes a chat title from its first message: the first line,
// without Markdown, cut at a word near 60 characters.
func TitleFrom(text string) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimLeft(line, "#>-*+ ")
	line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
	line = oneLine(line)
	if line == "" {
		return "New chat"
	}
	r := []rune(line)
	if len(r) <= 60 {
		return line
	}
	cut := 60
	for i := 60; i > 35; i-- {
		if unicode.IsSpace(r[i]) {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(r[:cut]), " ,.;:-") + "…"
}

var listMarker = regexp.MustCompile(`(?m)^\s*([-*+]|\d+[.)])\s+`)

// Preview is a short line from the latest turn, for a chat list.
func (c *Conversation) Preview() string {
	for i := len(c.Turns) - 1; i >= 0; i-- {
		s := listMarker.ReplaceAllString(c.Turns[i].Content, "")
		s = oneLine(strings.NewReplacer("#", "", "*", "", "`", "", ">", "").Replace(s))
		if s != "" {
			if r := []rune(s); len(r) > 140 {
				s = string(r[:140]) + "…"
			}
			return s
		}
	}
	return ""
}

// ---------- the chats folder

// Chats reads and writes conversations in a store's chats/ folder.
type Chats struct {
	Store *store.Store
}

func validID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\. `) {
		return fmt.Errorf("invalid chat id %q", id)
	}
	return nil
}

// New starts an unsaved conversation.
func (cs *Chats) New(now time.Time) *Conversation {
	now = now.UTC().Truncate(time.Second)
	return &Conversation{ID: store.NewID(now), Created: now, Updated: now, Models: []string{}, Turns: []Turn{}}
}

// Get loads one conversation.
func (cs *Chats) Get(id string) (*Conversation, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	b, err := cs.Store.ReadFile(Folder + "/" + id + ".md")
	if errors.Is(err, os.ErrNotExist) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := ParseConversation(b)
	if err != nil {
		return nil, fmt.Errorf("chat %s: %w", id, err)
	}
	if c.ID == "" {
		c.ID = id
	}
	return c, nil
}

// List returns every conversation, most recently updated first.
// Unreadable files are skipped.
func (cs *Chats) List() ([]*Conversation, error) {
	entries, err := os.ReadDir(filepath.Join(cs.Store.Dir, Folder))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Conversation
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		c, err := cs.Get(strings.TrimSuffix(name, ".md"))
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Save writes the conversation and commits it. It fills in the title from
// the first message and the list of models used.
func (cs *Chats) Save(c *Conversation, now time.Time) error {
	if err := validID(c.ID); err != nil {
		return err
	}
	if len(c.Turns) == 0 {
		return errors.New("an empty chat is not saved")
	}
	if strings.TrimSpace(c.Title) == "" {
		for _, t := range c.Turns {
			if t.Role == User {
				c.Title = TitleFrom(t.Content)
				break
			}
		}
	}
	seen := map[string]bool{}
	c.Models = c.Models[:0]
	for _, t := range c.Turns {
		if t.Model != "" && !seen[t.Model] {
			seen[t.Model] = true
			c.Models = append(c.Models, t.Model)
		}
	}
	c.Updated = now.UTC().Truncate(time.Second)
	if c.Created.IsZero() {
		c.Created = c.Updated
	}
	return cs.Store.WriteFile(Folder+"/"+c.ID+".md", c.Marshal(), fmt.Sprintf("ask %s: %s", c.ID, c.Title))
}

// Delete removes a conversation. Git history keeps it.
func (cs *Chats) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	return cs.Store.RemoveFile(Folder+"/"+id+".md", "forget chat "+id)
}
