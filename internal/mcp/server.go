// Package mcp is a minimal Model Context Protocol server over stdio
// (newline-delimited JSON-RPC 2.0) exposing jot's memory tools.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
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

	// Dir and Resolve, when Resolve is set, replace Stores: the stores are
	// worked out from Dir on every call, so a .jot folder created during a
	// session is picked up, and Dir follows the workspace root the client
	// reports through MCP roots.
	Dir     string
	Resolve func(dir string) []*store.Store

	rootsAsked bool
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"` // set on replies to our own requests
}

// rootsID is the id of jot's own roots/list request to the client.
const rootsID = `"jot-roots"`

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
		if req.Method == "" { // a reply to a request jot sent
			if string(req.ID) == rootsID {
				s.useRoots(req.Result)
			}
			continue
		}
		if len(req.ID) == 0 { // notification: no reply
			if req.Method == "notifications/initialized" || req.Method == "notifications/roots/list_changed" {
				if s.rootsAsked {
					if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(rootsID), "method": "roots/list"}); err != nil {
						return err
					}
				}
			}
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
		var p struct {
			Capabilities struct {
				Roots json.RawMessage `json:"roots"`
			} `json:"capabilities"`
		}
		json.Unmarshal(req.Params, &p)
		s.rootsAsked = s.Resolve != nil && len(p.Capabilities.Roots) > 0 && string(p.Capabilities.Roots) != "null"
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "jot", "version": s.Version},
			"instructions":    s.instructions(),
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
				return fmt.Sprintf("Not saved: this looks like an existing memory.\n[%s] (%s, from %s memory)\n%s\n\n"+
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
		res := search.Search(s.all(), "", search.Options{Limit: listLimit(a.Limit), Kind: a.Kind, Tags: a.Tags})
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
		fmt.Fprintf(&b, "[%s] (%s, from %s memory", m.ID, m.Kind, m.Scope)
		if len(m.Tags) > 0 {
			fmt.Fprintf(&b, ", tags: %s", strings.Join(m.Tags, ", "))
		}
		b.WriteString(")\n")
		b.WriteString(m.Body)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// stores returns the stores in use: project first, then global.
func (s *Server) stores() []*store.Store {
	if s.Resolve != nil {
		return s.Resolve(s.Dir)
	}
	return s.Stores
}

// useRoots moves Dir to the first workspace root the client reported that
// has project memory, so a client that starts jot outside the repo still
// gets that repo's memories.
func (s *Server) useRoots(result json.RawMessage) {
	var r struct {
		Roots []struct {
			URI string `json:"uri"`
		} `json:"roots"`
	}
	if json.Unmarshal(result, &r) != nil {
		return
	}
	for _, root := range r.Roots {
		dir, ok := fileURIPath(root.URI)
		if !ok {
			continue
		}
		if _, found := store.FindProjectDir(dir); found {
			s.Dir = dir
			return
		}
	}
}

// fileURIPath turns file:///home/me/repo or file:///C:/repo into a path.
func fileURIPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' { // /C:/repo
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

func (s *Server) all() []*store.Memory {
	var out []*store.Memory
	for _, st := range s.stores() {
		ms, _ := st.List()
		out = append(out, ms...)
	}
	return out
}

func (s *Server) pick(scope string) (*store.Store, error) {
	if scope == "" {
		scope = "global"
	}
	for _, st := range s.stores() {
		if st.Scope == scope {
			return st, nil
		}
	}
	if scope == "project" {
		return nil, errors.New("Not saved: this repo has no project memory yet. Save it with scope \"global\" if it holds " +
			"for the user everywhere, or ask the user to run `jot init --project` at the repo root to start project memory.")
	}
	return nil, fmt.Errorf("unknown scope %q: use \"global\" or \"project\"", scope)
}

func (s *Server) find(id string) (*store.Store, error) {
	for _, st := range s.stores() {
		if _, err := st.Get(id); err == nil {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no memory with id %q", id)
}

// instructions is what the server tells the agent on initialize: when to
// recall, what is worth saving, and which scope to use.
func (s *Server) instructions() string {
	var b strings.Builder
	b.WriteString("jot is the user's memory, shared by all of their AI coding tools. ")
	b.WriteString("At the start of each task, call recall with a few words about it and follow the preferences and decisions it returns. Do this without being asked. ")
	b.WriteString("Call remember only for things that will still matter in a future session: a preference the user states, a decision that was made, a correction to how you work, or a non-obvious fact about the project (how to build, test or deploy it). ")
	b.WriteString("Write each memory as one or two plain sentences that make sense on their own, months later, to a different agent: name the tool, language or project they apply to. ")
	b.WriteString("Don't save secrets or credentials, task progress, code, logs, anything you could read from the repo itself, or anything the user asks you not to keep. Saving nothing is often right. ")
	b.WriteString("If remember says a similar memory exists, call update on that one instead of saving another. If a memory is wrong or outdated, update or forget it. ")
	if p := s.projectStore(); p != nil {
		b.WriteString("This repo has project memory (" + p.Dir + "). recall searches it and the user's global memory together and labels each result project or global. ")
		b.WriteString("Use scope \"project\" for facts about this repo only, and the default \"global\" for the user's preferences that hold everywhere.")
	} else {
		b.WriteString("Memories are global (they follow the user to every project) unless the repo has project memory, created with `jot init --project`.")
	}
	return b.String()
}

func (s *Server) projectStore() *store.Store {
	for _, st := range s.stores() {
		if st.Scope == "project" {
			return st
		}
	}
	return nil
}

func tools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	tags := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "One to three short lowercase topic tags, e.g. [\"js\", \"tooling\"]."}
	kind := map[string]any{"type": "string", "enum": []string{"fact", "preference", "decision", "note"},
		"description": "preference: how the user likes things done. decision: a choice made, ideally with why. fact: something true about the user or project. note: anything else (default)."}
	obj := func(props map[string]any, req ...string) map[string]any {
		o := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			o["required"] = req
		}
		return o
	}
	return []map[string]any{
		{
			"name": "remember",
			"description": "Save one lasting memory that every agent the user runs can recall later. " +
				"Save: a preference the user states (\"Use pnpm, never npm, in JS projects\"), a decision with its reason, " +
				"a correction to how you work, a non-obvious fact about the project. " +
				"Don't save: secrets or credentials (refused), task progress or todo items, code, logs, " +
				"things readable from the repo, or one-off details. Prefer saving nothing to saving noise. " +
				"Limit " + fmt.Sprint(MaxMemoryLen) + " characters. If a similar memory exists this saves nothing and returns it; update that one instead.",
			"inputSchema": obj(map[string]any{
				"text":   str("One or two plain sentences that make sense on their own months from now, to another agent. Name what it applies to (tool, language, project)."),
				"kind":   kind,
				"tags":   tags,
				"scope":  map[string]any{"type": "string", "enum": []string{"global", "project"}, "description": "global (default): about the user, applies in every project. project: only about the current repo; needs project memory there (`jot init --project`)."},
				"source": str("Name of the agent saving this, e.g. claude-code. Usually set for you."),
				"force":  map[string]any{"type": "boolean", "description": "Save even though a similar memory exists. Only after reading it and deciding this one is genuinely separate."},
			}, "text"),
		},
		{
			"name": "recall",
			"description": "Search the user's shared memory for preferences, decisions and facts relevant to the task. " +
				"Call it at the start of every task, before writing code, with a few words about the task (e.g. \"set up tests for the React app\"). " +
				"Searches this repo's project memory, when there is one, and the user's global memory; each result says which.",
			"inputSchema": obj(map[string]any{
				"query": str("What you are about to do or want to know, in plain words."),
				"kind":  kind,
				"tags":  tags,
				"limit": map[string]any{"type": "integer", "description": "Max results (default 10)."},
			}, "query"),
		},
		{
			"name":        "update",
			"description": "Rewrite a memory that changed or that a new memory would duplicate. Prefer this over saving a second, contradicting memory.",
			"inputSchema": obj(map[string]any{"id": str("Memory id, from recall or remember."), "text": str("The full new text (replaces the old). Same rules as remember."), "kind": kind, "tags": tags}, "id"),
		},
		{
			"name":        "forget",
			"description": "Delete a memory that is wrong, outdated, or that the user asked you to forget. Git history keeps a copy the user can restore.",
			"inputSchema": obj(map[string]any{"id": str("Memory id, from recall.")}, "id"),
		},
		{
			"name":        "list",
			"description": "List the most recent memories, optionally filtered by kind or tags. Use recall to search by topic.",
			"inputSchema": obj(map[string]any{"kind": kind, "tags": tags, "limit": map[string]any{"type": "integer", "description": "Max results (default 20)."}}),
		},
	}
}

func listLimit(n int) int {
	if n <= 0 {
		return 20
	}
	return n
}
