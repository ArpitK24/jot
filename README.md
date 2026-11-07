# jot

**One memory for all your AI coding agents, kept as plain Markdown files on your machine.**

Tell Claude Code you use pnpm, and Cursor already knows it tomorrow. jot is a
small local MCP server that every agent you connect reads from and writes to.
No cloud, no account, no database: one `.md` file per memory, versioned in git.

## Quickstart (about 60 seconds)

1. **Install** the CLI (macOS or Linux):

   ```sh
   curl -fsSL https://raw.githubusercontent.com/ArpitK24/jot/main/scripts/install.sh | sh
   ```

   On Windows, in PowerShell:

   ```powershell
   irm https://raw.githubusercontent.com/ArpitK24/jot/main/scripts/install.ps1 | iex
   ```

   Both download the newest release from GitHub, check its SHA-256 against the
   release's `checksums.txt`, and install one binary. Other ways are under
   [Install](#install).

2. **Connect your tools:**

   ```sh
   jot setup
   ```

   It finds Claude Code, Cursor, Codex, Antigravity and Claude Desktop, prints
   the exact lines it will add to each config, and asks once before writing.

3. **Restart those tools.** That's it. From now on they are told to recall your
   preferences before a task and to save new ones as you work.

To check that everything is wired up, run `jot doctor`.

## What it looks like

Monday, in Claude Code:

```
you:    we use pnpm in this repo, never npm
claude: Noted. (calls jot remember)
```

jot writes `~/.jot/20261109-101502-3a1c.md`:

```markdown
---
id: 20261109-101502-3a1c
kind: preference
tags: [js, tooling]
source: claude-code
created: 2026-11-09T10:15:02Z
updated: 2026-11-09T10:15:02Z
---

Use pnpm, never npm, in JS projects.
```

Tuesday, in Cursor:

```
you:    add a lint script
cursor: (calls jot recall "add lint script to package.json")
        [20261109-101502-3a1c] (preference, from global memory, tags: js, tooling)
        Use pnpm, never npm, in JS projects.
cursor: Running pnpm add -D eslint ...
```

Whether an agent calls `recall` on its own is up to the agent. jot's server
tells every agent to, and `jot setup` also adds a short block to the
instructions file that Claude Code, Codex and Antigravity read every session.
Cursor and Claude Desktop rely on the server's instructions alone.

## FAQ

**Does anything leave my machine?** No. The jot CLI has no server, account or
telemetry; your agents start it as a local process. The only network call it
can make is to [Ollama](https://ollama.com) on `localhost`, if you turn on
semantic search. (The Windows desktop app also checks GitHub for updates when
it opens.)

**Where do the files live?** Global memory is in `~/.jot` (or `$JOT_HOME`).
Project memory, if you create it with `jot init --project`, is in
`<repo>/.jot`, and agents working in that repo search both. `jot where` prints
the stores in use. Each memory is a Markdown file you can read, edit or grep.

**How do I undo something?** Every change is a git commit in the store (when
git is installed). `jot log` shows the history; `git -C ~/.jot show <hash>`
shows an old version, and `git -C ~/.jot checkout <hash>^ -- <id>.md` brings
back a deleted memory. To disconnect jot from your tools, run
`jot setup --undo`; it removes only what it added, and the first time it edits
a config it keeps the original next to it as `<file>.jot-backup`.

**Which tools work?** `jot setup` connects Claude Code, Cursor, Codex,
Antigravity and Claude Desktop. Any other MCP client works with command `jot`,
args `["serve"]` over stdio; see [docs/setup.md](docs/setup.md).

**What do agents save?** jot asks them to save few, short, self-contained
memories: preferences you state, decisions, corrections, and non-obvious facts
about a project. Not task progress, code, logs or secrets. Similar memories
are caught before a second copy is saved, and `jot dupes` finds the rest.

**How do I see or change what's saved?** `jot ls`, `jot search <words>`,
`jot edit <id>` and `jot rm <id>`, the Windows desktop app, or any text
editor.

## Install

**macOS and Linux:** the one-line installer above, or download a
`jot_<version>_<os>_<arch>.tar.gz` from
[Releases](https://github.com/ArpitK24/jot/releases). The installer puts `jot`
in `~/.local/bin` (`/usr/local/bin` as root); set `JOT_INSTALL_DIR` to choose
another folder and `JOT_VERSION` to pin a version.

**Windows desktop app:** download the newest `jot-<version>-windows-x64-setup.exe`
from a `jot desktop` release on the [Releases](https://github.com/ArpitK24/jot/releases) page.
It installs the app and the `jot` CLI for your user, adds `jot` to PATH and
connects your AI tools. The build is unsigned, so SmartScreen may ask you to
click *More info*, then *Run anyway*. From 0.3.1 the app updates itself: it
checks GitHub when it opens and installs a new version in one click, after
checking its SHA-256. Light, dark or system appearance is in Settings.

**Windows CLI only:** the PowerShell one-liner above installs `jot.exe` to
`%LOCALAPPDATA%\jot\bin` and adds it to your user PATH.

**With Go 1.23+:**

```sh
go install github.com/ArpitK24/jot/cmd/jot@latest
```

It's also listed on the [official MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.ArpitK24/jot`, with one-click `.mcpb` bundles on every release.

### Connecting tools

```
$ jot setup
  Claude Code     will connect   ~/.claude.json
  Cursor          connected      ~/.cursor/mcp.json
  Codex           will connect   ~/.codex/config.toml
  Antigravity     not installed  ~/.gemini/config/mcp_config.json
  Claude Desktop  not installed  ~/Library/Application Support/Claude/claude_desktop_config.json
  ...
Connect 2 tools? [y/N]
```

Besides the MCP entry, `jot setup` adds a short marked block to the tool's own
instructions file, which the agent reads at the start of every session:
`~/.claude/CLAUDE.md` (Claude Code), `~/.codex/AGENTS.md` (Codex) and
`~/.gemini/GEMINI.md` (Antigravity). Undo both with `jot setup --undo`. To
configure a tool by hand, see [docs/setup.md](docs/setup.md).

`jot doctor` checks the store, each tool's entry and instructions block, that
the configured `jot` exists and answers MCP requests, and semantic search, and
prints the exact command that fixes each problem:

```
$ jot doctor
...
Tools
  ✓ Claude Code      MCP entry in ~/.claude.json
  ✗                  jot block missing or outdated in ~/.claude/CLAUDE.md
      fix: jot setup claude-code
  ✓ Cursor           MCP entry in ~/.cursor/mcp.json
Server
  ✓ jot serve        ~/.local/bin/jot answers initialize and tools/list (jot <version>, 5 tools)
```

## How it works

```
~/.jot/                      global memory (or $JOT_HOME)
  20261103-134832-fb9e.md
  .git/
<repo>/.jot/                 optional project memory (jot init --project)
```

A memory file:

```markdown
---
id: 20261103-134832-fb9e
kind: preference
tags: [js, tooling]
source: claude-code
created: 2026-11-03T13:48:32Z
updated: 2026-11-03T13:48:32Z
---

Use pnpm, never npm.
```

Recall uses BM25 ranking with boosts for tag matches and recent updates,
across project memories and global ones, and says which store each result
came from. An agent started inside a repo with a `.jot` folder gets that
repo's memories; clients that report their workspace
through MCP roots get them even when they start jot elsewhere.

### MCP tools

| Tool       | What it does                                         |
|------------|------------------------------------------------------|
| `remember` | Save one preference, decision, fact or note. If a similar memory exists, it saves nothing and points to that one instead. It refuses empty text, more than 600 characters, and anything that looks like a key or password |
| `recall`   | Search memory for what's relevant to the task        |
| `update`   | Rewrite a memory that changed                        |
| `forget`   | Delete a memory                                      |
| `list`     | Newest memories, filtered by kind or tags            |

### CLI

```
jot init [--project]     jot search <query>     jot show <id>     jot log
jot add [-k -t -p] text  jot ls [-k -t]         jot edit <id>     jot where
jot serve                jot rm <id>            jot version
jot setup [--undo]       jot dupes              jot merge <keep> <drop>
jot doctor
```

### Semantic search (optional)

Keyword search is built in and needs nothing. To also find notes that say
the same thing in other words, run [Ollama](https://ollama.com) and turn
semantic search on:

```sh
ollama pull all-minilm   # 46 MB
jot embed on             # or: Settings → Search in the desktop app
jot embed status
```

Search, `recall`, `jot dupes` and the `remember` duplicate guard then use
keyword and semantic matches together. If Ollama isn't running, jot falls
back to keyword search. Vectors are cached in your user cache folder, never
in the store, and nothing leaves your machine. Use `--model` to pick another
Ollama embedding model (`embeddinggemma` and `nomic-embed-text` are tuned
too) and `jot embed off` to turn it off.

### Duplicates

Agents often save the same thing twice in different words. `jot dupes` lists
likely pairs, and `jot merge` keeps the newer one, adds any sentence only the
other one had, and removes it in one commit. Git still has the original.

```
$ jot dupes
 66%  20261106-091200-a3f9  global  claude-code  Use pnpm for every JS project...
      20261104-180511-7be0  global  cursor       Use pnpm for every JS project...
      jot merge 20261106-091200-a3f9 20261104-180511-7be0
```

### Ask (chat with your notes)

`jot ask` answers a question from your own memories. It recalls the notes that
match, sends them with the question to a model you choose, and streams the
answer. The desktop app has the same thing as an Ask view, with a "Used N
notes" list under each reply and a Remember button to save a fact from it.

```
$ jot ask "which package manager do we use?"
$ jot ask -m llama3.2 -c <chat-id> "and for Python?"   # continue a chat
$ jot chats                                             # list saved chats
$ jot models                                            # what you can pick
```

Models: Ollama (local, no key), any OpenAI-compatible server (OpenAI,
OpenRouter, Groq, LM Studio) and Anthropic. Keys live in
`<user config dir>/jot/keys.json` with owner-only permissions, never in the
store; `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` also work, and a saved key
wins over them. In the desktop app, set them in Settings → Models.

Chats are Markdown files in the global store's `chats/` folder, versioned like
everything else. They never show up in Notes, duplicate checks, or what agents
get from `recall`, so a chat can't pollute your agents' memory. Only what you
choose to Remember becomes a note.

## Roadmap

- [x] Merge near-duplicate memories instead of piling them up
- [x] `jot setup` to connect installed agents in one step
- [x] Desktop app for Windows (macOS and Linux next)
- [ ] Expiry and review for stale facts
- [x] Optional local embeddings (Ollama) for semantic recall
- [x] Ask: chat with your notes using Ollama, OpenAI-compatible or Anthropic models
- [ ] `jot sync` through a git remote across machines
- [ ] TUI browser

## License

MIT
