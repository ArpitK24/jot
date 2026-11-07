package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// ---------- Ollama

// OllamaProvider talks to a local Ollama server.
type OllamaProvider struct {
	URL string // e.g. http://localhost:11434
}

// Name implements Provider.
func (p *OllamaProvider) Name() string { return Ollama }

// Models lists installed models, leaving out embedding-only ones.
func (p *OllamaProvider) Models(ctx context.Context) ([]string, error) {
	raw, err := get(ctx, Ollama, p.URL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Models []struct {
			Name    string `json:"name"`
			Details struct {
				Family   string   `json:"family"`
				Families []string `json:"families"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, &Error{Provider: Ollama, Message: "unexpected reply from /api/tags"}
	}
	out := []string{}
	for _, m := range r.Models {
		if isEmbedding(m.Name, append(m.Details.Families, m.Details.Family)) {
			continue
		}
		out = append(out, m.Name)
	}
	sort.Strings(out)
	return out, nil
}

// isEmbedding guesses whether an Ollama model only makes embeddings: BERT
// families and the usual embedding names can't chat.
func isEmbedding(name string, families []string) bool {
	low := strings.ToLower(name)
	if strings.Contains(low, "embed") || strings.Contains(low, "minilm") || strings.Contains(low, "bge-") {
		return true
	}
	for _, f := range families {
		if strings.Contains(strings.ToLower(f), "bert") {
			return true
		}
	}
	return false
}

// Stream implements Provider with /api/chat, which streams one JSON object
// per line.
func (p *OllamaProvider) Stream(ctx context.Context, req Request, onDelta func(string)) (string, error) {
	sys, msgs := mergeSystem(req.Messages)
	if sys != "" {
		msgs = append([]Message{{Role: RoleSystem, Content: sys}}, msgs...)
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "stream": true}
	if req.MaxTokens > 0 {
		body["options"] = map[string]any{"num_predict": req.MaxTokens}
	}
	resp, err := post(ctx, Ollama, p.URL+"/api/chat", body, nil)
	if err != nil {
		return "", notFoundHint(err, req.Model)
	}
	defer resp.Body.Close()
	var out strings.Builder
	dec := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := dec.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				if ctx.Err() != nil {
					return out.String(), ctx.Err()
				}
				return out.String(), &Error{Provider: Ollama, Message: "the reply ended early"}
			}
			return out.String(), cancelled(ctx, &Error{Provider: Ollama, Message: "reading the reply: " + err.Error()})
		}
		if chunk.Error != "" {
			return out.String(), &Error{Provider: Ollama, Message: chunk.Error}
		}
		if chunk.Message.Content != "" {
			out.WriteString(chunk.Message.Content)
			onDelta(chunk.Message.Content)
		}
		if chunk.Done {
			return out.String(), nil
		}
	}
}

func notFoundHint(err error, model string) error {
	var e *Error
	if errors.As(err, &e) && e.Status == 404 && strings.Contains(e.Message, "not found") {
		e.Message += " (run: ollama pull " + model + ")"
	}
	return err
}

// ---------- OpenAI-compatible

// OpenAIProvider talks to any /v1/chat/completions server.
type OpenAIProvider struct {
	BaseURL string // up to and including /v1, e.g. https://api.openai.com/v1
	Key     string // may be empty for local servers such as LM Studio
	// Allow, when set, limits Models to these names. Useful for services
	// that list hundreds of models, such as OpenRouter.
	Allow []string
}

// Name implements Provider.
func (p *OpenAIProvider) Name() string { return OpenAI }

func (p *OpenAIProvider) headers() map[string]string {
	h := map[string]string{}
	if p.Key != "" {
		h["Authorization"] = "Bearer " + p.Key
	}
	if strings.Contains(p.BaseURL, "openrouter.ai") {
		h["HTTP-Referer"] = "https://github.com/ArpitK24/jot"
		h["X-Title"] = "jot"
	}
	return h
}

func (p *OpenAIProvider) official() bool { return strings.Contains(p.BaseURL, "api.openai.com") }

// Models lists /models. On api.openai.com only chat models are kept.
func (p *OpenAIProvider) Models(ctx context.Context) ([]string, error) {
	if p.official() && p.Key == "" {
		return nil, &Error{Provider: OpenAI, Message: ErrNoKey.Error()}
	}
	if len(p.Allow) > 0 {
		return append([]string{}, p.Allow...), nil
	}
	raw, err := get(ctx, OpenAI, p.BaseURL+"/models", p.headers())
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, &Error{Provider: OpenAI, Message: "unexpected reply from /models"}
	}
	out := []string{}
	for _, m := range r.Data {
		if p.official() && !openAIChatModel(m.ID) {
			continue
		}
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out, nil
}

func openAIChatModel(id string) bool {
	low := strings.ToLower(id)
	if !(strings.HasPrefix(low, "gpt-") || strings.HasPrefix(low, "chatgpt-") ||
		strings.HasPrefix(low, "o1") || strings.HasPrefix(low, "o3") || strings.HasPrefix(low, "o4")) {
		return false
	}
	for _, skip := range []string{"audio", "realtime", "tts", "transcribe", "image", "search", "instruct", "embedding"} {
		if strings.Contains(low, skip) {
			return false
		}
	}
	return true
}

// Stream implements Provider with a streamed chat completion.
func (p *OpenAIProvider) Stream(ctx context.Context, req Request, onDelta func(string)) (string, error) {
	if p.official() && p.Key == "" {
		return "", &Error{Provider: OpenAI, Message: ErrNoKey.Error()}
	}
	sys, msgs := mergeSystem(req.Messages)
	if sys != "" {
		msgs = append([]Message{{Role: RoleSystem, Content: sys}}, msgs...)
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "stream": true}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	resp, err := post(ctx, OpenAI, p.BaseURL+"/chat/completions", body, p.headers())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out strings.Builder
	var serr error
	done := false
	err = sse(resp.Body, func(_, data string) bool {
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			return false
		}
		var c struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &c) != nil {
			return true
		}
		if len(c.Error) > 0 && string(c.Error) != "null" {
			serr = &Error{Provider: OpenAI, Message: errorMessage([]byte(data), "the reply failed")}
			return false
		}
		for _, ch := range c.Choices {
			if ch.Delta.Content != "" {
				out.WriteString(ch.Delta.Content)
				onDelta(ch.Delta.Content)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				done = true
			}
		}
		return true
	})
	if serr != nil {
		return out.String(), serr
	}
	if err != nil {
		return out.String(), cancelled(ctx, &Error{Provider: OpenAI, Message: "reading the reply: " + err.Error()})
	}
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if !done {
		return out.String(), &Error{Provider: OpenAI, Message: "the reply ended early"}
	}
	return out.String(), nil
}

// ---------- Anthropic

// AnthropicModels is offered when the key can't list models (or there is
// no key yet). The live list from /v1/models replaces it when it answers.
var AnthropicModels = []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"}

const anthropicVersion = "2023-06-01"

// AnthropicProvider talks to the Anthropic Messages API.
type AnthropicProvider struct {
	BaseURL string // e.g. https://api.anthropic.com/v1
	Key     string
}

// Name implements Provider.
func (p *AnthropicProvider) Name() string { return Anthropic }

func (p *AnthropicProvider) headers() map[string]string {
	return map[string]string{"x-api-key": p.Key, "anthropic-version": anthropicVersion}
}

// Models lists /v1/models, or the built-in list when that fails for a
// reason other than a rejected key.
func (p *AnthropicProvider) Models(ctx context.Context) ([]string, error) {
	if p.Key == "" {
		return nil, &Error{Provider: Anthropic, Message: ErrNoKey.Error()}
	}
	raw, err := get(ctx, Anthropic, p.BaseURL+"/models?limit=100", p.headers())
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || ctx.Err() != nil {
			return nil, err
		}
		var e *Error
		if errors.As(err, &e) && e.Status == 0 {
			return nil, err // unreachable: say so rather than pretend
		}
		return append([]string{}, AnthropicModels...), nil
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Data) == 0 {
		return append([]string{}, AnthropicModels...), nil
	}
	out := make([]string, 0, len(r.Data))
	for _, m := range r.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// Stream implements Provider with a streamed Messages call.
func (p *AnthropicProvider) Stream(ctx context.Context, req Request, onDelta func(string)) (string, error) {
	if p.Key == "" {
		return "", &Error{Provider: Anthropic, Message: ErrNoKey.Error()}
	}
	sys, msgs := mergeSystem(req.Messages)
	// The first turn must be the user's.
	if len(msgs) > 0 && msgs[0].Role != RoleUser {
		msgs = append([]Message{{Role: RoleUser, Content: "(continuing an earlier conversation)"}}, msgs...)
	}
	max := req.MaxTokens
	if max <= 0 {
		max = 4096
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "max_tokens": max, "stream": true}
	if sys != "" {
		body["system"] = sys
	}
	resp, err := post(ctx, Anthropic, p.BaseURL+"/messages", body, p.headers())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out strings.Builder
	var serr error
	done := false
	err = sse(resp.Body, func(event, data string) bool {
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true
		}
		if ev.Type == "" {
			ev.Type = event
		}
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				out.WriteString(ev.Delta.Text)
				onDelta(ev.Delta.Text)
			}
		case "message_stop":
			done = true
			return false
		case "error":
			serr = &Error{Provider: Anthropic, Message: errorMessage([]byte(data), "the reply failed")}
			return false
		}
		return true
	})
	if serr != nil {
		return out.String(), serr
	}
	if err != nil {
		return out.String(), cancelled(ctx, &Error{Provider: Anthropic, Message: "reading the reply: " + err.Error()})
	}
	if ctx.Err() != nil {
		return out.String(), ctx.Err()
	}
	if !done {
		return out.String(), &Error{Provider: Anthropic, Message: "the reply ended early"}
	}
	return out.String(), nil
}
