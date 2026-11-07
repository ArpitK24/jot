// Package embed adds optional semantic matching to jot through a local
// embedding model (Ollama). It is off unless the user turns it on, and
// every caller falls back to keyword search when the model can't be
// reached, so jot never depends on it.
//
// Vectors are cached by content hash in the user's cache directory, not in
// the store, so they never end up in git and can be thrown away at any time.
package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Defaults for the Ollama provider. all-minilm is small (46 MB), fast on a
// laptop CPU, and separates related from unrelated short notes clearly.
const (
	DefaultModel = "all-minilm"
	DefaultURL   = "http://localhost:11434"
	configFile   = "config.json"
	batchSize    = 32
)

// Config says whether and how to embed. It lives in the global store as
// config.json under "embed"; JOT_EMBED, JOT_EMBED_MODEL and JOT_OLLAMA_URL
// override it.
type Config struct {
	Provider string `json:"provider,omitempty"` // "ollama" or "" (off)
	Model    string `json:"model,omitempty"`
	URL      string `json:"url,omitempty"`

	// Cosine cut-offs. Each model spreads its scores differently, so the
	// defaults come from a per-model table; set these to override it.
	MinSimilarity  float64 `json:"min_similarity,omitempty"`  // a search match
	DupeSimilarity float64 `json:"dupe_similarity,omitempty"` // a paraphrased duplicate
}

// tuning holds what jot knows about a model: its task prefixes and where
// its cosine scores separate "related" from "unrelated" (measured on short
// notes like the ones agents save).
type tuning struct {
	query, doc  string
	match, dupe float64
}

var models = map[string]tuning{
	"all-minilm":             {match: 0.25, dupe: 0.75},
	"embeddinggemma":         {query: "task: search result | query: ", doc: "title: none | text: ", match: 0.25, dupe: 0.8},
	"nomic-embed-text":       {query: "search_query: ", doc: "search_document: ", match: 0.6, dupe: 0.9},
	"mxbai-embed-large":      {query: "Represent this sentence for searching relevant passages: ", match: 0.55, dupe: 0.9},
	"snowflake-arctic-embed": {query: "Represent this sentence for searching relevant passages: ", match: 0.55, dupe: 0.93},
}

// unknown models get cautious cut-offs: fewer, surer matches.
var fallback = tuning{match: 0.5, dupe: 0.92}

func (c Config) tuning() tuning {
	name, _, _ := strings.Cut(c.Model, ":")
	t, ok := models[name]
	if !ok {
		t = fallback
	}
	if c.MinSimilarity > 0 {
		t.match = c.MinSimilarity
	}
	if c.DupeSimilarity > 0 {
		t.dupe = c.DupeSimilarity
	}
	return t
}

// On reports whether embeddings are enabled.
func (c Config) On() bool { return c.Provider == "ollama" }

func (c Config) withDefaults() Config {
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.URL == "" {
		c.URL = DefaultURL
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return c
}

// Load reads the config from globalDir and applies environment overrides.
// A missing or unreadable file means "off".
func Load(globalDir string) Config {
	var file struct {
		Embed Config `json:"embed"`
	}
	if b, err := os.ReadFile(filepath.Join(globalDir, configFile)); err == nil {
		_ = json.Unmarshal(b, &file)
	}
	c := file.Embed
	switch v := strings.TrimSpace(os.Getenv("JOT_EMBED")); strings.ToLower(v) {
	case "":
	case "off", "0", "false", "none":
		c.Provider = ""
	case "on", "1", "true", "ollama":
		c.Provider = "ollama"
	default:
		if p, m, ok := strings.Cut(v, ":"); ok && p == "ollama" {
			c.Provider, c.Model = "ollama", m
		}
	}
	if m := os.Getenv("JOT_EMBED_MODEL"); m != "" {
		c.Model = m
	}
	if u := os.Getenv("JOT_OLLAMA_URL"); u != "" {
		c.URL = u
	}
	if !c.On() {
		return Config{}
	}
	return c.withDefaults()
}

// Save writes c into globalDir/config.json, keeping any other keys there.
func Save(globalDir string, c Config) error {
	path := filepath.Join(globalDir, configFile)
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s is not valid JSON: %v", path, err)
		}
	}
	if c.On() {
		doc["embed"] = c.withDefaults()
	} else {
		delete(doc, "embed")
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Index turns texts into vectors, remembering every vector it has made.
// It is safe for concurrent use.
type Index struct {
	cfg    Config
	client *http.Client
	path   string

	mu    sync.Mutex
	vecs  map[string][]float32
	dirty bool
}

// Open returns an index for cfg, or nil when embeddings are off.
func Open(cfg Config) *Index {
	if !cfg.On() {
		return nil
	}
	cfg = cfg.withDefaults()
	ix := &Index{cfg: cfg, client: &http.Client{}, vecs: map[string][]float32{}}
	if dir, err := os.UserCacheDir(); err == nil {
		name := "embeddings-" + safe(cfg.Model) + ".json"
		ix.path = filepath.Join(dir, "jot", name)
		if b, err := os.ReadFile(ix.path); err == nil {
			_ = json.Unmarshal(b, &ix.vecs)
		}
	}
	return ix
}

// Model names the model in use.
func (ix *Index) Model() string { return ix.cfg.Model }

// URL is the Ollama address in use.
func (ix *Index) URL() string { return ix.cfg.URL }

// Cached reports how many vectors are stored.
func (ix *Index) Cached() int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return len(ix.vecs)
}

