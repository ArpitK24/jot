# Polish pass: first-run experience and memory quality

Branch `polish`, draft PR #1 (https://github.com/ArpitK24/jot/pull/1). Nothing
tagged, released or merged.

## What changed

1. **`jot doctor`** (`cmd/jot/doctor.go`, `internal/setup/inspect.go`).
   Store and git; for each installed tool, the MCP entry (read with the new
   `setup.Inspect`, JSON or TOML), that its command exists, whether it points at
   a different jot binary, and the instructions-file block (`setup.RulesCurrent`);
   a live `jot serve` handshake (initialize + tools/list, 10 s timeout) for each
   distinct configured command; semantic search state. Prints ✓ / ✗ / ! / -
   lines, a `fix:` line with the exact command for each problem, and exits 1 when
   anything fails. `--no-serve` skips the handshake.
2. **Memory quality** (`internal/mcp/quality.go`, `server.go`). `remember` and
   `update` refuse empty text, more than 600 characters, and text that looks like
   a credential (private keys, AWS/GitHub/Slack/Google/`sk-`-style keys, JWTs,
   `password: <value with a digit or symbol>`, `api_key=...`, URLs with a
   password). `force` does not bypass these. A memory with 3+ bullet points is
   saved with a tip to split it. Server instructions and tool descriptions now
   say what to save, what not to, how to write it, and which scope to use.
   `list` limit can go below 20 (it was `max(limit, 20)`).
3. **Project scope.** recall already searched project + global. Now: results say
   "from project memory" / "from global memory"; `jot serve` re-resolves stores on
   every call (a `.jot` created mid-session is used at once); if the client
   declares MCP `roots`, jot asks for them and uses the first root with a `.jot`.
   Scope `project` with no project store explains both ways forward.
4. **Installers** (`scripts/install.sh`, `scripts/install.ps1`). Newest `v*` tag
   from the releases list (not `/releases/latest`, because `desktop/v*` releases
   share the repo), download archive + `checksums.txt`, refuse on SHA-256
   mismatch, install to `~/.local/bin` (root: `/usr/local/bin`) or
   `%LOCALAPPDATA%\jot\bin` (+ user PATH). `JOT_VERSION`, `JOT_INSTALL_DIR`.
5. **README** top rewritten: pitch, 3-step quickstart with the installers, Claude
   Code -> Cursor example, FAQ. Unreleased features marked "next release".
6. **First run.** `jot` with no args and no store prints where memories will live
   and to run `jot setup` / `jot doctor`. `jot setup` now creates the global store,
   so `jot init` is optional; doctor treats a missing store as a note.

## Tested

- `go vet ./...`, `go test ./...` before every commit; desktop module vet + test.
- CI (ubuntu, macOS, Windows) green on the PR. The doctor and roots tests run on
  Windows there, so the Go side is covered on Windows; the installer is not.
- `install.sh` against the real v0.3.0 and v0.2.0 releases (linux/arm64), plus a
  bad-version run; `go test ./scripts` runs it against a fake releases API
  (desktop tag first, checksum mismatch refused).
- `install.ps1` under PowerShell 7.4 on Linux against the real v0.3.0 windows
  zips (amd64 and arm64) and a bad version.
- `jot doctor` by hand on a fake home, fresh and after `jot setup`.

## Not tested

- Real Windows: install.ps1 PATH update, PowerShell 5.1, doctor's ✓/✗ glyphs in
  an old console code page.
- Real Claude Code / Cursor / Codex sessions: whether they send MCP roots, whether
  agents follow the new instructions, false positives of the secret check in
  real use.
- macOS installer run (CI runs the fake-API test on macOS).

## Decisions for Arpit

- Installer URLs point at `main/scripts/...`, so they only work after merge. They
  work with v0.3.0, so README lists them; merge = advertising them.
- The README already described the instructions-file block, which is in main but
  not in v0.3.0. Cut a release (v0.4.0?) so README matches a release.
- `RulesBlock` text left unchanged on purpose: changing it marks every existing
  block outdated and doctor would flag all users. Worth aligning with the new
  server instructions in a release that also bumps it.
- Secret refusal has no override. If false positives show up, consider letting
  the CLI `jot add` warn instead (it currently doesn't check at all).
- Doctor counts "no tool connected" as a failure but "no known tool installed"
  as a note.
