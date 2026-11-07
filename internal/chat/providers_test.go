package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var hello = Request{Model: "m", Messages: []Message{
	{Role: RoleSystem, Content: "be brief"},
	{Role: RoleUser, Content: "hi"},
}}

func collect(t *testing.T, p Provider, ctx context.Context, req Request) (string, string, error) {
	t.Helper()
	var got strings.Builder
	full, err := p.Stream(ctx, req, func(d string) { got.WriteString(d) })
	return got.String(), full, err
}

// ---------- Ollama

func ollamaFake(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"qwen2.5:0.5b","details":{"family":"qwen2"}},{"name":"all-minilm:latest","details":{"family":"bert"}},{"name":"llama3.2:1b","details":{"family":"llama"}}]}`)
		case "/api/chat":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			h(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOllamaStream(t *testing.T) {
	var sent map[string]any
	srv := ollamaFake(t, func(w http.ResponseWriter, body map[string]any) {
		sent = body
		for _, p := range []string{"Hel", "lo", "!"} {
			fmt.Fprintf(w, `{"message":{"role":"assistant","content":%q},"done":false}`+"\n", p)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, `{"message":{"role":"assistant","content":""},"done":true}`+"\n")
	})
	p := &OllamaProvider{URL: srv.URL}
	deltas, full, err := collect(t, p, context.Background(), hello)
	if err != nil || deltas != "Hello!" || full != "Hello!" {
		t.Fatalf("got %q %q %v", deltas, full, err)
	}
	msgs := sent["messages"].([]any)
	if sent["stream"] != true || len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("request: %+v", sent)
	}
	ms, err := p.Models(context.Background())
	if err != nil || strings.Join(ms, ",") != "llama3.2:1b,qwen2.5:0.5b" {
		t.Fatalf("models: %v %v", ms, err)
	}
}

func TestOllamaErrors(t *testing.T) {
	srv := ollamaFake(t, func(w http.ResponseWriter, body map[string]any) {
		if body["model"] == "missing" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":"model \"missing\" not found, try pulling it first"}`)
			return
		}
		fmt.Fprint(w, `{"message":{"content":"par"},"done":false}`+"\n"+`{"error":"out of memory"}`+"\n")
	})
	p := &OllamaProvider{URL: srv.URL}
	_, _, err := collect(t, p, context.Background(), Request{Model: "missing", Messages: hello.Messages})
	var e *Error
	if !errors.As(err, &e) || e.Status != 404 || !strings.Contains(err.Error(), "ollama pull missing") {
		t.Fatalf("404: %v", err)
	}
	_, full, err := collect(t, p, context.Background(), hello)
	if err == nil || !strings.Contains(err.Error(), "out of memory") || full != "par" {
		t.Fatalf("mid-stream error: %q %v", full, err)
	}
	down := &OllamaProvider{URL: "http://127.0.0.1:1"}
	if _, err := down.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Fatalf("down: %v", err)
	}
}

// ---------- OpenAI-compatible

func openaiFake(t *testing.T, key string, h func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"gpt-4o-mini"},{"id":"meta/llama-3"}]}`)
		case "/v1/chat/completions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "text/event-stream")
			h(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sseChunk(w http.ResponseWriter, s string) {
	fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", s)
	w.(http.Flusher).Flush()
}

func TestOpenAIStream(t *testing.T) {
	var sent map[string]any
	srv := openaiFake(t, "sk-test", func(w http.ResponseWriter, body map[string]any) {
		sent = body
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`+"\n\n")
		sseChunk(w, "Use ")
		sseChunk(w, "pnpm.")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	p := &OpenAIProvider{BaseURL: srv.URL + "/v1", Key: "sk-test"}
	deltas, full, err := collect(t, p, context.Background(), hello)
	if err != nil || deltas != "Use pnpm." || full != deltas {
		t.Fatalf("got %q %q %v", deltas, full, err)
	}
	if sent["stream"] != true || sent["model"] != "m" {
		t.Fatalf("request: %+v", sent)
	}
	ms, err := p.Models(context.Background())
	if err != nil || len(ms) != 2 {
		t.Fatalf("models: %v %v", ms, err)
	}
	p.Allow = []string{"only-this"}
	if ms, _ := p.Models(context.Background()); len(ms) != 1 || ms[0] != "only-this" {
		t.Fatalf("allow list: %v", ms)
	}
}

