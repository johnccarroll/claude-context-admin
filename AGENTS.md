# Claude Context Admin (cca)

Naming: **Claude Context Admin** everywhere (app, docs, npm and Homebrew `claude-context-admin`, command `cca`),
except the Claude Code plugin and its marketplace, which must be named `context-admin`: Claude Code's
plugin validator rejects third-party names starting with `claude-`.

An open-source (MIT, © John Carroll) local app, MCP server and Claude Code plugin for managing
everything Claude Code loads: memories, instructions, plugins, MCP servers, skills, agents and hooks.
**The repo is public:** never commit secrets, real memories, personal paths or hostnames.
Fixtures, demos and screenshots use made-up data only (`scripts/demo-home.py`).

- Go 1.26 single binary (`cmd/cca`). The web UI lives in `web/` (TypeScript, bundled with Bun) and is embedded with `go:embed`.
- Plugin and MCP config changes go **only** through the `claude plugin` / `claude mcp` CLI.
  Claude Code rewrites `~/.claude.json` constantly, so never write to it directly.
- Never read, log, return or commit MCP env or header values, or other secrets.
- Tests are `go test ./...` against fixtures in `testdata/`. Never test against the real `~/.claude`.
