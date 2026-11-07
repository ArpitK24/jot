# jot

**Shared, local-first memory for every AI agent.**

Tell Claude Code you use pnpm, and Cursor already knows it tomorrow. jot is one
memory on your machine that every MCP-capable agent can read and write. It's
stored as plain Markdown and versioned in git.

```
$ jot add -k preference -t js "Use pnpm, never npm"
remembered 20261103-134832-fb9e (global)

$ jot search "package manager"
20261103-134832-fb9e  preference global  Use pnpm, never npm  #js
```

## Why

- **One memory, every agent.** Claude Code, Cursor, Codex and any MCP client share it.
- **Yours, on disk.** Each memory is a readable `.md` file. No cloud, no account, no database.
- **Git-versioned.** Every change is a commit, so you get diffs, blame and undo for free.
- **Zero dependencies.** A single static Go binary using only the standard library.

## Install

**Windows desktop app:** download the newest `jot-<version>-windows-x64-setup.exe`
from a `jot desktop` release on the [Releases](https://github.com/ArpitK24/jot/releases) page.
It installs the app and the `jot` CLI for your user, adds `jot` to PATH and
connects your AI tools. The build is unsigned, so SmartScreen may ask you to
click *More info*, then *Run anyway*. From 0.3.1 the app updates itself: it
checks GitHub when it opens and installs a new version in one click, after
checking its SHA-256. Light, dark or system appearance is in Settings.

**CLI only:** download a binary for macOS, Linux or Windows from
[Releases](https://github.com/ArpitK24/jot/releases), or build with Go 1.23+:

```sh
go install github.com/ArpitK24/jot/cmd/jot@latest
```

It's also listed on the [official MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.ArpitK24/jot`, with one-click `.mcpb` bundles on every release.

Then create your memory store:

```sh
jot init
```

Then connect your agents. `jot setup` finds Claude Code, Cursor, Codex,
Antigravity and Claude Desktop, shows the exact lines it will add to each
config, and asks once before writing:

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

You never have to tell an agent to "check jot". jot's MCP server tells each
agent to recall before a task and remember lasting preferences. Not every tool
passes that on to its model, so `jot setup` also adds a short marked block to
the tool's own instructions file, which the agent reads at the start of every
session: `~/.claude/CLAUDE.md` (Claude Code), `~/.codex/AGENTS.md` (Codex) and
`~/.gemini/GEMINI.md` (Antigravity). Cursor and Claude Desktop rely on the
server's instructions and tool descriptions.

Undo both with `jot setup --undo`. To configure a tool by hand, see
[docs/setup.md](docs/setup.md).

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
across project memories and then global ones.

### MCP tools

| Tool       | What it does                                         |
|------------|------------------------------------------------------|
| `remember` | Save one preference, decision, fact or note. If a similar memory exists, it saves nothing and points to that one instead |
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

## Roadmap

- [x] Merge near-duplicate memories instead of piling them up
- [x] `jot setup` to connect installed agents in one step
- [x] Desktop app for Windows (macOS and Linux next)
- [ ] Expiry and review for stale facts
- [x] Optional local embeddings (Ollama) for semantic recall
- [ ] `jot sync` through a git remote across machines
- [ ] TUI browser

## License

MIT
