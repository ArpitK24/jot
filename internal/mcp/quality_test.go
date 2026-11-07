package mcp

import (
	"strings"
	"testing"

	"github.com/ArpitK24/jot/internal/store"
)

func TestCheckMemory(t *testing.T) {
	bad := map[string]string{
		"":                                     "empty",
		"   \n ":                               "empty",
		strings.Repeat("word ", 130):           "too long",
		"Deploy key: AKIAIOSFODNN7EXAMPLE":     "AWS",
		"token ghp_" + strings.Repeat("a", 36): "GitHub",
		"OpenAI sk-proj-" + strings.Repeat("Ab1", 10):        "API key",
		"Anthropic sk-ant-api03-" + strings.Repeat("x9", 15): "API key",
		"stripe sk_live_" + strings.Repeat("4e", 12):         "API key",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc":           "private key",
		"The staging db password is hunter22":                "password",
		"PASSWORD=S3cret!x":                                  "password",
		"api_key = 'abcd1234abcd1234abcd'":                   "secret value",
		"DB at postgres://admin:pa55word@db.local:5432/app":  "URL with a password",
		"auth eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": "JSON web token",
	}
	for text, why := range bad {
		if err := checkMemory(text); err == nil {
			t.Errorf("%s: accepted %q", why, text)
		}
	}
	good := []string{
		"Use pnpm, never npm, in JS projects.",
		"Password is required on the admin login; the policy is at least 12 characters.",
		"The password: field must be masked in the signup form.",
		"Store API keys in 1Password under 'dev', never in .env files committed to git.",
		"Run tests with `go test ./...`; the task-runner-config lives in Makefile.",
		"Commit SHAs in changelogs are 7 characters, e.g. 3f4c6d3.",
		strings.Repeat("a", MaxMemoryLen),
	}
	for _, text := range good {
		if err := checkMemory(text); err != nil {
			t.Errorf("rejected %q: %v", text, err)
		}
	}
}

func TestRememberRefusesBadInput(t *testing.T) {
	st := store.Open(t.TempDir(), "global")
	s := &Server{Stores: []*store.Store{st}, Source: "test"}
	res := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember","arguments":{"text":"  "}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"remember","arguments":{"text":"GitHub token ghp_abcdefghijklmnopqrstuvwxyz0123456789"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remember","arguments":{"text":"`+strings.Repeat("long ", 200)+`","force":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"remember","arguments":{"text":"Release checklist:\n- bump version\n- run tests\n- tag"}}}`,
	)
	for i, want := range []string{"text is empty", "contains a GitHub token", "too long for one memory"} {
		r := res[i]["result"].(map[string]any)
		if r["isError"] != true || !strings.Contains(text(res[i]), want) {
			t.Errorf("call %d: want error with %q, got %v", i+1, want, r)
		}
	}
	if got := text(res[3]); !strings.HasPrefix(got, "Remembered") || !strings.Contains(got, "Tip: this holds 3 separate points") {
		t.Errorf("tip: %s", got)
	}
	if ms, _ := st.List(); len(ms) != 1 {
		t.Fatalf("want only the list memory saved, got %d", len(ms))
	}
}

func TestUpdateRefusesSecret(t *testing.T) {
	st := store.Open(t.TempDir(), "global")
	m, err := st.Add("Use the shared staging database.", "fact", nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Stores: []*store.Store{st}}
	res := rpc(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update","arguments":{"id":"`+m.ID+`","text":"staging password: Winter2026!"}}}`)
	if got := text(res[0]); !strings.HasPrefix(got, "Not updated") {
		t.Fatalf("update: %s", got)
	}
	if got, _ := st.Get(m.ID); got.Body != m.Body {
		t.Fatalf("body changed to %q", got.Body)
	}
}
