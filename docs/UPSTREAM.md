# Feature requests for Claude Code (drafts)

Each one would let cca drop a fragile dependency listed in `docs/COMPAT.md`. These are drafts to
file at github.com/anthropics/claude-code/issues; none have been filed yet.

## 1. `claude plugin details --json`

`claude plugin details <id>` prints a useful component inventory and projected token cost, but only
as text. Tools that show context cost per plugin have to parse that text, which breaks whenever its
layout changes. Please add `--json` with the same fields: components with counts and names,
always-on tokens, and per-component always-on and on-invoke tokens. `plugin list`,
`install`, `enable`, `disable` and `uninstall` already have `--json` (2.1.268).

## 2. `claude mcp list --json` and `claude mcp get --json`

Today the only machine-readable source for configured MCP servers is reading `~/.claude.json`
and `.mcp.json` directly, which mixes MCP config with unrelated, frequently rewritten state. A
`--json` mode on `mcp list` and `mcp get` would give name, scope, transport, enabled or disabled
for this project, approval state and env/header key names (never values).

## 3. A supported way to turn an MCP server off per project

`disabledMcpServers` in `~/.claude.json` is honoured, and `/mcp` can toggle it in a session,
but there's no CLI command. A `claude mcp disable <name>` / `enable <name>` that respects scopes
would let tools offer an off switch instead of only "remove".

## 4. A documented memory format and lock

Auto-memory's header moved `type` under `metadata` and gained `modified` without a changelog
entry, and `.consolidate-lock` (memory consolidation) is undocumented. A short spec would let
tools that edit memories stay compatible: the header fields, which shapes are read, and how
other writers should detect that consolidation is running.

## 5. A usage signal that isn't the transcript format

Telling which memories, skills and MCP servers are actually used means parsing session
transcripts, an internal format. Either a documented, stable subset of the transcript (tool name,
input path, timestamp), or a hook such as `InstructionsLoaded` extended to memory reads, would make
this robust.
