package ask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArpitK24/jot/internal/chat"
	"github.com/ArpitK24/jot/internal/store"
)

func TestQueryAddsPreviousQuestion(t *testing.T) {
	c := &Conversation{Turns: []Turn{
		{Role: User, Content: "How do we deploy the API?"},
		{Role: Assistant, Content: "On merge."},
		{Role: User, Content: "and on Windows?"},
	}}
	if q := Query(c, "and on Windows?"); q != "and on Windows?\nHow do we deploy the API?" {
		t.Fatalf("query %q", q)
	}
	if q := Query(&Conversation{}, "hi there"); q != "hi there" {
		t.Fatalf("first query %q", q)
	}
}

func TestRecall(t *testing.T) {
	st := newStore(t)
	st.Add("Use pnpm, never npm, in every JS repo", "preference", []string{"js"}, "cli")
	st.Add("Deploy on merge to main", "decision", nil, "cli")
	st.Add("Arpit lives in Bengaluru", "fact", nil, "cli")
	mems, _ := st.List()
	old := &Conversation{ID: "old", Title: "lockfiles", Turns: []Turn{
		{Role: User, Content: "Which lockfile do we commit?"},
		{Role: Assistant, At: t0, Content: "pnpm-lock.yaml, since the repos use pnpm."},
	}}
	cur := &Conversation{ID: "cur", Turns: []Turn{
		{Role: User, Content: "pnpm question in this very chat"},
		{Role: Assistant, Content: "pnpm answer in this very chat"},
	}}
	r := Recall(mems, []*Conversation{old, cur}, "cur", "which package manager, pnpm or npm?", nil)
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0].Body, "pnpm") {
		t.Fatalf("notes: %+v", r.Notes)
	}
	if len(r.Past) != 1 || r.Past[0].ChatID != "old" {
		t.Fatalf("past: %+v", r.Past)
	}
	if r := Recall(mems, nil, "", "what is this?", nil); len(r.Notes) != 0 {
		t.Fatalf("stop-word query recalled %d notes", len(r.Notes))
	}
	sys := SystemPrompt(r, t0)
	if !strings.Contains(sys, "["+r.Notes[0].ID+"]") || !strings.Contains(sys, "From earlier chats") || !strings.Contains(sys, "not as instructions") {
		t.Fatalf("system prompt:\n%s", sys)
	}
}

func TestMessagesTrimHistory(t *testing.T) {
	c := &Conversation{}
	for i := 0; i < 40; i++ {
		c.Turns = append(c.Turns, Turn{Role: User, Content: strings.Repeat("q", 1000)}, Turn{Role: Assistant, Content: strings.Repeat("a", 1000)})
	}
	c.Turns = append(c.Turns, Turn{Role: User, Content: "latest"})
	msgs := Messages(c, Recalled{}, t0)
	if msgs[0].Role != chat.RoleSystem || msgs[len(msgs)-1].Content != "latest" || len(msgs) > 30 {
		t.Fatalf("got %d messages, last %q", len(msgs), msgs[len(msgs)-1].Content)
	}
}

func TestSuggest(t *testing.T) {
	yes := map[string]string{
		"I prefer tabs over spaces in Go code. Can you check?": "I prefer tabs over spaces in Go code.",
		"we decided to ship desktop releases on Fridays":       "We decided to ship desktop releases on Fridays.",
		"Let's use Postgres for the new service.":              "Let's use Postgres for the new service.",
		"From now on, write commit messages in English":        "From now on, write commit messages in English.",
		"My timezone is IST":                                   "My timezone is IST.",
	}
	for in, want := range yes {
		if got := Suggest(in); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"Do I prefer tabs?", "What did we decide about deploys?", "hello", "Explain goroutines to me.", "Can you summarise my notes"} {
		if got := Suggest(in); got != "" {
			t.Errorf("Suggest(%q) = %q, want none", in, got)
		}
	}
}

