package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const configFile = "config.json"

// Config is the chat setup kept in the global store's config.json under
// "chat". It holds no secrets: the store is a git repo, so API keys live in
// a separate file (see Keys).
type Config struct {
	Default   string          `json:"default,omitempty"` // model ref, e.g. "ollama/qwen2.5:0.5b"
	Ollama    OllamaConfig    `json:"ollama,omitempty"`
	OpenAI    OpenAIConfig    `json:"openai,omitempty"`
	Anthropic AnthropicConfig `json:"anthropic,omitempty"`
}

// OllamaConfig points at a local Ollama.
type OllamaConfig struct {
	URL string `json:"url,omitempty"`
}

// OpenAIConfig points at any OpenAI-compatible server.
type OpenAIConfig struct {
	BaseURL string   `json:"base_url,omitempty"`
	Models  []string `json:"models,omitempty"` // optional allow-list for the model picker
}

// AnthropicConfig points at the Anthropic API.
type AnthropicConfig struct {
	BaseURL string `json:"base_url,omitempty"`
}

// ReadConfig reads the chat config exactly as saved: no defaults and no
// environment overrides. Edit this and pass it to SaveConfig.
func ReadConfig(globalDir string) Config {
	var file struct {
		Chat Config `json:"chat"`
	}
	if b, err := os.ReadFile(filepath.Join(globalDir, configFile)); err == nil {
		_ = json.Unmarshal(b, &file)
	}
	return file.Chat
}

// LoadConfig reads the chat config from globalDir. A missing file is an
// empty config. JOT_CHAT_MODEL overrides the default model, and when no
// Ollama address is set the one from JOT_OLLAMA_URL or semantic search's
// "embed" config is used, so one Ollama serves both.
func LoadConfig(globalDir string) Config {
	var file struct {
		Chat  Config `json:"chat"`
		Embed struct {
			URL string `json:"url"`
		} `json:"embed"`
	}
	if b, err := os.ReadFile(filepath.Join(globalDir, configFile)); err == nil {
		_ = json.Unmarshal(b, &file)
	}
	c := file.Chat
	if m := strings.TrimSpace(os.Getenv("JOT_CHAT_MODEL")); m != "" {
		c.Default = m
	}
	if c.Ollama.URL == "" {
		if u := os.Getenv("JOT_OLLAMA_URL"); u != "" {
			c.Ollama.URL = u
		} else {
			c.Ollama.URL = file.Embed.URL
		}
	}
	return c.withDefaults()
}

func (c Config) withDefaults() Config {
	trim := func(s, def string) string {
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		if s == "" {
			return def
		}
		return s
	}
	c.Ollama.URL = trim(c.Ollama.URL, DefaultOllamaURL)
	c.OpenAI.BaseURL = trim(c.OpenAI.BaseURL, DefaultOpenAIURL)
	c.Anthropic.BaseURL = trim(c.Anthropic.BaseURL, DefaultAnthropicURL)
	return c
}

// SaveConfig writes c into globalDir/config.json under "chat", keeping
// every other key. Default addresses are left out so the file stays short.
func SaveConfig(globalDir string, c Config) error {
	path := filepath.Join(globalDir, configFile)
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s is not valid JSON: %v", path, err)
		}
	}
	c = c.withDefaults()
	if c.Ollama.URL == DefaultOllamaURL {
		c.Ollama.URL = ""
	}
	if c.OpenAI.BaseURL == DefaultOpenAIURL {
		c.OpenAI.BaseURL = ""
	}
	if c.Anthropic.BaseURL == DefaultAnthropicURL {
		c.Anthropic.BaseURL = ""
	}
	doc["chat"] = c
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------- keys

// Keys holds API keys in their own file outside every store, readable only
// by the user. Environment variables are used when no key is saved.
type Keys struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// KeysPath is <user config dir>/jot/keys.json.
func KeysPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "jot", "keys.json")
}

// envKeys are read when nothing is saved for a provider.
var envKeys = map[string]string{OpenAI: "OPENAI_API_KEY", Anthropic: "ANTHROPIC_API_KEY"}

// EnvName is the environment variable a provider's key can come from.
func EnvName(provider string) string { return envKeys[provider] }

// LoadKeys reads the key file at path (it need not exist).
func LoadKeys(path string) *Keys {
	k := &Keys{path: path, m: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &k.m)
	}
	return k
}

// Get returns the key for provider and where it came from: "saved", the
// environment variable's name, or "" when there is none.
func (k *Keys) Get(provider string) (key, source string) {
	k.mu.Lock()
	v := strings.TrimSpace(k.m[provider])
	k.mu.Unlock()
	if v != "" {
		return v, "saved"
	}
	if env := envKeys[provider]; env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, env
		}
	}
	return "", ""
}

// Set saves (or, with an empty key, removes) the key for provider. The
// file is written with 0600 permissions.
func (k *Keys) Set(provider, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key = strings.TrimSpace(key)
	if key == "" {
		delete(k.m, provider)
	} else {
		k.m[provider] = key
	}
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(k.m, "", "  ")
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600) // WriteFile keeps the mode of a file that existed
	return os.Rename(tmp, k.path)
}