func TestOpenAIErrors(t *testing.T) {
	srv := openaiFake(t, "sk-good", func(w http.ResponseWriter, body map[string]any) {
		sseChunk(w, "half")
		fmt.Fprint(w, `data: {"error":{"message":"upstream overloaded","code":502}}`+"\n\n")
	})
	bad := &OpenAIProvider{BaseURL: srv.URL + "/v1", Key: "sk-wrong"}
	_, _, err := collect(t, bad, context.Background(), hello)
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Fatalf("401: %v", err)
	}
	if _, err := bad.Models(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("models 401: %v", err)
	}
	good := &OpenAIProvider{BaseURL: srv.URL + "/v1", Key: "sk-good"}
	_, full, err := collect(t, good, context.Background(), hello)
	if err == nil || !strings.Contains(err.Error(), "upstream overloaded") || full != "half" {
		t.Fatalf("mid-stream: %q %v", full, err)
	}
	// A stream that stops without [DONE] or a finish reason is cut off.
	cut := openaiFake(t, "", func(w http.ResponseWriter, body map[string]any) { sseChunk(w, "trunc") })
	_, full, err = collect(t, &OpenAIProvider{BaseURL: cut.URL + "/v1"}, context.Background(), hello)
	if err == nil || !strings.Contains(err.Error(), "ended early") || full != "trunc" {
		t.Fatalf("cut: %q %v", full, err)
	}
	// The official API needs a key; nothing is sent without one.
	if _, err := (&OpenAIProvider{BaseURL: DefaultOpenAIURL}).Stream(context.Background(), hello, func(string) {}); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("no key: %v", err)
	}
}

// ---------- Anthropic