// Documents embeds memory bodies. Only texts never seen before reach the model.
func (ix *Index) Documents(ctx context.Context, texts []string) ([][]float32, error) {
	return ix.vectors(ctx, texts, ix.docPrefix())
}

// Query embeds a search query.
func (ix *Index) Query(ctx context.Context, q string) ([]float32, error) {
	v, err := ix.vectors(ctx, []string{q}, ix.queryPrefix())
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// MatchThreshold is the cosine a semantic-only search match needs.
func (ix *Index) MatchThreshold() float64 { return ix.cfg.tuning().match }

// DupeThreshold is the cosine above which two notes mean the same thing.
func (ix *Index) DupeThreshold() float64 { return ix.cfg.tuning().dupe }

// Some models are trained with task prefixes and rank noticeably better
// with them.
func (ix *Index) docPrefix() string   { return ix.cfg.tuning().doc }
func (ix *Index) queryPrefix() string { return ix.cfg.tuning().query }

func (ix *Index) vectors(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	keys := make([]string, len(texts))
	var missing []int
	ix.mu.Lock()
	for i, t := range texts {
		keys[i] = key(prefix + t)
		if v, ok := ix.vecs[keys[i]]; ok {
			out[i] = v
		} else {
			missing = append(missing, i)
		}
	}
	ix.mu.Unlock()

	for start := 0; start < len(missing); start += batchSize {
		end := min(start+batchSize, len(missing))
		in := make([]string, 0, end-start)
		for _, i := range missing[start:end] {
			in = append(in, prefix+texts[i])
		}
		vs, err := ix.embed(ctx, in)
		if err != nil {
			ix.Flush()
			return nil, err
		}
		ix.mu.Lock()
		for j, i := range missing[start:end] {
			out[i] = vs[j]
			ix.vecs[keys[i]] = vs[j]
		}
		ix.dirty = true
		ix.mu.Unlock()
	}
	if len(missing) > 0 {
		ix.Flush()
	}
	return out, nil
}

// Flush writes new vectors to the cache file. Errors are ignored: the cache
// only saves time.
func (ix *Index) Flush() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if !ix.dirty || ix.path == "" {
		return
	}
	b, err := json.Marshal(ix.vecs)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(ix.path), 0o755) != nil {
		return
	}
	tmp := ix.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil && os.Rename(tmp, ix.path) == nil {
		ix.dirty = false
	}
}

// ErrUnavailable wraps every failure to reach the model, so callers can
// fall back to keyword search.
var ErrUnavailable = errors.New("embedding model unavailable")

func (ix *Index) embed(ctx context.Context, in []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": ix.cfg.Model, "input": in})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ix.cfg.URL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ix.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: can't reach Ollama at %s (is it running?)", ErrUnavailable, ix.cfg.URL)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if strings.Contains(msg, "not found") {
			msg += fmt.Sprintf(" (run: ollama pull %s)", ix.cfg.Model)
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, msg)
	}
	var r struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Embeddings) != len(in) {
		return nil, fmt.Errorf("%w: unexpected reply from Ollama", ErrUnavailable)
	}
	for _, v := range r.Embeddings {
		normalize(v)
	}
	return r.Embeddings, nil
}

// Check asks Ollama for one tiny embedding, to tell the user whether
// semantic search works right now.
func (ix *Index) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := ix.embed(ctx, []string{"ping"})
	return err
}

// Cosine returns the cosine similarity of two normalized vectors.
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func normalize(v []float32) {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
}

func key(s string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h[:16])
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, s)
}
