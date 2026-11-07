// Package chat talks to language models for jot's Ask feature. It knows
// three wire protocols, each streaming its reply as it is written:
//
//   - Ollama's /api/chat (newline-delimited JSON), for local models.
//   - OpenAI-compatible /v1/chat/completions (server-sent events). The base
//     URL is configurable, so the same client covers OpenAI, OpenRouter,
//     Groq, LM Studio and anything else that speaks that dialect.
//   - Anthropic's /v1/messages (server-sent events).
//
// It only moves text. Recall, prompts and chat history live in package ask.
package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Roles a message can have.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn sent to a model.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is one completion: a model name (without the provider prefix)
// and the conversation so far. System messages may appear anywhere;
// providers that want a single system prompt join them.
type Request struct {
	Model     string
	Messages  []Message
	MaxTokens int // 0 means the provider's default
}

// Provider is one model service.
type Provider interface {
	// Name is the provider's id: "ollama", "openai" or "anthropic".
	Name() string
	// Models lists the chat models the service offers right now.
	Models(ctx context.Context) ([]string, error)
	// Stream sends req and calls onDelta with each piece of the reply as it
	// arrives. It returns the whole reply. When ctx is cancelled it stops
	// and returns what it has so far with ctx.Err().
	Stream(ctx context.Context, req Request, onDelta func(string)) (string, error)
}

// Provider ids.
const (
	Ollama    = "ollama"
	OpenAI    = "openai"
	Anthropic = "anthropic"
)

// Default addresses.
const (
	DefaultOllamaURL    = "http://localhost:11434"
	DefaultOpenAIURL    = "https://api.openai.com/v1"
	DefaultAnthropicURL = "https://api.anthropic.com/v1"
)

// Providers lists every provider id, in the order the UI shows them.
var Providers = []string{Ollama, OpenAI, Anthropic}

// ErrUnauthorized is wrapped by errors caused by a missing or rejected key.
var ErrUnauthorized = errors.New("the API key was rejected")

// ErrNoKey is returned before any request when a provider needs a key and
// none is set.
var ErrNoKey = errors.New("no API key set")

// Error is a failed request, with the service's own message.
type Error struct {
	Provider string
	Status   int // HTTP status, 0 when the request never got an answer
	Message  string
}

func (e *Error) Error() string {
	name := label(e.Provider)
	if e.Status == 0 {
		return name + ": " + e.Message
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", name, e.Message, e.Status)
}

// Unwrap lets errors.Is(err, ErrUnauthorized) see auth failures.
func (e *Error) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return ErrUnauthorized
	}
	return nil
}

func label(p string) string {
	switch p {
	case Ollama:
		return "Ollama"
	case OpenAI:
		return "OpenAI-compatible"
	case Anthropic:
		return "Anthropic"
	}
	return p
}

// Ref names a model on a provider. Its string form is "provider/model",
// e.g. "ollama/qwen2.5:0.5b" or "openai/anthropic/claude-3.5-sonnet" (an
// OpenRouter model, whose own name has a slash).
type Ref struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (r Ref) String() string {
	if r.Provider == "" {
		return r.Model
	}
	return r.Provider + "/" + r.Model
}

// ParseRef reads a model reference. A leading "ollama/", "openai/" or
// "anthropic/" picks the provider. Without one, claude-* means Anthropic,
// gpt-*, chatgpt-* and o1/o3/o4 mean OpenAI, and anything else is taken to
// be a local Ollama model.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, errors.New("empty model name")
	}
	if p, m, ok := strings.Cut(s, "/"); ok {
		for _, id := range Providers {
			if strings.EqualFold(p, id) {
				if m == "" {
					return Ref{}, fmt.Errorf("model %q has no name after the provider", s)
				}
				return Ref{Provider: id, Model: m}, nil
			}
		}
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "claude"):
		return Ref{Provider: Anthropic, Model: s}, nil
	case strings.HasPrefix(low, "gpt-"), strings.HasPrefix(low, "chatgpt-"),
		low == "o1" || low == "o3" || low == "o4-mini" ||
			strings.HasPrefix(low, "o1-") || strings.HasPrefix(low, "o3-") || strings.HasPrefix(low, "o4-"):
		return Ref{Provider: OpenAI, Model: s}, nil
	}
	return Ref{Provider: Ollama, Model: s}, nil
}

// Short is how a model is named in a conversation: just the model.
func (r Ref) Short() string { return r.Model }

// mergeSystem splits msgs into one system prompt and the rest, joining
// consecutive turns of the same role (Anthropic rejects two user turns in a
// row, and a failed reply can leave one).
func mergeSystem(msgs []Message) (string, []Message) {
	var sys []string
	var out []Message
	for _, m := range msgs {
		if m.Role == RoleSystem {
			if strings.TrimSpace(m.Content) != "" {
				sys = append(sys, m.Content)
			}
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	return strings.Join(sys, "\n\n"), out
}

// cancelled turns a read error into ctx.Err() when the caller stopped it.
func cancelled(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
