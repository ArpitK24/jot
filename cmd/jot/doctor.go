package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArpitK24/jot/internal/embed"
	"github.com/ArpitK24/jot/internal/setup"
	"github.com/ArpitK24/jot/internal/store"
)

// A check is one line of `jot doctor` output.
type check struct {
	mark   string // ok, fail, warn or skip
	label  string
	detail string
	fix    string // exact command (or step) that clears a failure
}

const (
	markOK   = "✓"
	markFail = "✗"
	markWarn = "!"
	markSkip = "-"
)

// serveTimeout bounds how long doctor waits for `jot serve` to answer.
var serveTimeout = 10 * time.Second

// runDoctor checks that jot is installed and wired up, and prints the
// exact command that fixes each problem. It returns an error when any
// check fails, so scripts can use the exit code.
func runDoctor(stores []*store.Store, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	offline := fs.Bool("no-serve", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	clients, err := setup.Clients()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "jot %s doctor\n", version)

	failed := section(stdout, "Store", storeChecks(stores))
	tools, commands := toolChecks(clients, self())
	failed += section(stdout, "Tools", tools)
	if !*offline {
		if len(commands) == 0 {
			commands = []string{self()}
		}
		var srv []check
		for _, c := range commands {
			srv = append(srv, serveCheck(c))
		}
		failed += section(stdout, "Server", srv)
	}
	failed += section(stdout, "Search", []check{embedCheck()})

	if failed > 0 {
		fmt.Fprintf(stdout, "\n%d %s to fix. Run the fix lines above, then `jot doctor` again.\n", failed, plural(failed, "problem", "problems"))
		return errDoctor
	}
	fmt.Fprintln(stdout, "\nAll good.")
	return nil
}

var errDoctor = errors.New("doctor found problems")

// section prints one group of checks and returns how many failed.
func section(w io.Writer, title string, cs []check) int {
	fmt.Fprintf(w, "\n%s\n", title)
	failed := 0
	for _, c := range cs {
		line := fmt.Sprintf("  %s %-16s %s", c.mark, c.label, c.detail)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if c.fix != "" {
			fmt.Fprintf(w, "      fix: %s\n", c.fix)
		}
		if c.mark == markFail {
			failed++
		}
	}
	return failed
}

func storeChecks(stores []*store.Store) []check {
	var out []check
	gitPath, gitErr := exec.LookPath("git")
	for _, st := range stores {
		label := st.Scope + " store"
		if !st.Exists() {
			if st.Scope == "global" {
				out = append(out, check{mark: markWarn, label: label, detail: tilde(st.Dir) + " not created yet; jot setup or the first save creates it", fix: "jot init   (optional)"})
			}
			continue
		}
		ms, err := st.List()
		if err != nil {
			out = append(out, check{mark: markFail, label: label, detail: fmt.Sprintf("%s: %v", tilde(st.Dir), err)})
			continue
		}
		n := fmt.Sprintf("%d %s", len(ms), plural(len(ms), "memory", "memories"))
		_, gerr := os.Stat(filepath.Join(st.Dir, ".git"))
		switch {
		case gerr == nil:
			out = append(out, check{mark: markOK, label: label, detail: fmt.Sprintf("%s (%s, git history on)", tilde(st.Dir), n)})
		case gitErr != nil:
			out = append(out, check{mark: markWarn, label: label, detail: fmt.Sprintf("%s (%s, no git history)", tilde(st.Dir), n)})
		default:
			fix := "jot init"
			if st.Scope == "project" {
				fix = "jot init --project   (from the repo root)"
			}
			out = append(out, check{mark: markFail, label: label, detail: fmt.Sprintf("%s (%s) is not a git repo, so changes can't be undone", tilde(st.Dir), n), fix: fix})
		}
	}
	if gitErr != nil {
		out = append(out, check{mark: markWarn, label: "git", detail: "not found; memories are saved but not versioned", fix: "install git (https://git-scm.com), then run `jot init`"})
	} else {
		out = append(out, check{mark: markOK, label: "git", detail: gitPath})
	}
	return out
}

