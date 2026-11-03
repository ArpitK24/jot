package search

import (
	"testing"
	"time"

	"github.com/ArpitK24/jot/internal/store"
)

func mem(id, body, kind string, tags ...string) *store.Memory {
	return &store.Memory{ID: id, Body: body, Kind: kind, Tags: tags, Updated: time.Now()}
}

func TestRanking(t *testing.T) {
	mems := []*store.Memory{
		mem("a", "User prefers pnpm over npm for JavaScript projects", "preference", "js"),
		mem("b", "Deploys go to Vercel on merge to main", "fact", "deploy"),
		mem("c", "Decided to use SQLite for local caching", "decision", "db"),
	}
	res := Search(mems, "which package manager for javascript?", Options{})
	if len(res) == 0 || res[0].Memory.ID != "a" {
		t.Fatalf("want a first, got %+v", res)
	}
	res = Search(mems, "deploys", Options{})
	if len(res) != 1 || res[0].Memory.ID != "b" {
		t.Fatalf("stemmed match failed: %+v", res)
	}
	if res := Search(mems, "kubernetes", Options{}); len(res) != 0 {
		t.Fatalf("want no results, got %d", len(res))
	}
}

func TestTagBoostAndFilters(t *testing.T) {
	mems := []*store.Memory{
		mem("a", "testing notes about go and more go", "note"),
		mem("b", "go testing", "preference", "go"),
	}
	res := Search(mems, "go", Options{})
	if res[0].Memory.ID != "b" {
		t.Fatalf("tag boost not applied: %+v", res[0].Memory.ID)
	}
	if res := Search(mems, "", Options{Kind: "preference"}); len(res) != 1 || res[0].Memory.ID != "b" {
		t.Fatalf("kind filter failed")
	}
	if res := Search(mems, "", Options{Tags: []string{"GO"}}); len(res) != 1 {
		t.Fatalf("tag filter failed")
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("The Libraries are running, ok?")
	want := []string{"library", "runn", "ok"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