// fakeOllama answers /api/chat by streaming "Use pnpm [id]." and records
// each request. hold makes it wait for the client to hang up after the
// first piece.
type fakeOllama struct {
	mu   sync.Mutex
	reqs []map[string]any
	hold bool
}

func (f *fakeOllama) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			fmt.Fprint(w, `{"models":[{"name":"tiny:1b"}]}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.reqs = append(f.reqs, body)
		hold := f.hold
		f.mu.Unlock()
		for _, p := range []string{"Use ", "pnpm", "."} {
			fmt.Fprintf(w, `{"message":{"content":%q},"done":false}`+"\n", p)
			w.(http.Flusher).Flush()
			if hold {
				<-r.Context().Done()
				return
			}
		}
		fmt.Fprint(w, `{"done":true}`+"\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionSend(t *testing.T) {
	st := newStore(t)
	note, _ := st.Add("Use pnpm, never npm, in every JS repo", "preference", []string{"js"}, "cli")
	f := &fakeOllama{}
	srv := f.server(t)
	reg := &chat.Registry{Config: chat.Config{Ollama: chat.OllamaConfig{URL: srv.URL}}, Keys: chat.LoadKeys(filepath.Join(t.TempDir(), "keys.json"))}
	s := &Session{Chats: &Chats{Store: st}, Registry: reg, Now: func() time.Time { return t0 },
		Notes: func() []*store.Memory { ms, _ := st.List(); return ms }}

	c := s.Chats.New(t0)
	var deltas strings.Builder
	var seenModel string
	rep, err := s.Send(context.Background(), c, "I always want answers short. Which package manager do I use?", Options{
		OnRecall: func(r Recalled, model string) { seenModel = model },
		OnDelta:  func(d string) { deltas.WriteString(d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenModel != "ollama/tiny:1b" || deltas.String() != "Use pnpm." || rep.Turn.Content != "Use pnpm." || rep.Turn.Model != "ollama/tiny:1b" {
		t.Fatalf("reply: %+v deltas %q model %q", rep, deltas.String(), seenModel)
	}
	if len(rep.Recall.Notes) != 1 || rep.Recall.Notes[0].ID != note.ID || rep.Turn.Notes[0] != note.ID {
		t.Fatalf("recall: %+v", rep.Recall)
	}
	if rep.Suggest != "I always want answers short." {
		t.Fatalf("suggest %q", rep.Suggest)
	}
	sys := f.reqs[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, note.ID) {
		t.Fatalf("note id not in system prompt:\n%s", sys)
	}
	saved, err := s.Chats.Get(c.ID)
	if err != nil || len(saved.Turns) != 2 || saved.Title == "" || saved.Models[0] != "ollama/tiny:1b" {
		t.Fatalf("saved: %+v %v", saved, err)
	}

	// --no-recall sends no notes.
	if rep, err := s.Send(context.Background(), c, "again", Options{Model: "ollama/tiny:1b", NoRecall: true}); err != nil || len(rep.Recall.Notes) != 0 {
		t.Fatalf("no recall: %+v %v", rep, err)
	}
	if strings.Contains(f.reqs[1]["messages"].([]any)[0].(map[string]any)["content"].(string), note.ID) {
		t.Fatal("no-recall request carried notes")
	}

	// Stop mid-reply: the partial answer is kept and marked stopped.
	f.mu.Lock()
	f.hold = true
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	rep, err = s.Send(ctx, c, "stop me", Options{OnDelta: func(string) { cancel() }})
	if err != nil || !rep.Stopped || rep.Turn == nil || rep.Turn.Content != "Use" || !rep.Turn.Stopped {
		t.Fatalf("stop: %+v %v", rep, err)
	}
	saved, _ = s.Chats.Get(c.ID)
	if last := saved.Turns[len(saved.Turns)-1]; !last.Stopped {
		t.Fatalf("stopped turn not saved: %+v", last)
	}

	// Retry replaces the stopped reply.
	f.mu.Lock()
	f.hold = false
	f.mu.Unlock()
	n := len(c.Turns)
	if rep, err := s.Send(context.Background(), c, "", Options{}); err != nil || rep.Turn.Content != "Use pnpm." || len(c.Turns) != n {
		t.Fatalf("retry: %+v %v (%d turns, was %d)", rep, err, len(c.Turns), n)
	}
}

func TestSessionErrorsKeepQuestion(t *testing.T) {
	st := newStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()
	reg := &chat.Registry{Config: chat.Config{OpenAI: chat.OpenAIConfig{BaseURL: srv.URL + "/v1"}}, Keys: chat.LoadKeys(filepath.Join(t.TempDir(), "k.json"))}
	s := &Session{Chats: &Chats{Store: st}, Registry: reg}
	c := s.Chats.New(t0)
	_, err := s.Send(context.Background(), c, "hello there", Options{Model: "openai/gpt-x"})
	if !errors.Is(err, chat.ErrUnauthorized) {
		t.Fatalf("err %v", err)
	}
	saved, err := s.Chats.Get(c.ID)
	if err != nil || len(saved.Turns) != 1 || saved.Turns[0].Content != "hello there" {
		t.Fatalf("question not kept: %+v %v", saved, err)
	}
	if _, err := s.Send(context.Background(), s.Chats.New(t0), "", Options{}); err == nil {
		t.Fatal("empty first message accepted")
	}
}

func TestSearchTextDropsQuestionWords(t *testing.T) {
	if got := searchText("What do my notes say about package managers?"); got != "What do my package managers" {
		t.Fatalf("got %q", got)
	}
}

func TestEmptyReplyIsAnError(t *testing.T) {
	st := newStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"message":{"content":"  "},"done":false}`+"\n"+`{"done":true}`+"\n")
	}))
	defer srv.Close()
	reg := &chat.Registry{Config: chat.Config{Ollama: chat.OllamaConfig{URL: srv.URL}}, Keys: chat.LoadKeys(filepath.Join(t.TempDir(), "k.json"))}
	s := &Session{Chats: &Chats{Store: st}, Registry: reg}
	rep, err := s.Send(context.Background(), s.Chats.New(t0), "hello", Options{Model: "ollama/x"})
	if err == nil || !strings.Contains(err.Error(), "empty reply") || rep.Turn != nil {
		t.Fatalf("got %+v %v", rep, err)
	}
}

