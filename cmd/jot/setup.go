package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArpitK24/jot/internal/setup"
)

// runSetup connects jot to the AI tools found on this machine.
func runSetup(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	fs.BoolVar(yes, "y", false, "")
	undo := fs.Bool("undo", false, "")
	command := fs.String("command", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	clients, err := setup.Clients()
	if err != nil {
		return err
	}
	clients, err = only(clients, fs.Args())
	if err != nil {
		return err
	}

	if *undo {
		for _, c := range clients {
			removed, err := setup.Remove(c)
			switch {
			case err != nil:
				fmt.Fprintf(stdout, "  %-15s %v\n", c.Name, err)
			case removed:
				fmt.Fprintf(stdout, "  %-15s removed from %s\n", c.Name, tilde(c.Config))
				if c.Rules != "" {
					fmt.Fprintf(stdout, "  %-15s and its block in %s, if any\n", "", tilde(c.Rules))
				}
			}
		}
		return nil
	}

	if *command == "" {
		*command = self()
	}
	var todo []setup.Plan
	for _, c := range clients {
		p, err := setup.Check(c, *command)
		if err != nil {
			fmt.Fprintf(stdout, "  %-15s %-14s %v\n", c.Name, "error", err)
			continue
		}
		fmt.Fprintf(stdout, "  %-15s %-14s %s\n", c.Name, p.State, tilde(c.Config))
		if p.State == setup.Absent || p.State == setup.Different {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(stdout, "\nNothing to change.")
		return nil
	}
	for _, p := range todo {
		fmt.Fprintf(stdout, "\n%s\n", tilde(p.Client.Config))
		for _, line := range strings.Split(strings.TrimRight(p.Snippet, "\n"), "\n") {
			fmt.Fprintln(stdout, strings.TrimRight("+ "+line, " "))
		}
		if p.Rules {
			fmt.Fprintf(stdout, "\n%s  (so the agent checks jot on its own)\n", tilde(p.Client.Rules))
			for _, line := range strings.Split(strings.TrimRight(setup.RulesBlock, "\n"), "\n") {
				fmt.Fprintln(stdout, strings.TrimRight("+ "+line, " "))
			}
		}
	}
	if *dry {
		return nil
	}
	if !*yes {
		fmt.Fprintf(stdout, "\nConnect %d %s? [y/N] ", len(todo), plural(len(todo), "tool", "tools"))
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "Nothing changed.")
			return nil
		}
	}
	fmt.Fprintln(stdout)
	failed := 0
	for _, p := range todo {
		if err := setup.Apply(p); err != nil {
			failed++
			fmt.Fprintf(stdout, "  %-15s %v\n", p.Client.Name, err)
			continue
		}
		fmt.Fprintf(stdout, "  %-15s connected\n", p.Client.Name)
	}
	if failed > 0 {
		return fmt.Errorf("%d %s failed", failed, plural(failed, "tool", "tools"))
	}
	fmt.Fprintln(stdout, "\nRestart those tools to load jot. Undo any time with `jot setup --undo`.")
	return nil
}

func only(all []setup.Client, ids []string) ([]setup.Client, error) {
	if len(ids) == 0 {
		return all, nil
	}
	var out []setup.Client
	for _, id := range ids {
		found := false
		for _, c := range all {
			if c.ID == id {
				out = append(out, c)
				found = true
			}
		}
		if !found {
			var known []string
			for _, c := range all {
				known = append(known, c.ID)
			}
			return nil, fmt.Errorf("unknown tool %q (known: %s)", id, strings.Join(known, ", "))
		}
	}
	return out, nil
}

// self is the absolute path of this binary, so GUI apps that don't
// inherit the shell's PATH can still start it.
func self() string {
	p, err := os.Executable()
	if err != nil {
		return "jot"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
