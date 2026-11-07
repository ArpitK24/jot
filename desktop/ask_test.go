package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArpitK24/jot/internal/chat"
	"github.com/ArpitK24/jot/internal/store"
)

// events records what the app would send the window.
type events struct {
	mu  sync.Mutex
	got []map[string]any
	ch  chan map[string]any
}

func (e *events) emit(name string, data any) {
	m := data.(map[string]any)
	e.mu.Lock()
	e.got = append(e.got, m)
	e.mu.Unlock()
	if m["type"] == "done" {
		e.ch <- m
	}
}

func (e *events) wait(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-e.ch:
		return m
	case <-time.After(10 * time.Second):
		t.Fatal("no done event")
	}
	return nil
}

func askApp(t *testing.T, chatHandler http.HandlerFunc) (*App, *events) {
	t.Helper()
	a, _ := testApp(t)
	t.Setenv("JOT_EMBED", "off")
	t.Setenv("JOT_CHAT_MODEL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"tiny:1b"}]}`)
			return
		}
		chatHandler(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("JOT_OLLAMA_URL", srv.URL)
	ev := &events{ch: make(chan map[string]any, 4)}
	a.setEmitter(ev.emit)
	return a, ev
}

func streamReply(parts ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, p := range parts {
			fmt.Fprintf(w, `{"message":{"content":%q},"done":false}`+"\n", p)
		}
		fmt.Fprint(w, `{"done":true}`+"\n")
	}
}

func TestAskFlow(t *testing.T) {
	a, ev := askApp(t, streamReply("Use ", "pnpm."))
	n, _ := a.Create("global", "Preferred package manager: pnpm, never npm", "preference", nil)

	id, err := a.AskSend("s1", "", "Which package manager do I prefer? I always want short answers.", "ollama/tiny:1b")
	if err != nil || id == "" {
		t.Fatal(id, err)
	}
	done := ev.wait(t)
	if done["error"] != nil || done["stream"] != "s1" || done["suggest"] != "I always want short answers." {
		t.Fatalf("done: %+v", done)
	}
	view := done["chat"].(ChatView)
	if view.ID != id || len(view.Turns) != 2 || view.Turns[1].Content != "Use pnpm." {
		t.Fatalf("view: %+v", view)
	}
	refs := view.Turns[1].NoteRefs
	if len(refs) != 1 || refs[0].ID != n.ID || refs[0].Store != "global" || refs[0].Title != n.Title {
		t.Fatalf("note refs: %+v", refs)
	}
	ev.mu.Lock()
	start, deltas := ev.got[0], ""
	for _, e := range ev.got {
		if e["type"] == "delta" {
			deltas += e["text"].(string)
		}
	}
	ev.mu.Unlock()
	if start["type"] != "start" || start["model"] != "ollama/tiny:1b" || len(start["notes"].([]NoteRef)) != 1 || deltas != "Use pnpm." {
		t.Fatalf("events: start %+v deltas %q", start, deltas)
	}
	if a.prefs.AskModel != "ollama/tiny:1b" {
		t.Fatal("model choice not remembered")
	}

	// The chat is listed and searchable, and never shows up as a note.
	if cs := a.Chats(); len(cs) != 1 || cs[0].ID != id || cs[0].Turns != 2 {
		t.Fatalf("chats: %+v", cs)
	}
	if ids := a.SearchChats("package manager"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("search chats: %v", ids)
	}
	if ids := a.SearchChats("kubernetes"); len(ids) != 0 {
		t.Fatalf("search chats miss: %v", ids)
	}
	notes, _ := a.Notes()
	if len(notes) != 1 || len(a.Duplicates()) != 0 || len(a.Search("package manager")) != 1 {
		t.Fatalf("chat leaked into notes: %d notes", len(notes))
	}
	if st := a.State(); st.Stores[0].Count != 1 {
		t.Fatalf("store count %d", st.Stores[0].Count)
	}

	// Remember a message: a duplicate is caught, then forced.
	r, err := a.Remember("Preferred package manager: pnpm, never npm.", "preference", false)
	if err != nil || r.Saved || r.Similar == nil || r.Similar.ID != n.ID {
		t.Fatalf("remember dup: %+v %v", r, err)
	}
	r, err = a.Remember("I always want short answers.", "preference", false)
	if err != nil || !r.Saved || r.Note.Source != "ask" || r.Note.Kind != "preference" {
		t.Fatalf("remember: %+v %v", r, err)
	}

	if err := a.DeleteChat(id); err != nil || len(a.Chats()) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

func TestAskStopKeepsPartial(t *testing.T) {
	release := make(chan struct{})
	a, ev := askApp(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"message":{"content":"partial"},"done":false}`+"\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	// Stop as soon as the first piece arrives.
	stopped := make(chan bool, 1)
	inner := ev.emit
	a.setEmitter(func(name string, data any) {
		if m := data.(map[string]any); m["type"] == "delta" {
			select {
			case stopped <- true:
				a.AskStop("s2")
			default:
			}
		}
		inner(name, data)
	})
	id, err := a.AskSend("s2", "", "tell me something long", "ollama/tiny:1b")
	if err != nil {
		t.Fatal(err)
	}
	done := ev.wait(t)
	view := done["chat"].(ChatView)
	if done["stopped"] != true || done["error"] != nil || len(view.Turns) != 2 || !view.Turns[1].Stopped || view.Turns[1].Content != "partial" {
		t.Fatalf("stop: %+v", done)
	}
	if _, err := a.AskSend("s3", id, "", "ollama/tiny:1b"); err != nil { // retry is accepted once idle
		t.Fatal(err)
	}
	a.AskStop("s3")
	ev.wait(t)
}

func TestAskErrorsReachTheWindow(t *testing.T) {
	a, ev := askApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"model \"nope\" not found, try pulling it first"}`)
	})
	if _, err := a.AskSend("s", "", "", ""); err == nil {
		t.Fatal("empty first message accepted")
	}
	id, err := a.AskSend("s4", "", "hello", "ollama/nope")
	if err != nil {
		t.Fatal(err)
	}
	done := ev.wait(t)
	if msg, _ := done["error"].(string); !strings.Contains(msg, "ollama pull nope") {
		t.Fatalf("error: %+v", done)
	}
	if c, err := a.Chat(id); err != nil || len(c.Turns) != 1 {
		t.Fatalf("question not kept: %+v %v", c, err)
	}
}