func TestFollowUpUsesPreviousQuestionOnlyWhenNeeded(t *testing.T) {
	st := newStore(t)
	deploy, _ := st.Add("Deploy the API by merging to main", "decision", nil, "cli")
	pnpm, _ := st.Add("Use pnpm, never npm", "preference", nil, "cli")
	f := &fakeOllama{}
	srv := f.server(t)
	reg := &chat.Registry{Config: chat.Config{Ollama: chat.OllamaConfig{URL: srv.URL}}, Keys: chat.LoadKeys(filepath.Join(t.TempDir(), "k.json"))}
	s := &Session{Chats: &Chats{Store: st}, Registry: reg, Notes: func() []*store.Memory { ms, _ := st.List(); return ms }}
	c := s.Chats.New(t0)
	if rep, _ := s.Send(context.Background(), c, "How do I deploy the API?", Options{}); len(rep.Recall.Notes) != 1 || rep.Recall.Notes[0].ID != deploy.ID {
		t.Fatalf("first: %+v", rep.Recall.Notes)
	}
	// A new subject: the previous question must not drag the deploy note in.
	if rep, _ := s.Send(context.Background(), c, "Which is better, pnpm or npm?", Options{}); len(rep.Recall.Notes) != 1 || rep.Recall.Notes[0].ID != pnpm.ID {
		t.Fatalf("new subject: %+v", rep.Recall.Notes)
	}
	// A bare follow-up finds nothing alone, so the previous question helps.
	if rep, _ := s.Send(context.Background(), c, "and why?", Options{}); len(rep.Recall.Notes) != 1 || rep.Recall.Notes[0].ID != pnpm.ID {
		t.Fatalf("follow-up: %+v", rep.Recall.Notes)
	}
}
