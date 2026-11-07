package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ArpitK24/jot/internal/ask"
	"github.com/ArpitK24/jot/internal/chat"
	"github.com/ArpitK24/jot/internal/store"
)

// stderr is where progress notes go, so stdout carries only the answer.
var stderr io.Writer = os.Stderr

func session(stores []*store.Store) *ask.Session {
	global := store.GlobalDir()
	return &ask.Session{
		Chats:    &ask.Chats{Store: store.Open(global, "global")},
		Notes:    func() []*store.Memory { return all(stores) },
		Index:    semantic(),
		Registry: chat.NewRegistry(global),
	}
}

// runAsk answers one question with the user's notes, streaming to stdout.
func runAsk(stores []*store.Store, args []string, stdin io.Reader, stdout io.Writer) error {
	var model, chatID string
	noRecall := false
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "-m" || a == "--model" || a == "-model":
			model, err = next()
		case strings.HasPrefix(a, "--model=") || strings.HasPrefix(a, "-m="):
			model = a[strings.Index(a, "=")+1:]
		case a == "-c" || a == "--chat" || a == "-chat":
			chatID, err = next()
		case a == "--no-recall" || a == "-no-recall":
			noRecall = true
		case a == "--":
			words = append(words, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-") && a != "-":
			return fmt.Errorf("unknown flag %s\nusage: jot ask [-m model] [-c chat] [--no-recall] <question>", a)
		default:
			words = append(words, a)
		}
		if err != nil {
			return err
		}
	}
	text := strings.Join(words, " ")
	if text == "-" || (text == "" && stdin != nil && !isTerminal(stdin)) {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("usage: jot ask [-m model] [-c chat] [--no-recall] <question>")
	}

	s := session(stores)
	var c *ask.Conversation
	if chatID != "" {
		var err error
		if c, err = s.Chats.Get(chatID); err != nil {
			return fmt.Errorf("chat %s: %v", chatID, err)
		}
	} else {
		c = s.Chats.New(time.Now())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	wrote := false
	rep, err := s.Send(ctx, c, text, ask.Options{
		Model:    model,
		NoRecall: noRecall,
		OnRecall: func(r ask.Recalled, m string) {
			if r.Skipped != nil {
				fmt.Fprintf(stderr, "jot: keyword recall only (%v)\n", r.Skipped)
			}
		},
		OnDelta: func(d string) {
			if !wrote {
				d = strings.TrimLeft(d, " \n")
			}
			if d != "" {
				wrote = true
				io.WriteString(stdout, d)
			}
		},
	})
	if wrote {
		fmt.Fprintln(stdout)
	}
	if rep != nil {
		printSources(rep)
		if rep.Stopped {
			fmt.Fprintln(stderr, "jot: stopped")
		}
		if rep.Suggest != "" {
			fmt.Fprintf(stderr, "jot: worth remembering? jot add %q\n", rep.Suggest)
		}
	}
	if len(c.Turns) > 0 {
		fmt.Fprintf(stderr, "jot: chat %s (continue with: jot ask -c %s ...)\n", c.ID, c.ID)
	}
	return err
}

func printSources(rep *ask.Reply) {
	n := len(rep.Recall.Notes)
	if n == 0 && len(rep.Recall.Past) == 0 {
		fmt.Fprintf(stderr, "jot: %s, no notes used\n", rep.Model)
		return
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	fmt.Fprintf(stderr, "jot: %s, used %d note%s", rep.Model, n, plural)
	if p := len(rep.Recall.Past); p > 0 {
		fmt.Fprintf(stderr, " and %d earlier chat%s", p, map[bool]string{true: "", false: "s"}[p == 1])
	}
	fmt.Fprintln(stderr)
	for _, m := range rep.Recall.Notes {
		fmt.Fprintf(stderr, "  %s  %s\n", m.ID, m.Title())
	}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runChats lists saved chats, prints one, or deletes one.
func runChats(args []string, stdout io.Writer) error {
	cs := &ask.Chats{Store: store.Open(store.GlobalDir(), "global")}
	if len(args) >= 1 && (args[0] == "show" || args[0] == "rm") {
		if len(args) != 2 {
			return fmt.Errorf("usage: jot chats %s <id>", args[0])
		}
		c, err := cs.Get(args[1])
		if err != nil {
			return fmt.Errorf("chat %s: %v", args[1], err)
		}
		if args[0] == "show" {
			stdout.Write(c.Marshal())
			return nil
		}
		if err := cs.Delete(c.ID); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "forgot chat", c.ID)
		return nil
	}
	if len(args) > 0 {
		return errors.New("usage: jot chats [show|rm <id>]")
	}
	list, err := cs.List()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "no chats yet; try: jot ask \"what do my notes say about ...\"")
		return nil
	}
	for _, c := range list {
		fmt.Fprintf(stdout, "%s  %s  %2d turns  %s\n", c.ID, c.Updated.Local().Format("2006-01-02 15:04"), len(c.Turns), c.Title)
	}
	return nil
}

