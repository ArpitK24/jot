package dedup

import (
	"strings"
	"testing"
	"time"

	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/embed/embedtest"
	"github.com/ArpitK24/jot/internal/store"
)

const (
	pnpmA = "Use pnpm for every JS project. Never commit package-lock.json."
	pnpmB = "Use pnpm for every JS project. Pin the version with the packageManager field in package.json."
	other = "Deploy the web app on Vercel and Go services on Fly.io."
)

func TestSimilarity(t *testing.T) {
	if s := Similarity(pnpmA, pnpmB); s < Threshold {
		t.Errorf("pnpm notes = %.2f, want >= %.2f", s, Threshold)
	}
	if s := Similarity(pnpmA, other); s > 0.2 {
		t.Errorf("unrelated = %.2f", s)
	}
	if s := Similarity("Use Go", "Use Rust"); s != 0 {
		t.Errorf("tiny notes = %.2f, want 0", s)
	}
	if s := Similarity(pnpmA, pnpmA); s != 1 {
		t.Errorf("identical = %.2f", s)
	}
}

func mem(id, body string, age time.Duration) *store.Memory {
	return &store.Memory{ID: id, Body: body, Updated: time.Now().Add(-age)}
}

func TestPairs(t *testing.T) {
	a := mem("a", pnpmA, 48*time.Hour)
	b := mem("b", pnpmB, time.Hour)
	c := mem("c", other, time.Hour)
	ps := Pairs([]*store.Memory{a, b, c}, 0)
	if len(ps) != 1 {
		t.Fatalf("pairs = %d, want 1", len(ps))
	}
	if ps[0].Keep != b || ps[0].Drop != a {
		t.Errorf("want the newer note kept, got keep=%s", ps[0].Keep.ID)
	}
	if got := Similar([]*store.Memory{a, c}, pnpmB, 0); len(got) != 1 || got[0] != a {
		t.Errorf("similar = %v", got)
	}
}

func TestMergeBody(t *testing.T) {
	got := MergeBody(pnpmB, pnpmA)
	if !strings.HasPrefix(got, pnpmB) || !strings.Contains(got, "Never commit package-lock.json.") {
		t.Errorf("merge = %q", got)
	}
	if strings.Count(got, "Use pnpm") != 1 {
		t.Errorf("repeated sentence in %q", got)
	}
	if got := MergeBody(pnpmA, pnpmA); got != pnpmA {
		t.Errorf("self merge = %q", got)
	}
}

func TestMergeTags(t *testing.T) {
	got := MergeTags([]string{"js", "tooling"}, []string{"JS", "node"})
	if strings.Join(got, ",") != "js,node,tooling" {
		t.Errorf("tags = %v", got)
	}
}

func TestParaphrases(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", d)
	t.Setenv("HOME", d)
	t.Setenv("LocalAppData", d)
	srv := embedtest.Server(nil)
	defer srv.Close()
	now := time.Now()
	mems := []*store.Memory{
		{ID: "a", Body: "Login uses a JWT token.", Updated: now},
		{ID: "b", Body: "Users sign in with an auth session.", Updated: now.Add(-time.Hour)},
		{ID: "c", Body: "Package manager is pnpm, not npm.", Updated: now},
	}
	if p := Pairs(mems, 0); len(p) != 0 {
		t.Fatalf("lexical dedup should miss the paraphrase, got %d", len(p))
	}
	ix := embed.Open(embed.Config{Provider: "ollama", URL: srv.URL})
	p, err := PairsWith(mems, 0, ix)
	if err != nil || len(p) != 1 || !p[0].Semantic || p[0].Keep.ID != "a" || p[0].Drop.ID != "b" {
		t.Fatalf("got %+v, %v", p, err)
	}
	sim, err := SimilarWith(mems, "Sign-in is handled by a JWT session.", 0, ix)
	if err != nil || len(sim) == 0 || (sim[0].ID != "a" && sim[0].ID != "b") {
		t.Fatalf("remember guard missed paraphrase: %v %v", sim, err)
	}
	if sim, _ := SimilarWith(mems, "Deploy on merge to main.", 0, ix); len(sim) != 0 {
		t.Fatalf("unrelated note flagged: %v", sim[0].ID)
	}
}
