// Package dedup finds memories that say the same thing and merges them.
//
// Agents often save the same fact twice in slightly different words.
// Similarity here is lexical: the share of meaningful words two memories
// have in common, using the same tokenizer as search. It needs no model
// and catches the common case of near-identical notes.
package dedup

import (
	"sort"
	"strings"

	"github.com/ArpitK24/jot/internal/search"
	"github.com/ArpitK24/jot/internal/store"
)

// Threshold is the default score above which two memories count as duplicates.
const Threshold = 0.6

// minShared stops very short notes ("Use Go", "Use Rust") from matching
// on one or two words.
const minShared = 3

// Pair is two memories that look like duplicates. Keep is the one
// updated most recently; Drop is the other.
type Pair struct {
	Keep, Drop *store.Memory
	Score      float64
}

// Similarity scores how much a and b overlap, from 0 to 1. It averages
// Jaccard (shared / all words) with the overlap coefficient (shared /
// words in the shorter one), so a short note restated inside a longer
// one still scores high.
func Similarity(a, b string) float64 {
	x, y := set(a), set(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	shared := 0
	for t := range x {
		if y[t] {
			shared++
		}
	}
	if shared < minShared {
		return 0
	}
	union := len(x) + len(y) - shared
	small := min(len(x), len(y))
	return (float64(shared)/float64(union) + float64(shared)/float64(small)) / 2
}

// Pairs returns every pair of memories scoring at least threshold, best
// first. Each memory appears in at most one pair, its strongest.
func Pairs(mems []*store.Memory, threshold float64) []Pair {
	if threshold <= 0 {
		threshold = Threshold
	}
	var all []Pair
	for i := 0; i < len(mems); i++ {
		for j := i + 1; j < len(mems); j++ {
			s := Similarity(mems[i].Body, mems[j].Body)
			if s < threshold {
				continue
			}
			keep, drop := mems[i], mems[j]
			if drop.Updated.After(keep.Updated) {
				keep, drop = drop, keep
			}
			all = append(all, Pair{Keep: keep, Drop: drop, Score: s})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].Score > all[b].Score })
	used := map[*store.Memory]bool{}
	var out []Pair
	for _, p := range all {
		if used[p.Keep] || used[p.Drop] {
			continue
		}
		used[p.Keep], used[p.Drop] = true, true
		out = append(out, p)
	}
	return out
}

// Similar returns existing memories that look like a duplicate of body,
// best first.
func Similar(mems []*store.Memory, body string, threshold float64) []*store.Memory {
	if threshold <= 0 {
		threshold = Threshold
	}
	type hit struct {
		m *store.Memory
		s float64
	}
	var hits []hit
	for _, m := range mems {
		if s := Similarity(m.Body, body); s >= threshold {
			hits = append(hits, hit{m, s})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].s > hits[b].s })
	out := make([]*store.Memory, len(hits))
	for i, h := range hits {
		out[i] = h.m
	}
	return out
}

// MergeBody keeps all of keep and adds each sentence of drop that keep
// doesn't already say.
func MergeBody(keep, drop string) string {
	have := set(keep)
	var extra []string
	for _, s := range sentences(drop) {
		toks := set(s)
		if len(toks) == 0 {
			continue
		}
		known := 0
		for t := range toks {
			if have[t] {
				known++
			}
		}
		if float64(known)/float64(len(toks)) >= 0.8 {
			continue
		}
		extra = append(extra, s)
		for t := range toks {
			have[t] = true
		}
	}
	out := strings.TrimSpace(keep)
	if len(extra) > 0 {
		out += "\n\n" + strings.Join(extra, " ")
	}
	return out
}

// MergeTags is the sorted union of both tag lists.
func MergeTags(a, b []string) []string {
	return store.NormalizeTags(append(append([]string{}, a...), b...))
}

func set(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range search.Tokenize(s) {
		out[t] = true
	}
	return out
}

// sentences splits text at sentence ends and line breaks.
func sentences(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	rs := []rune(text)
	for i, r := range rs {
		if r == '\n' {
			flush()
			continue
		}
		cur.WriteRune(r)
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\n') {
			flush()
		}
	}
	flush()
	return out
}