func anthropicFake(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak-good" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		if r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"claude-test-1"}]}`)
		case "/v1/messages":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "text/event-stream")
			h(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func anthEvent(w http.ResponseWriter, name, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	w.(http.Flusher).Flush()
}

func TestAnthropicStream(t *testing.T) {
	var sent map[string]any
	srv := anthropicFake(t, func(w http.ResponseWriter, body map[string]any) {
		sent = body
		anthEvent(w, "message_start", `{"type":"message_start","message":{"id":"x"}}`)
		anthEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		anthEvent(w, "ping", `{"type":"ping"}`)
		anthEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Deploy "}}`)
		anthEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"on merge."}}`)
		anthEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		anthEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
		anthEvent(w, "message_stop", `{"type":"message_stop"}`)
	})
	p := &AnthropicProvider{BaseURL: srv.URL + "/v1", Key: "ak-good"}
	req := Request{Model: "claude-x", Messages: []Message{
		{Role: RoleSystem, Content: "notes here"},
		{Role: RoleUser, Content: "q1"},
		{Role: RoleUser, Content: "q2"}, // a failed reply left two user turns
	}}
	deltas, full, err := collect(t, p, context.Background(), req)
	if err != nil || deltas != "Deploy on merge." || full != deltas {
		t.Fatalf("got %q %q %v", deltas, full, err)
	}
	msgs := sent["messages"].([]any)
	if sent["system"] != "notes here" || len(msgs) != 1 || sent["max_tokens"] == nil {
		t.Fatalf("request: %+v", sent)
	}
	if ms, err := p.Models(context.Background()); err != nil || ms[0] != "claude-test-1" {
		t.Fatalf("models: %v %v", ms, err)
	}
}

func TestAnthropicErrors(t *testing.T) {
	srv := anthropicFake(t, func(w http.ResponseWriter, body map[string]any) {
		anthEvent(w, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"so"}}`)
		anthEvent(w, "error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	})
	bad := &AnthropicProvider{BaseURL: srv.URL + "/v1", Key: "ak-bad"}
	_, _, err := collect(t, bad, context.Background(), hello)
	if !errors.Is(err, ErrUnauthorized) || !strings.Contains(err.Error(), "invalid x-api-key") {
		t.Fatalf("401: %v", err)
	}
	if _, err := bad.Models(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("models 401: %v", err)
	}
	good := &AnthropicProvider{BaseURL: srv.URL + "/v1", Key: "ak-good"}
	_, full, err := collect(t, good, context.Background(), hello)
	if err == nil || !strings.Contains(err.Error(), "Overloaded") || full != "so" {
		t.Fatalf("stream error: %q %v", full, err)
	}
	if _, err := (&AnthropicProvider{BaseURL: srv.URL + "/v1"}).Stream(context.Background(), hello, func(string) {}); err == nil {
		t.Fatal("no key: want error")
	}
}

// ---------- cancellation, for all three

func TestCancelStopsStream(t *testing.T) {
	// Each server sends one piece, then holds the stream open until the
	// client goes away.
	hold := func(first func(w http.ResponseWriter)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			first(w)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		}
	}
	servers := map[string]*httptest.Server{
		Ollama: httptest.NewServer(hold(func(w http.ResponseWriter) {
			fmt.Fprint(w, `{"message":{"content":"first"},"done":false}`+"\n")
		})),
		OpenAI: httptest.NewServer(hold(func(w http.ResponseWriter) {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"first"}}]}`+"\n\n")
		})),
		Anthropic: httptest.NewServer(hold(func(w http.ResponseWriter) {
			fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"first\"}}\n\n")
		})),
	}
	provs := []Provider{
		&OllamaProvider{URL: servers[Ollama].URL},
		&OpenAIProvider{BaseURL: servers[OpenAI].URL + "/v1", Key: "k"},
		&AnthropicProvider{BaseURL: servers[Anthropic].URL + "/v1", Key: "k"},
	}
	for _, p := range provs {
		t.Run(p.Name(), func(t *testing.T) {
			defer servers[p.Name()].Close()
			ctx, cancel := context.WithCancel(context.Background())
			t0 := time.Now()
			full, err := p.Stream(ctx, hello, func(d string) { cancel() })
			if !errors.Is(err, context.Canceled) || full != "first" {
				t.Fatalf("got %q %v", full, err)
			}
			if time.Since(t0) > 5*time.Second {
				t.Fatal("cancel did not stop the stream promptly")
			}
		})
	}
}

func TestParseRef(t *testing.T) {
	cases := map[string]string{
		"ollama/qwen2.5:0.5b":                "ollama/qwen2.5:0.5b",
		"qwen2.5:0.5b":                       "ollama/qwen2.5:0.5b",
		"claude-sonnet-4-5":                  "anthropic/claude-sonnet-4-5",
		"gpt-4o-mini":                        "openai/gpt-4o-mini",
		"o3-mini":                            "openai/o3-mini",
		"openai/anthropic/claude-3.5-sonnet": "openai/anthropic/claude-3.5-sonnet",
		"OpenAI/llama-3.1-8b-instant":        "openai/llama-3.1-8b-instant",
		"meta-llama/llama-3":                 "ollama/meta-llama/llama-3",
	}
	for in, want := range cases {
		r, err := ParseRef(in)
		if err != nil || r.String() != want {
			t.Errorf("ParseRef(%q) = %q, %v; want %q", in, r.String(), err, want)
		}
	}
	for _, bad := range []string{"", "  ", "openai/"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q): want error", bad)
		}
	}
}

func TestSSEParser(t *testing.T) {
	in := "event: a\ndata: one\ndata: two\n\n: comment\r\ndata: three\r\n\nevent: b\ndata: last"
	var got []string
	sse(strings.NewReader(in), func(ev, data string) bool { got = append(got, ev+"="+data); return true })
	if strings.Join(got, "|") != "a=one\ntwo|=three|b=last" {
		t.Fatalf("got %q", got)
	}
}
