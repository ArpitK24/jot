package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArpitK24/jot/internal/dedup"
	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/search"
	"github.com/ArpitK24/jot/internal/setup"
	"github.com/ArpitK24/jot/internal/store"
)

// App is the backend the window talks to. Every exported method is
// callable from JavaScript; everything it returns is plain JSON.
type App struct {
	prefs   *prefs
	cliPath func() string
	opener  func(path string) error

	ixMu  sync.Mutex
	ix    *embed.Index
	ixCfg embed.Config
}

// semantic returns the embedding index when semantic search is on. The
// index is kept between calls so its vector cache is read once.
func (a *App) semantic() *embed.Index {
	cfg := embed.Load(store.GlobalDir())
	a.ixMu.Lock()
	defer a.ixMu.Unlock()
	if a.ix == nil || cfg != a.ixCfg {
		a.ix, a.ixCfg = embed.Open(cfg), cfg
	}
	return a.ix
}

// NewApp loads saved preferences and makes sure the global store exists.
func NewApp(prefsFile string) *App {
	a := &App{prefs: loadPrefs(prefsFile), cliPath: findCLI, opener: reveal}
	store.Open(store.GlobalDir(), "global").Init()
	return a
}

// Note is one memory as the window sees it.
type Note struct {
	Store   string    `json:"store"` // store key: "global" or a project folder
	Scope   string    `json:"scope"` // label shown in the UI
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Kind    string    `json:"kind"`
	Tags    []string  `json:"tags"`
	Source  string    `json:"source"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Body    string    `json:"body"`
	File    string    `json:"file"`
	Pinned  bool      `json:"pinned"`
}

// StoreInfo describes one memory store.
type StoreInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Dir   string `json:"dir"`
	Count int    `json:"count"`
	Git   bool   `json:"git"`
}

// State is everything the window needs on launch.
type State struct {
	Version   string      `json:"version"`
	OS        string      `json:"os"`
	Onboarded bool        `json:"onboarded"`
	Stores    []StoreInfo `json:"stores"`
	GitFound  bool        `json:"gitFound"`
	CLI       string      `json:"cli"`
	OnPath    bool        `json:"onPath"`
	Connected int         `json:"connected"`
	PrefsFile string      `json:"prefsFile"`
	Theme     string      `json:"theme"` // system | light | dark
}

const pinTag = "pinned"

type openStore struct {
	key, label string
	st         *store.Store
}

func (a *App) stores() []openStore {
	out := []openStore{{"global", "global", store.Open(store.GlobalDir(), "global")}}
	for _, dir := range a.prefs.Projects {
		out = append(out, openStore{dir, filepath.Base(dir), store.Open(filepath.Join(dir, store.DirName), filepath.Base(dir))})
	}
	return out
}

func (a *App) find(key string) (openStore, error) {
	for _, s := range a.stores() {
		if s.key == key {
			return s, nil
		}
	}
	return openStore{}, fmt.Errorf("unknown store %q", key)
}

func toNote(s openStore, m *store.Memory) Note {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	pinned := false
	for _, t := range tags {
		if t == pinTag {
			pinned = true
		}
	}
	return Note{
		Store: s.key, Scope: s.label, ID: m.ID, Title: m.Title(), Kind: m.Kind, Tags: tags,
		Source: m.Source, Created: m.Created, Updated: m.Updated, Body: m.Body,
		File: filepath.Join(s.st.Dir, m.ID+".md"), Pinned: pinned,
	}
}

// State reports the app's setup: stores, git, CLI and connected tools.
func (a *App) State() State {
	st := State{Version: version, OS: runtime.GOOS, Onboarded: a.prefs.Onboarded, PrefsFile: a.prefs.path, Theme: a.themeName()}
	_, err := exec.LookPath("git")
	st.GitFound = err == nil
	for _, s := range a.stores() {
		mems, _ := s.st.List()
		_, gerr := os.Stat(filepath.Join(s.st.Dir, ".git"))
		st.Stores = append(st.Stores, StoreInfo{Key: s.key, Label: s.label, Dir: s.st.Dir, Count: len(mems), Git: gerr == nil})
	}
	st.CLI = a.cliPath()
	st.OnPath = onPath(st.CLI)
	for _, t := range a.Tools() {
		if t.State == "connected" {
			st.Connected++
		}
	}
	return st
}

// Notes lists every memory in every store, newest first.
func (a *App) Notes() ([]Note, error) {
	var out []Note
	for _, s := range a.stores() {
		mems, err := s.st.List()
		if err != nil {
			continue
		}
		for _, m := range mems {
			out = append(out, toNote(s, m))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Hit is a search result: which note, and how well it matched.
type Hit struct {
	Store string  `json:"store"`
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// Search ranks every note against query with jot's keyword search.
func (a *App) Search(query string) []Hit {
	var mems []*store.Memory
	keys := map[*store.Memory]string{}
	for _, s := range a.stores() {
		ms, _ := s.st.List()
		for _, m := range ms {
			keys[m] = s.key
			mems = append(mems, m)
		}
	}
	hits := []Hit{}
	for _, r := range search.Search(mems, query, search.Options{Limit: 200, Index: a.semantic()}) {
		hits = append(hits, Hit{Store: keys[r.Memory], ID: r.Memory.ID, Score: r.Score})
	}
	return hits
}

// Change is one commit in a note's history.
type Change struct {
	Hash    string    `json:"hash"`
	Date    time.Time `json:"date"`
	Subject string    `json:"subject"`
}

// History lists the commits that touched a note.
func (a *App) History(key, id string) ([]Change, error) {
	s, err := a.find(key)
	if err != nil {
		return nil, err
	}
	cs, err := s.st.History(id, 20)
	out := []Change{}
	for _, c := range cs {
		out = append(out, Change{c.Hash, c.Date, c.Subject})
	}
	return out, err
}

// Create saves a new note written in the app.
func (a *App) Create(key, body, kind string, tags []string) (Note, error) {
	s, err := a.find(key)
	if err != nil {
		return Note{}, err
	}
	if strings.TrimSpace(body) == "" {
		return Note{}, errors.New("a note needs some text")
	}
	if err := s.st.Init(); err != nil {
		return Note{}, err
	}
	m, err := s.st.Add(body, kind, tags, "desktop")
	if err != nil {
		return Note{}, err
	}
	return toNote(s, m), nil
}

// Save updates a note's text, kind and tags.
func (a *App) Save(key, id, body, kind string, tags []string) (Note, error) {
	s, err := a.find(key)
	if err != nil {
		return Note{}, err
	}
	if strings.TrimSpace(body) == "" {
		return Note{}, errors.New("a note needs some text")
	}
	if tags == nil {
		tags = []string{}
	}
	m, err := s.st.Update(id, body, kind, tags)
	if err != nil {
		return Note{}, err
	}
	return toNote(s, m), nil
}

// SetPinned pins or unpins a note. A pin is the tag "pinned", so agents
// and the CLI see it too.
func (a *App) SetPinned(key, id string, pinned bool) (Note, error) {
	s, err := a.find(key)
	if err != nil {
		return Note{}, err
	}
	m, err := s.st.Get(id)
	if err != nil {
		return Note{}, err
	}
	tags := []string{}
	for _, t := range m.Tags {
		if t != pinTag {
			tags = append(tags, t)
		}
	}
	if pinned {
		tags = append(tags, pinTag)
	}
	m, err = s.st.Update(id, "", "", tags)
	if err != nil {
		return Note{}, err
	}
	return toNote(s, m), nil
}

// Delete removes a note. Git history keeps it recoverable.
func (a *App) Delete(key, id string) error {
	s, err := a.find(key)
	if err != nil {
		return err
	}
	return s.st.Delete(id)
}

// Pair is two notes that look like duplicates, with a suggested merge.
type Pair struct {
	Keep       Note     `json:"keep"`
	Drop       Note     `json:"drop"`
	Score      float64  `json:"score"`
	Semantic   bool     `json:"semantic"`
	MergedBody string   `json:"mergedBody"`
	MergedTags []string `json:"mergedTags"`
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// Duplicates finds notes that say the same thing, within each store,
// skipping pairs the user chose to keep.
func (a *App) Duplicates() []Pair {
	skip := map[string]bool{}
	for _, k := range a.prefs.Dismissed {
		skip[k] = true
	}
	out := []Pair{}
	ix := a.semantic()
	for _, s := range a.stores() {
		mems, err := s.st.List()
		if err != nil {
			continue
		}
		pairs, _ := dedup.PairsWith(mems, dedup.Threshold, ix)
		for _, p := range pairs {
			if skip[pairKey(p.Keep.ID, p.Drop.ID)] {
				continue
			}
			out = append(out, Pair{
				Keep: toNote(s, p.Keep), Drop: toNote(s, p.Drop), Score: p.Score, Semantic: p.Semantic,
				MergedBody: dedup.MergeBody(p.Keep.Body, p.Drop.Body),
				MergedTags: dedup.MergeTags(p.Keep.Tags, p.Drop.Tags),
			})
		}
	}
	return out
}

// Merge folds dropID into keepID with the text the user approved.
func (a *App) Merge(key, keepID, dropID, body string, tags []string) (Note, error) {
	s, err := a.find(key)
	if err != nil {
		return Note{}, err
	}
	m, err := s.st.Merge(keepID, dropID, body, tags)
	if err != nil {
		return Note{}, err
	}
	return toNote(s, m), nil
}

// KeepBoth stops a pair from being suggested again.
func (a *App) KeepBoth(keepID, dropID string) error {
	a.prefs.Dismissed = append(a.prefs.Dismissed, pairKey(keepID, dropID))
	return a.prefs.save()
}

// Tool is one AI tool and whether jot is connected to it.
type Tool struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Config  string `json:"config"`
	State   string `json:"state"` // missing, absent, different, connected
	Snippet string `json:"snippet"`
	Lines   int    `json:"lines"`
}

var stateNames = map[setup.State]string{
	setup.Missing: "missing", setup.Absent: "absent", setup.Different: "different", setup.Connected: "connected",
}

// Tools lists the AI tools jot can connect, with the change it would make.
func (a *App) Tools() []Tool {
	clients, err := setup.Clients()
	if err != nil {
		return nil
	}
	cmd := a.cliPath()
	var out []Tool
	for _, c := range clients {
		t := Tool{ID: c.ID, Name: c.Name, Config: tilde(c.Config)}
		p, err := setup.Check(c, cmd)
		if err != nil {
			t.State = "error"
		} else {
			t.State = stateNames[p.State]
			t.Snippet = strings.TrimRight(p.Snippet, "\n")
			t.Lines = strings.Count(t.Snippet, "\n") + 1
		}
		out = append(out, t)
	}
	return out
}

// Connect writes jot into the chosen tools' MCP configs.
func (a *App) Connect(ids []string) ([]Tool, error) {
	cmd := a.cliPath()
	if cmd == "" {
		return nil, errors.New("the jot command-line tool was not found next to the app; reinstall jot")
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	clients, err := setup.Clients()
	if err != nil {
		return nil, err
	}
	var errs []string
	for _, c := range clients {
		if !want[c.ID] {
			continue
		}
		p, err := setup.Check(c, cmd)
		if err == nil && (p.State == setup.Absent || p.State == setup.Different) {
			err = setup.Apply(p)
		}
		if err != nil {
			errs = append(errs, c.Name+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return a.Tools(), errors.New(strings.Join(errs, "; "))
	}
	return a.Tools(), nil
}

// Disconnect removes jot from one tool's config.
func (a *App) Disconnect(id string) ([]Tool, error) {
	clients, err := setup.Clients()
	if err != nil {
		return nil, err
	}
	for _, c := range clients {
		if c.ID == id {
			if _, err := setup.Remove(c); err != nil {
				return a.Tools(), err
			}
		}
	}
	return a.Tools(), nil
}

// FinishOnboarding records that the first-run setup is done.
func (a *App) FinishOnboarding() error {
	a.prefs.Onboarded = true
	return a.prefs.save()
}

// AddProjectDir adds a project folder and creates its .jot store.
func (a *App) AddProjectDir(dir string) error {
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	for _, p := range a.prefs.Projects {
		if p == dir {
			return nil
		}
	}
	if err := store.Open(filepath.Join(dir, store.DirName), filepath.Base(dir)).Init(); err != nil {
		return err
	}
	a.prefs.Projects = append(a.prefs.Projects, dir)
	return a.prefs.save()
}

// RemoveProject stops showing a project. Its files stay on disk.
func (a *App) RemoveProject(dir string) error {
	var keep []string
	for _, p := range a.prefs.Projects {
		if p != dir {
			keep = append(keep, p)
		}
	}
	a.prefs.Projects = keep
	return a.prefs.save()
}

// Reveal opens a folder or file in the system file manager.
func (a *App) Reveal(path string) error { return a.opener(path) }

// AddToPath puts the jot command-line tool on the user's PATH.
func (a *App) AddToPath() (State, error) {
	cli := a.cliPath()
	if cli == "" {
		return a.State(), errors.New("the jot command-line tool was not found next to the app")
	}
	err := addToPath(filepath.Dir(cli))
	return a.State(), err
}

// findCLI returns the jot CLI shipped beside the app, else the one on PATH.
func findCLI() string {
	name := "jot"
	if runtime.GOOS == "windows" {
		name = "jot.exe"
	}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		for _, dir := range []string{filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "..", "Resources")} {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && !samePath(p, exe) {
				return filepath.Clean(p)
			}
		}
	}
	if p, err := exec.LookPath("jot"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	return ""
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// onPath reports whether running "jot" in a new terminal reaches cli.
func onPath(cli string) bool {
	if cli == "" {
		return false
	}
	dir := filepath.Dir(cli)
	for _, d := range filepath.SplitList(userPath()) {
		if samePath(os.ExpandEnv(d), dir) {
			return true
		}
	}
	return false
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

func reveal(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// Semantic describes semantic search for the Settings screen.
type Semantic struct {
	On     bool   `json:"on"`
	Model  string `json:"model"`
	URL    string `json:"url"`
	Ready  bool   `json:"ready"`
	Detail string `json:"detail"` // why it isn't ready
	Cached int    `json:"cached"`
}

// SemanticStatus reports whether semantic search is on and working.
func (a *App) SemanticStatus() Semantic {
	ix := a.semantic()
	if ix == nil {
		return Semantic{Model: embed.DefaultModel, URL: embed.DefaultURL}
	}
	st := Semantic{On: true, Model: ix.Model(), URL: ix.URL(), Cached: ix.Cached()}
	if err := ix.Check(context.Background()); err != nil {
		st.Detail = strings.TrimPrefix(err.Error(), embed.ErrUnavailable.Error()+": ")
	} else {
		st.Ready = true
	}
	return st
}

// SetSemantic turns semantic search on or off. Turning it on embeds every
// note once, so the first search is quick.
func (a *App) SetSemantic(on bool) (Semantic, error) {
	cfg := embed.Config{}
	if on {
		cfg = embed.Load(store.GlobalDir())
		if !cfg.On() {
			cfg = embed.Config{Provider: "ollama", Model: embed.DefaultModel, URL: embed.DefaultURL}
		}
	}
	if err := embed.Save(store.GlobalDir(), cfg); err != nil {
		return Semantic{}, err
	}
	st := a.SemanticStatus()
	if st.Ready {
		var texts []string
		for _, s := range a.stores() {
			ms, _ := s.st.List()
			for _, m := range ms {
				texts = append(texts, m.Body)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := a.semantic().Documents(ctx, texts); err == nil {
			st.Cached = a.semantic().Cached()
		}
	}
	return st, nil
}
