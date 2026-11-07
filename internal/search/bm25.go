// Package search ranks memories against a query with BM25, plus small
// boosts for tag matches and recency. No index is kept on disk: memory
// stores are small enough to score in memory on every call.
//
// When embeddings are on (Options.Index), keyword and semantic rankings are
// fused, so a note that says the same thing in other words still turns up.
// If the model can't be reached, search quietly stays keyword-only.
package search

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/store"
)

const (
	k1 = 1.2
	b  = 0.75

	tagBoost     = 1.5  // per query term that exactly matches a tag
	recencyBoost = 0.25 // max bonus for a memory updated just now
	recencyDays  = 90.0 // half-life of the recency bonus
)

// Result is a memory with its score.
type Result struct {
	Memory *store.Memory
	Score  float64
}

// Options filter a search.
type Options struct {
	Limit int
	Kind  string
	Tags  []string // memory must carry every one of these
	Now   time.Time

	// Index, when set, adds semantic matches. Nil means keyword only.
	Index *embed.Index
	// MinSimilarity is the cosine a semantic match needs (default: the
	// model's own cut-off).
	MinSimilarity float64
	// Err, when set, receives the reason semantic search was skipped.
	Err *error
}

const (
	rrfK       = 60.0
	semMargin  = 0.3 // semantic matches must be this close to the best one
	semTimeout = 20 * time.Second
)

// Search scores mems against query. An empty query returns the newest
// memories that pass the filters.
func Search(mems []*store.Memory, query string, opt Options) []Result {
	if opt.Limit <= 0 {
		opt.Limit = 10
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	var pool []*store.Memory
	for _, m := range mems {
		if keep(m, opt) {
			pool = append(pool, m)
		}
	}
	terms := Tokenize(query)
	if len(terms) == 0 {
		sort.SliceStable(pool, func(i, j int) bool { return pool[i].Updated.After(pool[j].Updated) })
		return top(pool, opt.Limit)
	}

	if len(pool) == 0 {
		return nil
	}
	scores := bm25(pool, terms, opt.Now)
	if opt.Index != nil {
		if fused, err := fuse(pool, query, scores, opt); err == nil {
			scores = fused
		} else if opt.Err != nil {
			*opt.Err = err
		}
	}
	idx := make([]int, 0, len(pool))
	for i := range pool {
		if scores[i] > 0 {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, c int) bool { return scores[idx[a]] > scores[idx[c]] })
	out := make([]Result, 0, opt.Limit)
	for _, i := range idx {
		if len(out) == opt.Limit {
			break
		}
		out = append(out, Result{Memory: pool[i], Score: scores[i]})
	}
	return out
}

// bm25 scores every memory in pool against terms; 0 means no match.
func bm25(pool []*store.Memory, terms []string, now time.Time) []float64 {
	docs := make([][]string, len(pool))
	df := map[string]int{}
	total := 0
	for i, m := range pool {
		docs[i] = Tokenize(m.Body + " " + strings.Join(m.Tags, " ") + " " + m.Kind)
		total += len(docs[i])
		seen := map[string]bool{}
		for _, t := range docs[i] {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	avg := float64(total) / float64(len(pool))
	n := float64(len(pool))

	scores := make([]float64, len(pool))
	for i, m := range pool {
		tf := map[string]int{}
		for _, t := range docs[i] {
			tf[t]++
		}
		dl := float64(len(docs[i]))
		var s float64
		for _, q := range terms {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[q])+0.5)/(float64(df[q])+0.5))
			s += idf * f * (k1 + 1) / (f + k1*(1-b+b*dl/avg))
		}
		if s == 0 {
			continue
		}
		for _, q := range terms {
			for _, t := range m.Tags {
				if t == q {
					s += tagBoost
				}
			}
		}
		age := now.Sub(m.Updated).Hours() / 24
		if age < 0 {
			age = 0
		}
		s += recencyBoost * math.Exp(-age*math.Ln2/recencyDays)
		scores[i] = s
	}
	return scores
}

// fuse merges the keyword ranking with a semantic one by reciprocal rank
// fusion. A memory found both ways ranks above one found only one way.
func fuse(pool []*store.Memory, query string, kw []float64, opt Options) ([]float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), semTimeout)
	defer cancel()
	q, err := opt.Index.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(pool))
	for i, m := range pool {
		texts[i] = m.Body
	}
	vecs, err := opt.Index.Documents(ctx, texts)
	if err != nil {
		return nil, err
	}
	minSim := opt.MinSimilarity
	if minSim <= 0 {
		minSim = opt.Index.MatchThreshold()
	}
	cos := make([]float64, len(pool))
	best := 0.0
	for i := range pool {
		cos[i] = embed.Cosine(q, vecs[i])
		best = max(best, cos[i])
	}
	// Keep only clear matches: above the model's cut-off and near the best
	// one, so a vague query doesn't drag in half the store.
	sem := make([]float64, len(pool))
	for i, c := range cos {
		if c >= minSim && c >= best-semMargin {
			sem[i] = c
		}
	}
	kr, sr := ranks(kw), ranks(sem)
	anySem := best >= minSim
	out := make([]float64, len(pool))
	for i := range pool {
		// When the meaning of the query is clear, a keyword hit on a
		// common word ("use", "works") in an unrelated note is noise.
		if kr[i] > 0 && sr[i] == 0 && anySem && cos[i] < minSim/2 {
			continue
		}
		if kr[i] > 0 {
			out[i] += 1 / (rrfK + float64(kr[i]))
		}
		if sr[i] > 0 {
			out[i] += 1 / (rrfK + float64(sr[i]))
		}
	}
	return out, nil
}

// ranks gives each positive score its 1-based rank, 0 for no score.
func ranks(scores []float64) []int {
	idx := make([]int, 0, len(scores))
	for i, s := range scores {
		if s > 0 {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	r := make([]int, len(scores))
	for k, i := range idx {
		r[i] = k + 1
	}
	return r
}

func keep(m *store.Memory, opt Options) bool {
	if opt.Kind != "" && !strings.EqualFold(m.Kind, opt.Kind) {
		return false
	}
	for _, want := range store.NormalizeTags(opt.Tags) {
		found := false
		for _, t := range m.Tags {
			if t == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func top(mems []*store.Memory, limit int) []Result {
	out := []Result{}
	for _, m := range mems {
		if len(out) == limit {
			break
		}
		out = append(out, Result{Memory: m})
	}
	return out
}

var stop = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true, "to": true,
	"in": true, "on": true, "for": true, "is": true, "are": true, "was": true, "be": true,
	"it": true, "this": true, "that": true, "with": true, "as": true, "at": true, "by": true,
	"i": true, "we": true, "you": true, "my": true, "our": true, "do": true, "does": true,
	"what": true, "which": true, "how": true,
}

// Tokenize lowercases, splits on anything that isn't a letter or digit,
// drops stop words and applies a light plural/suffix stem.
func Tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if stop[f] || len(f) < 2 {
			continue
		}
		out = append(out, stem(f))
	}
	return out
}

func stem(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 4 && strings.HasSuffix(w, "ing"):
		return w[:len(w)-3]
	case len(w) > 3 && strings.HasSuffix(w, "es") && !strings.HasSuffix(w, "ses"):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}