func TestModelsSettings(t *testing.T) {
	a, _ := askApp(t, streamReply("x"))
	m := a.ChatModels()
	if len(m.Options) != 1 || m.Options[0].Ref != "ollama/tiny:1b" || m.Default != "ollama/tiny:1b" || len(m.Providers) != 3 {
		t.Fatalf("models: %+v", m)
	}
	if m.Providers[1].Configured || m.Providers[2].Configured {
		t.Fatalf("cloud providers configured without keys: %+v", m.Providers)
	}

	// A key is stored outside the store, 0600, and only ever returned masked.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test-1234567890abcd" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "gpt-test"}}})
	}))
	defer fake.Close()
	st, err := a.SaveChatProvider(chat.OpenAI, fake.URL+"/v1", "sk-test-1234567890abcd", true)
	if err != nil || !st.Ready || st.Key != "sk-••••abcd" || st.KeySource != "saved" || st.Models[0] != "gpt-test" {
		t.Fatalf("save openai: %+v %v", st, err)
	}
	cfg, _ := os.ReadFile(filepath.Join(store.GlobalDir(), "config.json"))
	if strings.Contains(string(cfg), "sk-test") || !strings.Contains(string(cfg), fake.URL) {
		t.Fatalf("config.json: %s", cfg)
	}
	b, err := os.ReadFile(chat.KeysPath())
	if err != nil || !strings.Contains(string(b), "sk-test-1234567890abcd") {
		t.Fatalf("keys file: %v", err)
	}
	if strings.HasPrefix(chat.KeysPath(), store.GlobalDir()) {
		t.Fatal("keys file inside the store")
	}
	st, _ = a.SaveChatProvider(chat.OpenAI, fake.URL+"/v1", "sk-wrong-000000000000", true)
	if st.Ready || !strings.Contains(st.Detail, "rejected") {
		t.Fatalf("bad key: %+v", st)
	}
	if _, err := a.SaveChatProvider(chat.Ollama, "localhost:11434", "", false); err == nil {
		t.Fatal("address without scheme accepted")
	}
	if err := a.SetDefaultModel("gpt-test"); err != nil {
		t.Fatal(err)
	}
	if m := a.ChatModels(); m.Saved != "openai/gpt-test" || m.Default != "openai/gpt-test" {
		t.Fatalf("default: %+v", m)
	}
}
