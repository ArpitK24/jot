package mcp

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ArpitK24/jot/internal/store"
)

func rpc(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := s.Serve(strings.NewReader(strings.Join(lines, "\n")), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		res = append(res, m)
	}
	return res
}

func text(r map[string]any) string {
	c := r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
	return c["text"].(string)
}

func TestProtocol(t *testing.T) {
	s := &Server{Stores: []*store.Store{store.Open(t.TempDir(), "global")}, Version: "test", Source: "test"}
	res := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"text":"Prefers tabs over spaces in Go","kind":"preference","tags":["go"]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"tabs or spaces?"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"nope"}`,
		`not json`,
	)
	if len(res) != 6 {
		t.Fatalf("want 6 responses (notification gets none), got %d", len(res))
	}
	if res[0]["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "jot" {
		t.Fatal("bad initialize")
	}
	if n := len(res[1]["result"].(map[string]any)["tools"].([]any)); n != 5 {
		t.Fatalf("want 5 tools, got %d", n)
	}
	if !strings.HasPrefix(text(res[2]), "Remembered ") {
		t.Fatalf("remember: %s", text(res[2]))
	}
	if !strings.Contains(text(res[3]), "Prefers tabs over spaces") {
		t.Fatalf("recall: %s", text(res[3]))
	}
	if res[4]["error"] == nil || res[5]["error"] == nil {
		t.Fatal("expected errors for unknown method and bad json")
	}
}

func TestToolErrors(t *testing.T) {
	s := &Server{Stores: []*store.Store{store.Open(t.TempDir(), "global")}}
	res := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember","arguments":{"text":"x","scope":"project"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"forget","arguments":{"id":"missing"}}}`,
	)
	for _, r := range res {
		if r["result"].(map[string]any)["isError"] != true {
			t.Fatalf("want tool error, got %v", r)
		}
	}
}

func TestRememberCatchesDuplicates(t *testing.T) {
	s := &Server{Stores: []*store.Store{store.Open(t.TempDir(), "global")}, Source: "test"}
	call := func(id int, args string) string {
		return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"remember","arguments":` + args + `}}`
	}
	res := rpc(t, s,
		call(1, `{"text":"Use pnpm for every JS project. Never commit package-lock.json."}`),
		call(2, `{"text":"Use pnpm for every JS project, and never commit package-lock.json files."}`),
		call(3, `{"text":"Use pnpm for every JS project, and never commit package-lock.json files.","force":true}`),
	)
	if !strings.HasPrefix(text(res[0]), "Remembered") {
		t.Fatalf("first: %s", text(res[0]))
	}
	if got := text(res[1]); !strings.HasPrefix(got, "Not saved") || !strings.Contains(got, "call update with id") {
		t.Fatalf("duplicate: %s", got)
	}
	if !strings.HasPrefix(text(res[2]), "Remembered") {
		t.Fatalf("forced: %s", text(res[2]))
	}
	if ms, _ := s.Stores[0].List(); len(ms) != 2 {
		t.Fatalf("want 2 memories, got %d", len(ms))
	}
}
