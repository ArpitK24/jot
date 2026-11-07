package embed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArpitK24/jot/internal/embed/embedtest"
)

func useTempCache(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", d)
	t.Setenv("HOME", d)
	t.Setenv("LocalAppData", d)
}

func TestLoadSaveAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JOT_EMBED", "")
	if Load(dir).On() {
		t.Fatal("embeddings should be off without config")
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"other":1}`), 0o644)
	if err := Save(dir, Config{Provider: "ollama"}); err != nil {
		t.Fatal(err)
	}
	c := Load(dir)
	if !c.On() || c.Model != DefaultModel || c.URL != DefaultURL {
		t.Fatalf("got %+v", c)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(b), `"other"`) {
		t.Fatal("Save dropped other keys")
	}
	t.Setenv("JOT_EMBED", "off")
	if Load(dir).On() {
		t.Fatal("JOT_EMBED=off should win")
	}
	t.Setenv("JOT_EMBED", "ollama:mxbai-embed-large")
	if c := Load(dir); c.Model != "mxbai-embed-large" {
		t.Fatalf("model override: %+v", c)
	}
	t.Setenv("JOT_EMBED", "")
	Save(dir, Config{})
	if Load(dir).On() {
		t.Fatal("Save(off) should turn it off")
	}
}

func TestIndexCachesVectors(t *testing.T) {
	useTempCache(t)
	var calls int32
	srv := embedtest.Server(&calls)
	defer srv.Close()
	ix := Open(Config{Provider: "ollama", URL: srv.URL})
	ctx := context.Background()
	v, err := ix.Documents(ctx, []string{"login uses JWT", "deploy on merge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || calls != 1 {
		t.Fatalf("vectors %d calls %d", len(v), calls)
	}
	if _, err := ix.Documents(ctx, []string{"login uses JWT"}); err != nil || calls != 1 {
		t.Fatalf("cached text hit the model again (calls %d)", calls)
	}
	// A fresh index reads the cache file.
	ix2 := Open(Config{Provider: "ollama", URL: srv.URL})
	if ix2.Cached() != 2 {
		t.Fatalf("cache file not reused: %d", ix2.Cached())
	}
	q, _ := ix.Query(ctx, "auth token")
	if Cosine(q, v[0]) <= Cosine(q, v[1]) {
		t.Fatal("query should be closer to the auth note")
	}
}

func TestUnavailable(t *testing.T) {
	useTempCache(t)
	var calls int32
	srv := embedtest.Server(&calls)
	ix := Open(Config{Provider: "ollama", URL: srv.URL, Model: "missing"})
	err := ix.Check(context.Background())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "ollama pull missing") {
		t.Fatalf("got %v", err)
	}
	srv.Close()
	ix = Open(Config{Provider: "ollama", URL: srv.URL})
	if _, err := ix.Query(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
