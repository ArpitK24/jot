// Package embedtest is a stand-in for Ollama in tests. It embeds text as
// counts over a few hand-made concepts, so paraphrases ("login" and
// "sign in") land close together the way a real model places them.
package embedtest

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"unicode"
)

var concepts = [][]string{
	{"auth", "login", "jwt", "token", "sign", "signin", "authentication", "session"},
	{"deploy", "release", "ship", "merge", "merged", "publish", "production"},
	{"pnpm", "npm", "package", "lockfile", "yarn", "manager", "dependencies"},
	{"ist", "timezone", "time", "hour", "clock", "india"},
	{"bubble", "tea", "tui", "terminal"},
	{"commit", "commits", "push", "small", "chunks", "dump"},
}

const dims = 16

// Vector embeds s.
func Vector(s string) []float32 {
	v := make([]float32, dims)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		hit := false
		for i, c := range concepts {
			for _, x := range c {
				if w == x {
					v[i] += 1
					hit = true
				}
			}
		}
		if !hit && len(w) > 3 {
			h := fnv.New32a()
			h.Write([]byte(w))
			v[len(concepts)+int(h.Sum32()%uint32(dims-len(concepts)))] += 0.15
		}
	}
	v[dims-1] += 0.01
	return v
}

// Server answers /api/embed like Ollama. Calls counts requests when not nil.
// A model named "missing" gets Ollama's not-found error.
func Server(calls *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "missing" {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"model \"missing\" not found, try pulling it first"}`))
			return
		}
		out := make([][]float32, len(req.Input))
		for i, s := range req.Input {
			out[i] = Vector(s)
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
}
