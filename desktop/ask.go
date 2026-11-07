package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArpitK24/jot/internal/ask"
	"github.com/ArpitK24/jot/internal/chat"
	"github.com/ArpitK24/jot/internal/dedup"
	"github.com/ArpitK24/jot/internal/search"
	"github.com/ArpitK24/jot/internal/store"
)

// askEvent is the one event name replies stream on. Each payload names its
// stream (an id the window picks before sending) and a type: "start",
// "delta" or "done".
const askEvent = "ask"

// emitter sends an event to the window: Wails events in the app, a
// server-sent event stream in the browser preview.
type emitter func(name string, data any)

func (a *App) setEmitter(e emitter) {
	a.askMu.Lock()
	a.emit = e
	a.askMu.Unlock()
}

func (a *App) send(data map[string]any) {
	a.askMu.Lock()
	e := a.emit
	a.askMu.Unlock()
	if e != nil {
		e(askEvent, data)
	}
}

func (a *App) chats() *ask.Chats {
	return &ask.Chats{Store: store.Open(store.GlobalDir(), "global")}
}

func (a *App) registry() *chat.Registry { return chat.NewRegistry(store.GlobalDir()) }

// ChatSummary is one row of the chat list.
type ChatSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Models  []string  `json:"models"`
	Turns   int       `json:"turns"`
	Preview string    `json:"preview"`
}

func summary(c *ask.Conversation) ChatSummary {
	return ChatSummary{ID: c.ID, Title: c.Title, Created: c.Created, Updated: c.Updated, Models: c.Models, Turns: len(c.Turns), Preview: c.Preview()}
}

// Chats lists saved chats, newest first.
func (a *App) Chats() []ChatSummary {
	list, _ := a.chats().List()
	out := []ChatSummary{}
	for _, c := range list {
		out = append(out, summary(c))
	}
	return out
}

