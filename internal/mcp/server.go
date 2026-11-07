// Package mcp is a minimal Model Context Protocol server over stdio
// (newline-delimited JSON-RPC 2.0) exposing jot's memory tools.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ArpitK24/jot/internal/dedup"
	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/search"
	"github.com/ArpitK24/jot/internal/store"
)

const protocolVersion = "2025-06-18"

// Server answers MCP requests against a set of stores (project first, then global).
type Server struct {
	Stores  []*store.Store
	Version string
	Source  string       // default source label when a client doesn't send one
	Index   *embed.Index // semantic search; nil means keyword only
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests from r and writes responses to w until r closes.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // notification: no reply
			continue
		}
		res, rerr := s.handle(req)
		out := response{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			out.Error = rerr
		} else {
			out.Result = res
		}
		if err := enc.Encode(out); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s *Server) handle(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "jot", "version": s.Version},
			"instructions": "jot is the user's shared memory across AI agents. " +
				"Call recall at the start of a task to load relevant preferences and decisions. " +
				"Call remember when the user states a durable preference, makes a decision, or corrects you. " +
				"Keep each memory to one short, self-contained fact. " +
				"If remember reports a similar memory, update that one instead of saving a second copy.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		text, err := s.call(p.Name, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

type args struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Query  string   `json:"query"`
	Kind   string   `json:"kind"`
	Tags   []string `json:"tags"`
	Scope  string   `json:"scope"`
	Source string   `json:"source"`
	Limit  int      `json:"limit"`
	Force  bool     `json:"force"`
}

func (s *Server) call(name string, raw json.RawMessage) (string, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("invalid arguments: %v", err)
		}
	}
	switch name {
	case "remember":
		if err := checkMemory(a.Text); err != nil {
			return "", err
		}
		st, err := s.pick(a.Scope)
		if err != nil {
			return "", err
		}
		if !a.Force {
			if sim, _ := dedup.SimilarWith(s.all(), a.Text, 0, s.Index); len(sim) > 0 {
				m := sim[0]
				return fmt.Sprintf("Not saved: this looks like an existing memory.\n[%s] (%s, %s)\n%s\n\n"+
					"If it adds or changes something, call update with id %s. "+
					"If it really is a separate memory, call remember again with force: true.",
					m.ID, m.Kind, m.Scope, m.Body, m.ID), nil
			}
		}
		src := a.Source
		if src == "" {
			src = s.Source
		}
		m, err := st.Add(a.Text, a.Kind, a.Tags, src)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Remembered %s (%s, %s).", m.ID, m.Kind, m.Scope) + memoryTips(m.Body), nil
	case "recall":
		mems := s.all()
		res := search.Search(mems, a.Query, search.Options{Limit: a.Limit, Kind: a.Kind, Tags: a.Tags, Index: s.Index})
		if len(res) == 0 {
			return "No matching memories.", nil
		}
		return Format(res), nil
	case "update":
		if strings.TrimSpace(a.Text) != "" {
			if err := checkMemory(a.Text); err != nil {
				return "", errors.New(strings.Replace(err.Error(), "Not saved", "Not updated", 1))
			}
		}
		st, err := s.find(a.ID)
		if err != nil {
			return "", err
		}
		m, err := st.Update(a.ID, a.Text, a.Kind, a.Tags)
		if err != nil {
			return "", err
		}
		return "Updated " + m.ID + ".", nil
	case "forget":
		st, err := s.find(a.ID)
		if err != nil {
			return "", err
		}
		if err := st.Delete(a.ID); err != nil {
			return "", err
		}
		return "Forgot " + a.ID + ".", nil
	case "list":
		res := search.Search(s.all(), "", search.Options{Limit: max(a.Limit, 20), Kind: a.Kind, Tags: a.Tags})
		if len(res) == 0 {
			return "No memories yet.", nil
		}
		return Format(res), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// Format renders results as compact text for a model to read.
func Format(res []search.Result) string {
	var b strings.Builder
	for _, r := range res {
		m := r.Memory
		fmt.Fprintf(&b, "[%s] (%s, %s", m.ID, m.Kind, m.Scope)
		if len(m.Tags) > 0 {
			fmt.Fprintf(&b, ", tags: %s", strings.Join(m.Tags, ", "))
		}
		b.WriteString(")\n")
		b.WriteString(m.Body)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func (s *Server) all() []*store.Memory {
	var out []*store.Memory
	for _, st := range s.Stores {
		ms, _ := st.List()
		out = append(out, ms...)
	}
	return out
}

func (s *Server) pick(scope string) (*store.Store, error) {
	if scope == "" {
		scope = "global"
	}
	for _, st := range s.Stores {
		if st.Scope == scope {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no %s store here (run `jot init --project` in the repo)", scope)
}

func (s *Server) find(id string) (*store.Store, error) {
	for _, st := range s.Stores {
		if _, err := st.Get(id); err == nil {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no memory with id %q", id)
}

func tools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	tags := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Short lowercase topic tags, e.g. [\"go\", \"testing\"]."}
	kind := map[string]any{"type": "string", "enum": []string{"fact", "preference", "decision", "note"}, "description": "What sort of memory this is."}
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	return []map[string]any{
		{
			"name":        "remember",
			"description": "Save one durable, self-contained memory (a preference, decision, or fact about the user or project) so every agent can use it later.",
			"inputSchema": obj(map[string]any{
				"text":   str("The memory itself, one short self-contained statement."),
				"kind":   kind,
				"tags":   tags,
				"scope":  map[string]any{"type": "string", "enum": []string{"global", "project"}, "description": "global (default) for the user, project for this repo only."},
				"source": str("Name of the agent saving this, e.g. claude-code."),
				"force":  map[string]any{"type": "boolean", "description": "Save even if a similar memory exists. Only after checking it is not a duplicate."},
			}, "text"),
		},
		{
			"name":        "recall",
			"description": "Search shared memory for preferences, decisions and facts relevant to the current task. Call this before starting work.",
			"inputSchema": obj(map[string]any{
				"query": str("What you want to know, in plain words."),
				"kind":  kind,
				"tags":  tags,
				"limit": map[string]any{"type": "integer", "description": "Max results (default 10)."},
			}, "query"),
		},
		{
			"name":        "update",
			"description": "Rewrite an existing memory when it has changed. Prefer this over saving a contradicting new one.",
			"inputSchema": obj(map[string]any{"id": str("Memory id from recall."), "text": str("New text."), "kind": kind, "tags": tags}, "id"),
		},
		{
			"name":        "forget",
			"description": "Delete a memory that is wrong or that the user asked to forget.",
			"inputSchema": obj(map[string]any{"id": str("Memory id from recall.")}, "id"),
		},
		{
			"name":        "list",
			"description": "List the most recent memories, optionally filtered by kind or tags.",
			"inputSchema": obj(map[string]any{"kind": kind, "tags": tags, "limit": map[string]any{"type": "integer"}}),
		},
	}
}
