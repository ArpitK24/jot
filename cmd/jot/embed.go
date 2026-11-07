package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/store"
)

// semantic returns the embedding index when the user turned it on, else nil.
func semantic() *embed.Index {
	return embed.Open(embed.Load(store.GlobalDir()))
}

// warnSkipped tells the user, on stderr, why a search fell back to keywords.
func warnSkipped(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "jot: keyword results only (%v)\n", err)
	}
}

// runEmbed turns semantic search on or off, shows its state, or embeds
// every memory ahead of time.
func runEmbed(stores []*store.Store, args []string, stdout io.Writer) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	global := store.GlobalDir()
	switch sub {
	case "on":
		fs := flag.NewFlagSet("embed on", flag.ContinueOnError)
		model := fs.String("model", embed.DefaultModel, "")
		url := fs.String("url", embed.DefaultURL, "")
		if err := fs.Parse(args); err != nil {
			return err
		}
		cfg := embed.Config{Provider: "ollama", Model: *model, URL: *url}
		if err := embed.Save(global, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "semantic search on (Ollama %s at %s)\n", *model, *url)
		ix := embed.Open(cfg)
		if err := ix.Check(context.Background()); err != nil {
			fmt.Fprintf(stdout, "but the model isn't reachable yet: %v\n", err)
			fmt.Fprintf(stdout, "install Ollama from https://ollama.com, run `ollama pull %s`, then `jot embed index`\n", *model)
			return nil
		}
		return index(stores, ix, stdout)
	case "off":
		if err := embed.Save(global, embed.Config{}); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "semantic search off; jot uses keyword search only")
		return nil
	case "index":
		ix := semantic()
		if ix == nil {
			return errors.New("semantic search is off; run `jot embed on` first")
		}
		return index(stores, ix, stdout)
	case "status":
		cfg := embed.Load(global)
		if !cfg.On() {
			fmt.Fprintln(stdout, "semantic search: off (keyword search only)")
			fmt.Fprintln(stdout, "turn it on with `jot embed on` (needs Ollama running locally)")
			return nil
		}
		ix := embed.Open(cfg)
		fmt.Fprintf(stdout, "semantic search: on\nmodel:  %s\nollama: %s\ncached: %d vectors\n", ix.Model(), ix.URL(), ix.Cached())
		if err := ix.Check(context.Background()); err != nil {
			fmt.Fprintf(stdout, "state:  not reachable, searches fall back to keywords\n        %v\n", err)
		} else {
			fmt.Fprintln(stdout, "state:  ready")
		}
		return nil
	}
	return fmt.Errorf("usage: jot embed [status|on|off|index]")
}

func index(stores []*store.Store, ix *embed.Index, stdout io.Writer) error {
	mems := all(stores)
	texts := make([]string, len(mems))
	for i, m := range mems {
		texts[i] = m.Body
	}
	before := ix.Cached()
	t := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := ix.Documents(ctx, texts); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "indexed %d memories (%d new) in %s\n", len(mems), ix.Cached()-before, time.Since(t).Round(time.Millisecond))
	return nil
}