// NoteRef points at a note a reply used.
type NoteRef struct {
	Store   string `json:"store"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Missing bool   `json:"missing"` // deleted since
}

// PastRef points at an earlier chat a reply was reminded of.
type PastRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// TurnView is one message as the window shows it.
type TurnView struct {
	ask.Turn
	NoteRefs []NoteRef `json:"noteRefs"`
	PastRefs []PastRef `json:"pastRefs"`
}

// ChatView is a whole conversation.
type ChatView struct {
	ChatSummary
	Turns []TurnView `json:"turns"`
}

// noteIndex maps note ids to their store and title, across every store.
func (a *App) noteIndex() (map[string]NoteRef, []*store.Memory) {
	idx := map[string]NoteRef{}
	var all []*store.Memory
	for _, s := range a.stores() {
		ms, _ := s.st.List()
		for _, m := range ms {
			idx[m.ID] = NoteRef{Store: s.key, ID: m.ID, Title: m.Title()}
			all = append(all, m)
		}
	}
	return idx, all
}

func (a *App) view(c *ask.Conversation) ChatView {
	idx, _ := a.noteIndex()
	titles := map[string]string{}
	if list, err := a.chats().List(); err == nil {
		for _, o := range list {
			titles[o.ID] = o.Title
		}
	}
	v := ChatView{ChatSummary: summary(c), Turns: []TurnView{}}
	for _, t := range c.Turns {
		tv := TurnView{Turn: t, NoteRefs: []NoteRef{}, PastRefs: []PastRef{}}
		for _, id := range t.Notes {
			r, ok := idx[id]
			if !ok {
				r = NoteRef{ID: id, Title: id, Missing: true}
			}
			tv.NoteRefs = append(tv.NoteRefs, r)
		}
		for _, id := range t.Chats {
			if title, ok := titles[id]; ok {
				tv.PastRefs = append(tv.PastRefs, PastRef{ID: id, Title: title})
			}
		}
		v.Turns = append(v.Turns, tv)
	}
	return v
}

// Chat loads one conversation.
func (a *App) Chat(id string) (ChatView, error) {
	c, err := a.chats().Get(id)
	if err != nil {
		return ChatView{}, err
	}
	return a.view(c), nil
}

// DeleteChat removes a conversation. Git history keeps it.
func (a *App) DeleteChat(id string) error { return a.chats().Delete(id) }

// SearchChats ranks chats against query by their words, best first.
func (a *App) SearchChats(query string) []string {
	list, _ := a.chats().List()
	pool := make([]*store.Memory, 0, len(list))
	for _, c := range list {
		var b strings.Builder
		b.WriteString(c.Title)
		for _, t := range c.Turns {
			b.WriteString("\n")
			b.WriteString(t.Content)
		}
		pool = append(pool, &store.Memory{ID: c.ID, Body: b.String(), Updated: c.Updated})
	}
	ids := []string{}
	for _, r := range search.Search(pool, query, search.Options{Limit: 200}) {
		ids = append(ids, r.Memory.ID)
	}
	return ids
}

// AskSend asks a question in chat chatID ("" starts a new chat) and
// returns the chat's id at once. The reply streams as "ask" events tagged
// with stream. An empty text asks again for the last question.
func (a *App) AskSend(stream, chatID, text, model string) (string, error) {
	if stream == "" {
		return "", errors.New("missing stream id")
	}
	cs := a.chats()
	var c *ask.Conversation
	if chatID == "" {
		if strings.TrimSpace(text) == "" {
			return "", errors.New("type a question first")
		}
		c = cs.New(time.Now())
	} else {
		var err error
		if c, err = cs.Get(chatID); err != nil {
			return "", err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.askMu.Lock()
	if a.streams == nil {
		a.streams = map[string]context.CancelFunc{}
		a.busy = map[string]bool{}
	}
	if a.busy[c.ID] {
		a.askMu.Unlock()
		cancel()
		return "", errors.New("this chat is still answering")
	}
	a.streams[stream] = cancel
	a.busy[c.ID] = true
	a.askMu.Unlock()

	if model != "" && model != a.prefs.AskModel {
		a.prefs.AskModel = model
		a.prefs.save()
	}
	go a.answer(ctx, stream, c, text, model)
	return c.ID, nil
}

func (a *App) answer(ctx context.Context, stream string, c *ask.Conversation, text, model string) {
	defer func() {
		a.askMu.Lock()
		if cancel := a.streams[stream]; cancel != nil {
			cancel()
		}
		delete(a.streams, stream)
		delete(a.busy, c.ID)
		a.askMu.Unlock()
	}()
	idx, mems := a.noteIndex()
	s := &ask.Session{
		Chats:    a.chats(),
		Notes:    func() []*store.Memory { return mems },
		Index:    a.semantic(),
		Registry: a.registry(),
	}
	rep, err := s.Send(ctx, c, text, ask.Options{
		Model: model,
		OnRecall: func(r ask.Recalled, ref string) {
			notes := []NoteRef{}
			for _, m := range r.Notes {
				notes = append(notes, idx[m.ID])
			}
			past := []PastRef{}
			for _, e := range r.Past {
				past = append(past, PastRef{ID: e.ChatID, Title: e.Title})
			}
			a.send(map[string]any{"stream": stream, "type": "start", "chat": c.ID, "model": ref, "notes": notes, "past": past})
		},
		OnDelta: func(d string) {
			a.send(map[string]any{"stream": stream, "type": "delta", "text": d})
		},
	})
	done := map[string]any{"stream": stream, "type": "done", "chat": a.view(c)}
	if err != nil {
		done["error"] = friendly(err)
	}
	if rep != nil {
		done["stopped"] = rep.Stopped
		done["suggest"] = rep.Suggest
	}
	a.send(done)
}

// friendly words an error for the window.
func friendly(err error) string {
	switch {
	case errors.Is(err, chat.ErrUnauthorized):
		return err.Error() + ". Check the key in Settings → Models."
	case errors.Is(err, chat.ErrNoModel):
		return "No model yet. Start Ollama and pull a chat model, or add an API key in Settings → Models."
	}
	return err.Error()
}

// AskStop stops a streaming reply. What was written so far is kept.
func (a *App) AskStop(stream string) {
	a.askMu.Lock()
	cancel := a.streams[stream]
	a.askMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Remembered is the result of turning a message into a note.
type Remembered struct {
	Saved   bool  `json:"saved"`
	Note    *Note `json:"note,omitempty"`
	Similar *Note `json:"similar,omitempty"` // an existing note that says the same thing
}

// Remember saves text as a note in the global store, with source "ask".
// Unless force is set, it saves nothing when a note already says the same
// thing and returns that note instead.
func (a *App) Remember(text, kind string, force bool) (Remembered, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Remembered{}, errors.New("nothing to remember")
	}
	g := a.stores()[0]
	if !force {
		ms, _ := g.st.List()
		if sim, _ := dedup.SimilarWith(ms, text, 0, a.semantic()); len(sim) > 0 {
			n := toNote(g, sim[0])
			return Remembered{Similar: &n}, nil
		}
	}
	if kind == "" {
		kind = store.KindNote
	}
	m, err := g.st.Add(text, kind, nil, "ask")
	if err != nil {
		return Remembered{}, err
	}
	n := toNote(g, m)
	return Remembered{Saved: true, Note: &n}, nil
}

// ModelOption is one entry in the model picker.
type ModelOption struct {
	Ref      string `json:"ref"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

// Models is what the model picker and Settings → Models show.
type Models struct {
	Options   []ModelOption `json:"options"`
	Default   string        `json:"default"`   // configured or first available
	Last      string        `json:"last"`      // the model the user picked last
	Providers []chat.Status `json:"providers"` // each provider's state
	Saved     string        `json:"saved"`     // the default saved in config, if any
	KeysFile  string        `json:"keysFile"`  // where API keys are kept
	OpenAI    struct {
		Models []string `json:"models"` // allow-list for the picker
	} `json:"openai"`
}

// ChatModels checks every provider and lists the models to choose from.
func (a *App) ChatModels() Models {
	reg := a.registry()
	ctx := context.Background()
	sts := reg.CheckAll(ctx)
	m := Models{Options: []ModelOption{}, Providers: sts, Last: a.prefs.AskModel, Saved: chat.ReadConfig(store.GlobalDir()).Default, KeysFile: chat.KeysPath()}
	m.OpenAI.Models = reg.Config.OpenAI.Models
	for _, st := range sts {
		for _, name := range st.Models {
			m.Options = append(m.Options, ModelOption{Ref: chat.Ref{Provider: st.Provider, Model: name}.String(), Provider: st.Provider, Name: name})
		}
	}
	m.Default, _ = reg.DefaultModel(ctx, sts)
	return m
}

// TestChatProvider checks one provider right now.
func (a *App) TestChatProvider(provider string) chat.Status {
	return a.registry().Check(context.Background(), provider)
}

// SaveChatProvider sets a provider's address and, when setKey is true, its
// API key (an empty key removes the saved one). Keys go to their own file
// outside the store, never into config.json.
func (a *App) SaveChatProvider(provider, url, key string, setKey bool) (chat.Status, error) {
	reg := a.registry()
	cfg := chat.ReadConfig(store.GlobalDir())
	url = strings.TrimSpace(url)
	switch provider {
	case chat.Ollama:
		cfg.Ollama.URL = url
	case chat.OpenAI:
		cfg.OpenAI.BaseURL = url
	case chat.Anthropic:
		cfg.Anthropic.BaseURL = url
	default:
		return chat.Status{}, fmt.Errorf("unknown provider %q", provider)
	}
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return chat.Status{}, errors.New("the address must start with http:// or https://")
	}
	if err := chat.SaveConfig(store.GlobalDir(), cfg); err != nil {
		return chat.Status{}, err
	}
	if setKey && provider != chat.Ollama {
		if err := reg.Keys.Set(provider, key); err != nil {
			return chat.Status{}, err
		}
	}
	return a.TestChatProvider(provider), nil
}

// SetDefaultModel saves the model new chats start with ("" picks the first
// one available).
func (a *App) SetDefaultModel(ref string) error {
	if ref != "" {
		r, err := chat.ParseRef(ref)
		if err != nil {
			return err
		}
		ref = r.String()
	}
	cfg := chat.ReadConfig(store.GlobalDir())
	cfg.Default = ref
	return chat.SaveConfig(store.GlobalDir(), cfg)
}
