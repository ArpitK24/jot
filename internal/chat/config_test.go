package chat

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------- config, keys, registry

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JOT_CHAT_MODEL", "")
	t.Setenv("JOT_OLLAMA_URL", "")
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"embed":{"provider":"ollama","url":"http://box:11434"}}`), 0o644)
	c := LoadConfig(dir)
	if c.Ollama.URL != "http://box:11434" || c.OpenAI.BaseURL != DefaultOpenAIURL {
		t.Fatalf("defaults: %+v", c)
	}
	c.Default = "ollama/qwen2.5:0.5b"
	c.OpenAI.BaseURL = "https://openrouter.ai/api/v1/"
	if err := SaveConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(b), `"embed"`) || strings.Contains(string(b), "api.anthropic.com") {
		t.Fatalf("saved: %s", b)
	}
	c2 := LoadConfig(dir)
	if c2.Default != c.Default || c2.OpenAI.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("reload: %+v", c2)
	}
	t.Setenv("JOT_CHAT_MODEL", "gpt-4o")
	if LoadConfig(dir).Default != "gpt-4o" {
		t.Fatal("JOT_CHAT_MODEL ignored")
	}
}

func TestKeys(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-env-1234")
	path := filepath.Join(t.TempDir(), "jot", "keys.json")
	k := LoadKeys(path)
	if v, src := k.Get(Anthropic); v != "sk-ant-from-env-1234" || src != "ANTHROPIC_API_KEY" {
		t.Fatalf("env: %q %q", v, src)
	}
	if v, _ := k.Get(OpenAI); v != "" {
		t.Fatal("unexpected openai key")
	}
	if err := k.Set(OpenAI, "  sk-proj-abcdefghijklmnop  "); err != nil {
		t.Fatal(err)
	}
	if err := k.Set(Anthropic, "sk-ant-saved-99999999"); err != nil {
		t.Fatal(err)
	}
	k2 := LoadKeys(path)
	if v, src := k2.Get(OpenAI); v != "sk-proj-abcdefghijklmnop" || src != "saved" {
		t.Fatalf("saved: %q %q", v, src)
	}
	if v, src := k2.Get(Anthropic); v != "sk-ant-saved-99999999" || src != "saved" {
		t.Fatalf("saved wins over env: %q %q", v, src)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("perms: %v %v", fi.Mode(), err)
		}
	}
	k2.Set(OpenAI, "")
	if v, _ := LoadKeys(path).Get(OpenAI); v != "" {
		t.Fatal("key not removed")
	}
	if m := Mask("sk-proj-abcdefghijklmnop"); m != "sk-••••mnop" || strings.Contains(m, "abcdef") {
		t.Fatalf("mask: %q", m)
	}
	if Mask("short") != "••••" || Mask("") != "" {
		t.Fatal("mask short")
	}
}

func TestRegistry(t *testing.T) {
	srv := ollamaFake(t, func(w http.ResponseWriter, body map[string]any) {})
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	r := &Registry{Config: Config{Ollama: OllamaConfig{URL: srv.URL}}.withDefaults(), Keys: LoadKeys(filepath.Join(t.TempDir(), "k.json"))}
	sts := r.CheckAll(context.Background())
	if !sts[0].Ready || len(sts[0].Models) != 2 || sts[1].Configured || sts[2].Configured {
		t.Fatalf("statuses: %+v", sts)
	}
	m, err := r.DefaultModel(context.Background(), sts)
	if err != nil || m != "ollama/llama3.2:1b" {
		t.Fatalf("default: %q %v", m, err)
	}
	r.Config.Default = "claude-x"
	if m, _ := r.DefaultModel(context.Background(), sts); m != "claude-x" {
		t.Fatalf("configured default: %q", m)
	}
	p, ref, err := r.Resolve("gpt-4o-mini")
	if err != nil || p.Name() != OpenAI || ref.Model != "gpt-4o-mini" {
		t.Fatalf("resolve: %v %v %v", p, ref, err)
	}
	r.Config.Ollama.URL = "http://127.0.0.1:1"
	r.Config.Default = ""
	if _, err := r.DefaultModel(context.Background(), nil); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no model: %v", err)
	}
}
