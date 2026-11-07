package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// askEnv points jot at a scratch store, a scratch key file and a fake
// Ollama that answers with the notes it was shown.
func askEnv(t *testing.T) (sys *[]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	t.Setenv("JOT_HOME", filepath.Join(home, ".jot"))
	t.Setenv("JOT_EMBED", "off")
	t.Setenv("JOT_CHAT_MODEL", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	chdir(t, t.TempDir())
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"tiny:1b","details":{"family":"llama"}},{"name":"all-minilm","details":{"family":"bert"}}]}`)
		case "/api/chat":
			var body struct {
				Messages []struct{ Role, Content string }
			}
			json.NewDecoder(r.Body).Decode(&body)
			prompts = append(prompts, body.Messages[0].Content)
			for _, p := range []string{"You use ", "pnpm."} {
				fmt.Fprintf(w, `{"message":{"content":%q},"done":false}`+"\n", p)
			}
			fmt.Fprint(w, `{"done":true}`+"\n")
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("JOT_OLLAMA_URL", srv.URL)
	stderr = &bytes.Buffer{}
	t.Cleanup(func() { stderr = os.Stderr })
	return &prompts
}

func TestAskCLI(t *testing.T) {
	prompts := askEnv(t)
	jot(t, "", "init")
	id := strings.Fields(jot(t, "", "add", "-k", "preference", "Use pnpm, never npm"))[1]
	jot(t, "", "add", "Deploy on merge to main")

	out := jot(t, "", "ask", "which", "package", "manager", "do", "I", "use?")
	if out != "You use pnpm.\n" {
		t.Fatalf("answer: %q", out)
	}
	errs := stderr.(*bytes.Buffer).String()
	if !strings.Contains(errs, "ollama/tiny:1b, used 1 note") || !strings.Contains(errs, id) {
		t.Fatalf("stderr: %s", errs)
	}
	if !strings.Contains((*prompts)[0], "["+id+"]") || strings.Contains((*prompts)[0], "Deploy on merge") {
		t.Fatalf("prompt:\n%s", (*prompts)[0])
	}

	// The chat is saved, listed, and not a note.
	list := jot(t, "", "chats")
	if !strings.Contains(list, "which package manager do I use?") || !strings.Contains(list, "2 turns") {
		t.Fatalf("chats: %q", list)
	}
	chatID := strings.Fields(list)[0]
	if out := jot(t, "", "ls"); strings.Contains(out, "which package") || strings.Count(out, "\n") != 2 {
		t.Fatalf("chat listed as a note: %q", out)
	}
	if out := jot(t, "", "search", "package", "manager"); strings.Contains(out, chatID) {
		t.Fatalf("chat found by search: %q", out)
	}

	// Continue it, without recall, on a named model.
	jot(t, "", "ask", "-c", chatID, "--no-recall", "-m", "ollama/tiny:1b", "and", "for", "deploys?")
	if strings.Contains((*prompts)[1], id) {
		t.Fatal("--no-recall still sent notes")
	}
	if out := jot(t, "", "chats", "show", chatID); strings.Count(out, "### You") != 2 || !strings.Contains(out, "### tiny:1b") {
		t.Fatalf("show: %s", out)
	}

	// Errors.
	for _, args := range [][]string{{"ask"}, {"ask", "--bogus", "x"}, {"ask", "-c", "nope", "hi"}, {"ask", "-m"}, {"chats", "show", "nope"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("jot %v: want error", args)
		}
	}
	jot(t, "", "chats", "rm", chatID)
	if out := jot(t, "", "chats"); !strings.Contains(out, "no chats yet") {
		t.Fatalf("after rm: %q", out)
	}
}

func TestModelsCLI(t *testing.T) {
	askEnv(t)
	jot(t, "", "init")
	out := jot(t, "", "models")
	if !strings.Contains(out, "* ollama/tiny:1b") || strings.Contains(out, "all-minilm") || !strings.Contains(out, "Add an API key") {
		t.Fatalf("models:\n%s", out)
	}
	jot(t, "", "models", "default", "gpt-4o-mini")
	if out := jot(t, "", "models"); !strings.Contains(out, "default: openai/gpt-4o-mini") {
		t.Fatalf("default:\n%s", out)
	}
	if out := jot(t, "sk-proj-abcdefghijkl1234\n", "models", "key", "openai"); !strings.Contains(out, "sk-••••1234") || strings.Contains(out, "abcdef") {
		t.Fatalf("key: %q", out)
	}
	keys := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "jot", "keys.json")
	if runtime.GOOS == "darwin" {
		keys = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "jot", "keys.json")
	} else if runtime.GOOS == "windows" {
		keys = filepath.Join(os.Getenv("APPDATA"), "jot", "keys.json")
	}
	if b, err := os.ReadFile(keys); err != nil || !strings.Contains(string(b), "sk-proj-abcdefghijkl1234") {
		t.Fatalf("key file %s: %s %v", keys, b, err)
	}
	// The key never lands in the store, which is a git repo.
	cfg, _ := os.ReadFile(filepath.Join(os.Getenv("JOT_HOME"), "config.json"))
	if strings.Contains(string(cfg), "sk-proj") {
		t.Fatal("key written to the store")
	}
	jot(t, "", "models", "url", "openai", "https://openrouter.ai/api/v1")
	if cfg, _ := os.ReadFile(filepath.Join(os.Getenv("JOT_HOME"), "config.json")); !strings.Contains(string(cfg), "openrouter.ai") {
		t.Fatalf("url not saved: %s", cfg)
	}
	if err := run([]string{"models", "key", "ollama"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("ollama key accepted")
	}
}
