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

Download a binary for macOS, Linux or Windows from
[Releases](https://github.com/ArpitK24/jot/releases), or build with Go 1.23+:

```sh
go install github.com/ArpitK24/jot/cmd/jot@latest
```

Then create your memory store:

```sh
jot init
```

Then connect your agents with [docs/setup.md](docs/setup.md). For Claude Code:

```sh
claude mcp add jot --scope user -e JOT_SOURCE=claude-code -- jot serve
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
across project memories and then global ones.

### MCP tools

| Tool       | What it does                                         |
|------------|------------------------------------------------------|
| `remember` | Save one preference, decision, fact or note          |
| `recall`   | Search memory for what's relevant to the task        |
| `update`   | Rewrite a memory that changed                        |
| `forget`   | Delete a memory                                      |
| `list`     | Newest memories, filtered by kind or tags            |

### CLI

```
jot init [--project]     jot search <query>     jot show <id>     jot log
jot add [-k -t -p] text  jot ls [-k -t]         jot edit <id>     jot where
jot serve                jot rm <id>            jot version
```

## Roadmap

- [ ] Merge near-duplicate memories instead of piling them up
- [ ] Expiry and review for stale facts
- [ ] Optional local embeddings (Ollama) for semantic recall
- [ ] `jot sync` through a git remote across machines
- [ ] TUI browser

## License

MIT