// Mask shows enough of a key to recognise it and no more.
func Mask(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 10 {
		return "••••"
	}
	prefix := ""
	if i := strings.IndexAny(key, "-_"); i > 0 && i <= 7 {
		prefix = key[:i+1]
	}
	return prefix + "••••" + key[len(key)-4:]
}

// ---------- registry

// Registry builds providers from the config and keys.
type Registry struct {
	Config Config
	Keys   *Keys
}

// NewRegistry loads the config from globalDir and the keys from KeysPath().
func NewRegistry(globalDir string) *Registry {
	return &Registry{Config: LoadConfig(globalDir), Keys: LoadKeys(KeysPath())}
}

func (r *Registry) key(p string) string {
	if r.Keys == nil {
		return ""
	}
	k, _ := r.Keys.Get(p)
	return k
}

// Provider returns the client for a provider id.
func (r *Registry) Provider(id string) (Provider, error) {
	switch id {
	case Ollama:
		return &OllamaProvider{URL: r.Config.Ollama.URL}, nil
	case OpenAI:
		return &OpenAIProvider{BaseURL: r.Config.OpenAI.BaseURL, Key: r.key(OpenAI), Allow: r.Config.OpenAI.Models}, nil
	case Anthropic:
		return &AnthropicProvider{BaseURL: r.Config.Anthropic.BaseURL, Key: r.key(Anthropic)}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", id)
}

// Resolve parses a model ref and returns its provider.
func (r *Registry) Resolve(ref string) (Provider, Ref, error) {
	m, err := ParseRef(ref)
	if err != nil {
		return nil, Ref{}, err
	}
	p, err := r.Provider(m.Provider)
	return p, m, err
}

// Configured reports whether a provider has what it needs to be tried:
// Ollama always (it is local), OpenAI-compatible with a key or a custom
// base URL (local servers need none), Anthropic with a key.
func (r *Registry) Configured(id string) bool {
	switch id {
	case Ollama:
		return true
	case OpenAI:
		return r.key(OpenAI) != "" || r.Config.OpenAI.BaseURL != DefaultOpenAIURL
	case Anthropic:
		return r.key(Anthropic) != ""
	}
	return false
}

// Status is one provider's state for a settings screen or model picker.
type Status struct {
	Provider   string   `json:"provider"`
	Label      string   `json:"label"`
	URL        string   `json:"url"`
	Configured bool     `json:"configured"`
	Ready      bool     `json:"ready"`
	Detail     string   `json:"detail"` // why it isn't ready
	Models     []string `json:"models"`
	Key        string   `json:"key"`       // masked
	KeySource  string   `json:"keySource"` // "saved", an env var name, or ""
}

// Check asks one provider for its models.
func (r *Registry) Check(ctx context.Context, id string) Status {
	st := Status{Provider: id, Label: label(id), Configured: r.Configured(id), Models: []string{}}
	switch id {
	case Ollama:
		st.URL = r.Config.Ollama.URL
	case OpenAI:
		st.URL = r.Config.OpenAI.BaseURL
	case Anthropic:
		st.URL = r.Config.Anthropic.BaseURL
	}
	if r.Keys != nil && id != Ollama {
		k, src := r.Keys.Get(id)
		st.Key, st.KeySource = Mask(k), src
	}
	if !st.Configured {
		st.Detail = "Add an API key to use it."
		return st
	}
	p, err := r.Provider(id)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ms, err := p.Models(ctx)
	if err != nil {
		st.Detail = strings.TrimPrefix(err.Error(), label(id)+": ")
		if errors.Is(err, ErrUnauthorized) {
			st.Detail = "The key was rejected. Check it and try again."
		}
		return st
	}
	st.Ready, st.Models = true, ms
	if id == Ollama && len(ms) == 0 {
		st.Detail = "Ollama is running but has no chat model. Try: ollama pull qwen2.5:3b"
	}
	return st
}

// CheckAll checks every provider at once.
func (r *Registry) CheckAll(ctx context.Context) []Status {
	out := make([]Status, len(Providers))
	var wg sync.WaitGroup
	for i, id := range Providers {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			out[i] = r.Check(ctx, id)
		}(i, id)
	}
	wg.Wait()
	return out
}

// ErrNoModel means no provider has a model to use.
var ErrNoModel = errors.New("no chat model available: start Ollama and pull a model (ollama pull qwen2.5:3b), or add an OpenAI or Anthropic key")

// DefaultModel is the configured default, or else the first model of the
// first provider that answers.
func (r *Registry) DefaultModel(ctx context.Context, statuses []Status) (string, error) {
	if r.Config.Default != "" {
		return r.Config.Default, nil
	}
	if statuses == nil {
		statuses = r.CheckAll(ctx)
	}
	for _, st := range statuses {
		if st.Ready && len(st.Models) > 0 {
			return Ref{Provider: st.Provider, Model: st.Models[0]}.String(), nil
		}
	}
	return "", ErrNoModel
}
