package ask

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArpitK24/jot/internal/store"
)

var t0 = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func TestConversationRoundTrip(t *testing.T) {
	c := &Conversation{ID: "20261108-100000-ab12", Title: "Deploys: how?", Created: t0, Updated: t0.Add(time.Minute)}
	c.Turns = []Turn{
		{Role: User, At: t0, Content: "How do we deploy?"},
		{Role: Assistant, Model: "ollama/qwen2.5:0.5b", At: t0.Add(time.Second), Notes: []string{"n1", "n2"}, Chats: []string{"c0"},
			Content: "Merge to main.\n\n### You\n\nThat heading is part of the reply.\n\n```\n### qwen\n```"},
		{Role: User, At: t0.Add(2 * time.Second), Content: "And rollbacks?"},
		{Role: Assistant, Model: "openai/anthropic/claude-3.5-sonnet", At: t0.Add(3 * time.Second), Content: "Revert the merge", Stopped: true},
	}
	c.Models = []string{"ollama/qwen2.5:0.5b", "openai/anthropic/claude-3.5-sonnet"}
	raw := c.Marshal()
	if !strings.Contains(string(raw), "\n### You\n") || !strings.Contains(string(raw), "\n### qwen2.5:0.5b\n") ||
		!strings.Contains(string(raw), "\n### anthropic/claude-3.5-sonnet\n") {
		t.Fatalf("headings:\n%s", raw)
	}
	got, err := ParseConversation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != c.ID || got.Title != c.Title || !got.Updated.Equal(c.Updated) || len(got.Models) != 2 || len(got.Turns) != 4 {
		t.Fatalf("header/turns: %+v", got)
	}
	for i := range c.Turns {
		a, b := c.Turns[i], got.Turns[i]
		if a.Role != b.Role || a.Model != b.Model || !a.At.Equal(b.At) || a.Content != b.Content || a.Stopped != b.Stopped ||
			strings.Join(a.Notes, ",") != strings.Join(b.Notes, ",") || strings.Join(a.Chats, ",") != strings.Join(b.Chats, ",") {
			t.Fatalf("turn %d:\nwant %+v\ngot  %+v", i, a, b)
		}
	}
}

func TestTitleFrom(t *testing.T) {
	cases := map[string]string{
		"  \n# How do I **deploy** jot?\nmore": "How do I deploy jot?",
		"":                                     "New chat",
		"What is the one package manager I said we should use across every single JavaScript repo": "What is the one package manager I said we should use across…",
	}
	for in, want := range cases {
		if got := TitleFrom(in); got != want {
			t.Errorf("TitleFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.Open(filepath.Join(t.TempDir(), "jot"), "global")
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestChatsSaveListDelete(t *testing.T) {
	st := newStore(t)
	cs := &Chats{Store: st}
	st.Add("Deploy on merge to main", "decision", nil, "cli")
	a := cs.New(t0)
	a.Turns = append(a.Turns, Turn{Role: User, At: t0, Content: "first chat"}, Turn{Role: Assistant, Model: "ollama/m", At: t0, Content: "ok"})
	if err := cs.Save(a, t0); err != nil {
		t.Fatal(err)
	}
	b := cs.New(t0.Add(time.Hour))
	b.Turns = append(b.Turns, Turn{Role: User, At: t0, Content: "second chat"})
	if err := cs.Save(b, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := cs.Save(cs.New(t0), t0); err == nil {
		t.Fatal("empty chat saved")
	}
	list, err := cs.List()
	if err != nil || len(list) != 2 || list[0].ID != b.ID || list[1].Title != "first chat" || list[1].Models[0] != "ollama/m" {
		t.Fatalf("list: %+v %v", list, err)
	}
	// Chats never show up as notes.
	if ms, _ := st.List(); len(ms) != 1 || ms[0].Kind != "decision" {
		t.Fatalf("notes: %+v", ms)
	}
	if _, err := cs.Get("../x"); err == nil {
		t.Fatal("bad id accepted")
	}
	if err := cs.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Get(a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestPreviewDropsMarkdown(t *testing.T) {
	c := &Conversation{Turns: []Turn{{Role: Assistant, Content: "- **Warm** paper\n2. Ink `text`\n> quote"}}}
	if p := c.Preview(); p != "Warm paper Ink text quote" {
		t.Fatalf("preview %q", p)
	}
}