// runModels lists the models each provider offers, or sets the default.
func runModels(args []string, stdin io.Reader, stdout io.Writer) error {
	global := store.GlobalDir()
	reg := chat.NewRegistry(global)
	if len(args) >= 1 && args[0] == "url" {
		if len(args) != 3 || (args[1] != chat.Ollama && args[1] != chat.OpenAI && args[1] != chat.Anthropic) {
			return errors.New("usage: jot models url ollama|openai|anthropic <url>")
		}
		cfg := reg.Config
		switch args[1] {
		case chat.Ollama:
			cfg.Ollama.URL = args[2]
		case chat.OpenAI:
			cfg.OpenAI.BaseURL = args[2]
		case chat.Anthropic:
			cfg.Anthropic.BaseURL = args[2]
		}
		if err := chat.SaveConfig(global, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s url: %s\n", args[1], args[2])
		return nil
	}
	if len(args) >= 1 && args[0] == "key" {
		if len(args) != 2 || (args[1] != chat.OpenAI && args[1] != chat.Anthropic) {
			return errors.New("usage: jot models key openai|anthropic  (reads the key from stdin; an empty line removes it)")
		}
		if isTerminal(stdin) {
			fmt.Fprintf(stderr, "paste the %s key and press Enter (it is visible as you type): ", args[1])
		}
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" && !errors.Is(err, io.EOF) {
			return err
		}
		key := strings.TrimSpace(line)
		if err := reg.Keys.Set(args[1], key); err != nil {
			return err
		}
		if key == "" {
			fmt.Fprintf(stdout, "removed the saved %s key\n", args[1])
		} else {
			fmt.Fprintf(stdout, "saved %s key %s in %s\n", args[1], chat.Mask(key), tildePath(chat.KeysPath()))
		}
		return nil
	}
	if len(args) >= 1 && args[0] == "default" {
		if len(args) != 2 {
			return errors.New("usage: jot models default <provider/model>")
		}
		ref, err := chat.ParseRef(args[1])
		if err != nil {
			return err
		}
		cfg := reg.Config
		cfg.Default = ref.String()
		if err := chat.SaveConfig(global, cfg); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "default model:", ref)
		return nil
	}
	if len(args) > 0 {
		return errors.New("usage: jot models [default <provider/model> | url <provider> <url> | key <provider>]")
	}
	sts := reg.CheckAll(context.Background())
	def, _ := reg.DefaultModel(context.Background(), sts)
	for _, st := range sts {
		state := "ready"
		if !st.Ready {
			state = st.Detail
		}
		key := ""
		if st.Key != "" {
			key = "  key " + st.Key
			if st.KeySource != "saved" {
				key += " (" + st.KeySource + ")"
			}
		}
		fmt.Fprintf(stdout, "%-18s %s  %s%s\n", st.Label, st.URL, state, key)
		for _, m := range st.Models {
			ref := chat.Ref{Provider: st.Provider, Model: m}.String()
			mark := " "
			if ref == def {
				mark = "*"
			}
			fmt.Fprintf(stdout, "  %s %s\n", mark, ref)
		}
	}
	if def == "" {
		fmt.Fprintln(stdout, "\nno model available yet:", chat.ErrNoModel)
	} else {
		fmt.Fprintf(stdout, "\n* default: %s\n", def)
	}
	fmt.Fprintf(stdout, "keys: %s, or %s / %s\n", tildePath(chat.KeysPath()), chat.EnvName(chat.OpenAI), chat.EnvName(chat.Anthropic))
	return nil
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
