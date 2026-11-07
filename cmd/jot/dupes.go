package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ArpitK24/jot/internal/dedup"
	"github.com/ArpitK24/jot/internal/store"
)

// runDupes lists memories that look like duplicates of each other.
func runDupes(stores []*store.Store, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("dupes", flag.ContinueOnError)
	minScore := fs.Float64("min", dedup.Threshold, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pairs, why := dedup.PairsWith(all(stores), *minScore, semantic())
	warnSkipped(why)
	if len(pairs) == 0 {
		fmt.Fprintln(stdout, "no duplicates found")
		return nil
	}
	for _, p := range pairs {
		how := ""
		if p.Semantic {
			how = "  (same meaning)"
		}
		fmt.Fprintf(stdout, "%3.0f%%  %s  %s%s\n", p.Score*100, p.Keep.ID, describe(p.Keep), how)
		fmt.Fprintf(stdout, "      %s  %s\n", p.Drop.ID, describe(p.Drop))
		fmt.Fprintf(stdout, "      jot merge %s %s\n\n", p.Keep.ID, p.Drop.ID)
	}
	fmt.Fprintf(stdout, "%d possible %s. Merging keeps the first of each pair.\n", len(pairs), plural(len(pairs), "duplicate", "duplicates"))
	return nil
}

func describe(m *store.Memory) string {
	src := m.Source
	if src == "" {
		src = "?"
	}
	return fmt.Sprintf("%-7s %-12s %s", m.Scope, src, m.Title())
}

// runMerge folds one memory into another.
func runMerge(stores []*store.Store, args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	body := fs.String("body", "", "")
	dry := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: jot merge <keep-id> <drop-id> [--body text|-]")
	}
	keepID, dropID := fs.Arg(0), fs.Arg(1)
	ks, keep := find(stores, keepID)
	ds, drop := find(stores, dropID)
	if ks == nil {
		return fmt.Errorf("no memory with id %q", keepID)
	}
	if ds == nil {
		return fmt.Errorf("no memory with id %q", dropID)
	}
	text := *body
	if text == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		text = dedup.MergeBody(keep.Body, drop.Body)
	}
	tags := dedup.MergeTags(keep.Tags, drop.Tags)
	if *dry {
		fmt.Fprintln(stdout, strings.TrimSpace(text))
		return nil
	}
	var err error
	if ks == ds {
		_, err = ks.Merge(keepID, dropID, text, tags)
	} else {
		if _, err = ks.Update(keepID, text, "", tags); err == nil {
			err = ds.Delete(dropID)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "merged %s into %s\n", dropID, keepID)
	return nil
}
