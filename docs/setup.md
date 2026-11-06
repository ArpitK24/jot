# Connecting agents to jot

Every agent runs the same command: `jot serve`. It speaks MCP over stdio.
Set `JOT_SOURCE` so each memory records which agent wrote it.

The quickest way is `jot setup`, which does all of the below for every tool
it finds (`--dry-run` to preview, `--undo` to remove). The manual steps are
here if you'd rather do it yourself, or use a tool it doesn't know yet.

Desktop apps on macOS don't see your shell's PATH, so use the full path from
`which jot` as the command. `jot setup` does this for you.

## Claude Code

```sh
claude mcp add jot --scope user -e JOT_SOURCE=claude-code -- jot serve
```

## Cursor

`~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "jot": { "command": "jot", "args": ["serve"], "env": { "JOT_SOURCE": "cursor" } }
  }
}
```

## Codex CLI

`~/.codex/config.toml`:

```toml
[mcp_servers.jot]
command = "jot"
args = ["serve"]
env = { JOT_SOURCE = "codex" }
```

## Any other MCP client

Command `jot`, args `["serve"]`, transport stdio.

## Nudge the agent to use it

MCP clients pass jot's server instructions to the model, but a line in your
agent rules file (`CLAUDE.md`, `.cursorrules`, `AGENTS.md`) makes it reliable:

```
Before starting a task, call jot's `recall` with what you're about to do.
When I state a preference, make a decision, or correct you, save it with `remember`.
```

## Project memory

Run `jot init --project` at a repo root to create `.jot/`. Agents started
inside that repo read project memories first, then global ones, and can save
to either with `scope: "project"`. Commit `.jot/` to share it with your team,
or add it to `.gitignore` to keep it personal.
