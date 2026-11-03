# Connecting agents to jot

Every agent runs the same command: `jot serve`. It speaks MCP over stdio.
Set `JOT_SOURCE` so each memory records which agent wrote it.

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
