package mcp

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArpitK24/jot/internal/store"
)

// repo makes a fake repo with a .jot project store holding one memory.
func repo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := store.Open(filepath.Join(dir, store.DirName), "project").Add(body, "fact", nil, "test"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func call(id, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestRecallSearchesProjectAndGlobal(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "global"))
	if _, err := store.Open(store.GlobalDir(), "global").Add("Run the Go tests with -race everywhere.", "preference", nil, "test"); err != nil {
		t.Fatal(err)
	}
	dir := repo(t, "This repo's Go tests need TEST_DB_URL set to the local Postgres.")
	sub := filepath.Join(dir, "internal", "pkg")
	os.MkdirAll(sub, 0o755)

	s := &Server{Dir: sub, Resolve: store.Stores}
	res := rpc(t, s, call("1", "recall", `{"query":"go tests"}`))
	got := text(res[0])
	if !strings.Contains(got, "from project memory") || !strings.Contains(got, "from global memory") {
		t.Fatalf("want both scopes labelled:\n%s", got)
	}
	if strings.Index(got, "TEST_DB_URL") < 0 || strings.Index(got, "-race") < 0 {
		t.Fatalf("missing a memory:\n%s", got)
	}
}

func TestProjectStoreCreatedMidSession(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "global"))
	dir := t.TempDir()
	s := &Server{Dir: dir, Resolve: store.Stores}
	if text(rpc(t, s, call("1", "remember", `{"text":"Deploys go through the staging branch.","scope":"project"}`))[0]) == "" {
		t.Fatal("no reply")
	}
	if err := store.Open(filepath.Join(dir, store.DirName), "project").Init(); err != nil {
		t.Fatal(err)
	}
	got := text(rpc(t, s, call("2", "remember", `{"text":"Deploys go through the staging branch.","scope":"project"}`))[0])
	if !strings.Contains(got, "project") || !strings.HasPrefix(got, "Remembered") {
		t.Fatalf("after jot init --project: %s", got)
	}
}

func TestRootsPointAtTheRepo(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "global"))
	dir := repo(t, "The API server listens on port 8088 in development.")
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}).String()
	if !strings.HasPrefix(filepath.ToSlash(dir), "/") { // Windows: file:///C:/...
		uri = "file:///" + filepath.ToSlash(dir)
	}

	s := &Server{Dir: t.TempDir(), Resolve: store.Stores} // started outside the repo
	res := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"jot-roots","result":{"roots":[{"uri":"file:///nowhere"},{"uri":"`+uri+`"}]}}`,
		call("2", "recall", `{"query":"api port"}`),
	)
	if len(res) != 3 {
		t.Fatalf("want initialize reply, roots request, recall reply; got %d: %v", len(res), res)
	}
	if res[1]["method"] != "roots/list" {
		t.Fatalf("want a roots/list request, got %v", res[1])
	}
	if got := text(res[2]); !strings.Contains(got, "8088") || !strings.Contains(got, "from project memory") {
		t.Fatalf("recall after roots: %s", got)
	}
}

func TestNoRootsRequestWithoutCapability(t *testing.T) {
	s := &Server{Dir: t.TempDir(), Resolve: store.Stores}
	res := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if len(res) != 1 {
		t.Fatalf("want only the initialize reply, got %v", res)
	}
}

func TestFileURIPath(t *testing.T) {
	for uri, want := range map[string]string{
		"file:///home/me/repo":  filepath.FromSlash("/home/me/repo"),
		"file:///C:/Users/me/x": filepath.FromSlash("C:/Users/me/x"),
		"file:///tmp/a%20b":     filepath.FromSlash("/tmp/a b"),
	} {
		if got, ok := fileURIPath(uri); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", uri, got, ok, want)
		}
	}
	for _, bad := range []string{"https://x/y", "file://", "::"} {
		if _, ok := fileURIPath(bad); ok {
			t.Errorf("%s: want not ok", bad)
		}
	}
}

// With per-call store resolution (the way jot serve runs), chats in the
// global store's chats/ folder still never reach recall or list.
func TestResolvedStoresSkipChats(t *testing.T) {
	t.Setenv("JOT_HOME", filepath.Join(t.TempDir(), "global"))
	g := store.Open(store.GlobalDir(), "global")
	if _, err := g.Add("Run the Go tests with -race everywhere.", "preference", nil, "test"); err != nil {
		t.Fatal(err)
	}
	chat := "---\nid: c1\ntitle: go tests\n---\n\n### You\n<!-- jot:turn role=user at=2026-11-08T00:00:00Z -->\n\nhow do I run the go tests? quokka-marker\n"
	if err := g.WriteFile("chats/c1.md", []byte(chat), "ask c1"); err != nil {
		t.Fatal(err)
	}
	dir := repo(t, "This repo's Go tests need TEST_DB_URL set.")
	p := store.Open(filepath.Join(dir, store.DirName), "project")
	if err := p.WriteFile("chats/c2.md", []byte(strings.Replace(chat, "c1", "c2", 1)), "ask c2"); err != nil {
		t.Fatal(err)
	}
	s := &Server{Dir: dir, Resolve: store.Stores}
	res := rpc(t, s, call("1", "recall", `{"query":"go tests quokka-marker"}`), call("2", "list", `{}`))
	for i, r := range res {
		got := text(r)
		if strings.Contains(got, "quokka") || !strings.Contains(got, "TEST_DB_URL") || !strings.Contains(got, "-race") {
			t.Fatalf("response %d:\n%s", i, got)
		}
	}
}
