// Command jot is shared, local-first memory for AI agents.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/mcp"
	"github.com/ArpitK24/jot/internal/search"
	"github.com/ArpitK24/jot/internal/store"
)

// version is set by release builds with -ldflags "-X main.version=...".
var version = ""

func init() {
	if version != "" {
		return
	}
	version = "dev"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

const usage = `jot - shared memory for every AI agent

Usage:
  jot init [--project]            create the global store (or ./.jot for this repo)
  jot add [flags] <text>          remember something (reads stdin when text is "-")
      -k, --kind  fact|preference|decision|note
      -t, --tags  comma,separated
      -p, --project               save to this repo's store instead of global
  jot search <query> [-n 10]      find memories
  jot ls [-k kind] [-t tags]      list newest memories
  jot show <id>                   print one memory
  jot edit <id>                   open a memory in $EDITOR
  jot rm <id>                     forget a memory (git history keeps it)
  jot log [-n 20]                 history of changes
  jot dupes [--min 0.6]           list memories that look like duplicates
  jot merge <keep> <drop>         fold <drop> into <keep> (git history keeps it)
      --body text|-               use this text instead of the automatic merge
      --dry-run                   print the merged text, change nothing
  jot where                       print the stores in use
  jot embed [status|on|off|index] semantic search through a local Ollama model
      on [--model all-minilm] [--url http://localhost:11434]
  jot setup [tools...]            connect jot to the AI tools on this machine
      --dry-run                   show the changes, write nothing
      -y, --yes                   don't ask before writing
      --undo                      remove jot from those tools again
  jot doctor                      check the store, each tool's connection and the server
      --no-serve                  skip starting "jot serve" to test it
  jot serve                       run the MCP server on stdio
  jot version
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "jot:", err)
		os.Exit(1)
	}
}

func run(argv []string, stdin io.Reader, stdout io.Writer) error {
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help" {
		fmt.Fprint(stdout, usage)
		return nil
	}
	cwd, _ := os.Getwd()
	stores := store.Stores(cwd)
	cmd, args := argv[0], argv[1:]

	switch cmd {
	case "version", "--version":
		fmt.Fprintln(stdout, "jot", version)
		return nil

	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		project := fs.Bool("project", false, "")
		if err := fs.Parse(args); err != nil {
			return err
		}
		st := store.Open(store.GlobalDir(), "global")
		if *project {
			st = store.Open(store.DirName, "project")
		}
		if err := st.Init(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "jot %s store ready at %s\n", st.Scope, st.Dir)
		return nil

	case "add":
		fs := flag.NewFlagSet("add", flag.ContinueOnError)
		kind := fs.String("kind", store.KindNote, "")
		fs.StringVar(kind, "k", store.KindNote, "")
		tags := fs.String("tags", "", "")
		fs.StringVar(tags, "t", "", "")
		project := fs.Bool("project", false, "")
		fs.BoolVar(project, "p", false, "")
		if err := fs.Parse(args); err != nil {
			return err
		}
		text := strings.Join(fs.Args(), " ")
		if text == "-" || text == "" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return err
			}
			text = string(b)
		}
		st := pick(stores, *project)
		if st == nil {
			return errors.New("no project store here; run `jot init --project` first")
		}
		m, err := st.Add(text, *kind, splitTags(*tags), "cli")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "remembered %s (%s)\n", m.ID, m.Scope)
		return nil

	case "search", "ls":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		n := fs.Int("n", 10, "")
		kind := fs.String("k", "", "")
		tags := fs.String("t", "", "")
		if err := fs.Parse(args); err != nil {
			return err
		}
		query := strings.Join(fs.Args(), " ")
		if cmd == "search" && query == "" {
			return errors.New("usage: jot search <query>")
		}
		if cmd == "ls" {
			query = ""
		}
		opt := search.Options{Limit: *n, Kind: *kind, Tags: splitTags(*tags)}
		var why error
		if query != "" {
			opt.Index, opt.Err = semantic(), &why
		}
		res := search.Search(all(stores), query, opt)
		warnSkipped(why)
		if len(res) == 0 {
			fmt.Fprintln(stdout, "nothing found")
			return nil
		}
		for _, r := range res {
			m := r.Memory
			tag := ""
			if len(m.Tags) > 0 {
				tag = "  #" + strings.Join(m.Tags, " #")
			}
			fmt.Fprintf(stdout, "%s  %-10s %-7s %s%s\n", m.ID, m.Kind, m.Scope, m.Title(), tag)
		}
		return nil

	case "show", "rm", "edit":
		if len(args) != 1 {
			return fmt.Errorf("usage: jot %s <id>", cmd)
		}
		id := args[0]
		st, m := find(stores, id)
		if st == nil {
			return fmt.Errorf("no memory with id %q", id)
		}
		switch cmd {
		case "show":
			stdout.Write(m.Marshal())
		case "rm":
			if err := st.Delete(id); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "forgot", id)
		case "edit":
			ed := os.Getenv("EDITOR")
			if ed == "" {
				ed = "vi"
			}
			path := st.Dir + string(os.PathSeparator) + id + ".md"
			c := exec.Command(ed, path)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := c.Run(); err != nil {
				return err
			}
			if _, err := st.Update(id, "", "", nil); err != nil { // bump updated + commit
				return err
			}
			fmt.Fprintln(stdout, "saved", id)
		}
		return nil

	case "log":
		fs := flag.NewFlagSet("log", flag.ContinueOnError)
		n := fs.Int("n", 20, "")
		if err := fs.Parse(args); err != nil {
			return err
		}
		for _, st := range stores {
			out, err := st.Log(*n)
			if err != nil {
				continue
			}
			fmt.Fprintf(stdout, "== %s (%s)\n%s\n", st.Scope, st.Dir, out)
		}
		return nil

	case "where":
		for _, st := range stores {
			state := "missing"
			if st.Exists() {
				state = "ok"
			}
			fmt.Fprintf(stdout, "%-7s %s (%s)\n", st.Scope, st.Dir, state)
		}
		if c := embed.Load(store.GlobalDir()); c.On() {
			fmt.Fprintf(stdout, "search  keyword + semantic (%s)\n", c.Model)
		} else {
			fmt.Fprintln(stdout, "search  keyword")
		}
		return nil

	case "embed":
		return runEmbed(stores, args, stdout)

	case "dupes":
		return runDupes(stores, args, stdout)

	case "merge":
		return runMerge(stores, args, stdin, stdout)

	case "setup":
		return runSetup(args, stdin, stdout)

	case "doctor":
		return runDoctor(stores, args, stdout)

	case "serve":
		src := os.Getenv("JOT_SOURCE")
		if src == "" {
			src = "mcp"
		}
		srv := &mcp.Server{Version: version, Source: src, Index: semantic(), Dir: cwd, Resolve: store.Stores}
		return srv.Serve(stdin, stdout)
	}
	return fmt.Errorf("unknown command %q (try `jot help`)", cmd)
}

func pick(stores []*store.Store, project bool) *store.Store {
	want := "global"
	if project {
		want = "project"
	}
	for _, st := range stores {
		if st.Scope == want {
			return st
		}
	}
	return nil
}

func find(stores []*store.Store, id string) (*store.Store, *store.Memory) {
	for _, st := range stores {
		if m, err := st.Get(id); err == nil {
			return st, m
		}
	}
	return nil, nil
}

func all(stores []*store.Store) []*store.Memory {
	var out []*store.Memory
	for _, st := range stores {
		ms, _ := st.List()
		out = append(out, ms...)
	}
	return out
}

func splitTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