// toolChecks checks each installed tool's MCP entry and instructions block.
// It also returns the distinct commands the tools are configured to run.
func toolChecks(clients []setup.Client, me string) ([]check, []string) {
	var out []check
	var commands []string
	seen := map[string]bool{}
	connected, installed := 0, 0
	for _, c := range clients {
		if !c.Installed() {
			out = append(out, check{mark: markSkip, label: c.Name, detail: "not installed"})
			continue
		}
		installed++
		fix := "jot setup " + c.ID
		e, err := setup.Inspect(c)
		switch {
		case err != nil:
			out = append(out, check{mark: markFail, label: c.Name, detail: err.Error(), fix: "fix the JSON/TOML syntax in " + tilde(c.Config) + ", then " + fix})
			continue
		case !e.Found:
			out = append(out, check{mark: markFail, label: c.Name, detail: "no jot entry in " + tilde(c.Config), fix: fix})
			continue
		case len(e.Args) != 1 || e.Args[0] != "serve":
			out = append(out, check{mark: markFail, label: c.Name, detail: fmt.Sprintf("jot entry runs %q with args %v, want [serve]", e.Command, e.Args), fix: fix})
			continue
		}
		connected++
		path, err := resolve(e.Command)
		if err != nil {
			out = append(out, check{mark: markFail, label: c.Name, detail: fmt.Sprintf("command %q in %s not found", e.Command, tilde(c.Config)), fix: fix})
		} else {
			ck := check{mark: markOK, label: c.Name, detail: "MCP entry in " + tilde(c.Config)}
			if !samePath(path, me) {
				ck.mark = markWarn
				ck.detail += fmt.Sprintf(" runs a different jot (%s)", tilde(path))
				ck.fix = fix + "   (to use this one, " + tilde(me) + ")"
			}
			out = append(out, ck)
			if !seen[path] {
				seen[path] = true
				commands = append(commands, path)
			}
		}
		if c.Rules != "" {
			ok, err := setup.RulesCurrent(c)
			switch {
			case err != nil:
				out = append(out, check{mark: markFail, label: "", detail: fmt.Sprintf("%s: %v", tilde(c.Rules), err)})
			case ok:
				out = append(out, check{mark: markOK, label: "", detail: "jot block in " + tilde(c.Rules)})
			default:
				out = append(out, check{mark: markFail, label: "", detail: "jot block missing or outdated in " + tilde(c.Rules), fix: fix})
			}
		}
	}
	switch {
	case installed == 0:
		out = append(out, check{mark: markWarn, label: "connected", detail: "none of the tools jot knows is installed here",
			fix: "connect any other MCP client by hand: command `jot`, args [\"serve\"] (see docs/setup.md)"})
	case connected == 0:
		out = append(out, check{mark: markFail, label: "connected", detail: "no AI tool is connected to jot yet", fix: "jot setup"})
	}
	return out, commands
}

// resolve finds the executable a config's command names.
func resolve(command string) (string, error) {
	if command == "" {
		return "", errors.New("empty command")
	}
	p, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

func samePath(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return os.SameFile(fa, fb)
}

// serveCheck starts `<command> serve`, sends initialize and tools/list,
// and checks the answers, the same way an MCP client would.
func serveCheck(command string) check {
	label := "jot serve"
	tools, ver, err := handshake(command)
	if err != nil {
		return check{mark: markFail, label: label, detail: fmt.Sprintf("%s serve: %v", tilde(command), err),
			fix: "run `" + command + " serve` by hand to see the error; reinstall jot if it won't start"}
	}
	want := []string{"remember", "recall", "update", "forget", "list"}
	var missing []string
	for _, w := range want {
		if !contains(tools, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		return check{mark: markFail, label: label, detail: fmt.Sprintf("%s answers but lacks tools %v", tilde(command), missing), fix: "reinstall jot, then jot setup"}
	}
	return check{mark: markOK, label: label, detail: fmt.Sprintf("%s answers initialize and tools/list (jot %s, %d tools)", tilde(command), ver, len(tools))}
}

func handshake(command string) (tools []string, version string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), serveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "serve")
	cmd.Env = append(os.Environ(), "JOT_SOURCE=doctor")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	waited := false
	wait := func() {
		if !waited {
			waited = true
			in.Close()
			cmd.Wait()
		}
	}
	defer wait()
	io.WriteString(in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"jot-doctor","version":"1"}}}`+"\n"+
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")

	type reply struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	got := 0
	for got < 2 && sc.Scan() {
		var r reply
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.Error != nil {
			return nil, "", fmt.Errorf("request %d failed: %s", r.ID, r.Error.Message)
		}
		switch r.ID {
		case 1:
			var init struct {
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}
			json.Unmarshal(r.Result, &init)
			if init.ServerInfo.Name != "jot" {
				return nil, "", fmt.Errorf("initialize answered by %q, not jot", init.ServerInfo.Name)
			}
			version = init.ServerInfo.Version
			got++
		case 2:
			var list struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			json.Unmarshal(r.Result, &list)
			for _, t := range list.Tools {
				tools = append(tools, t.Name)
			}
			got++
		}
	}
	if got < 2 {
		if ctx.Err() != nil {
			return nil, "", fmt.Errorf("no answer within %s", serveTimeout)
		}
		wait() // stderr is complete only once the process is reaped
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "exited without answering"
		}
		return nil, "", errors.New(msg)
	}
	return tools, version, nil
}

func embedCheck() check {
	cfg := embed.Load(store.GlobalDir())
	if !cfg.On() {
		return check{mark: markSkip, label: "semantic search", detail: "off, keyword search only (optional: jot embed on, needs Ollama)"}
	}
	ix := embed.Open(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ix.Check(ctx); err != nil {
		return check{mark: markFail, label: "semantic search", detail: fmt.Sprintf("on, but %s at %s isn't reachable: %v", ix.Model(), ix.URL(), err),
			fix: "start Ollama and run `ollama pull " + ix.Model() + "`, or turn it off with `jot embed off`"}
	}
	return check{mark: markOK, label: "semantic search", detail: fmt.Sprintf("on (%s at %s, %d cached vectors)", ix.Model(), ix.URL(), ix.Cached())}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
