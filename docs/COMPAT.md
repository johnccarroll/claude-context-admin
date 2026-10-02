# Where cca depends on Claude Code

Every coupling point is listed here, with where it lives in our code and what happens if Claude Code changes it.
- `cca doctor` checks the rows marked **canary** against the live machine and exits 1 when one breaks.
- When a Claude Code update breaks something, fix the one file named in its row.

Last verified against Claude Code **2.1.287** (2026-10-01).

## Documented: stable, low risk

| What | Where we read or write it | If it changes |
|---|---|---|
| Auto memory: `~/.claude/projects/<dir>/memory/*.md` + `MEMORY.md` | `scan/memory.go`, `write/memory.go` | Memories disappear from the list. The doctor reports **Memory files** (canary). |
| Memory header: `name`, `description`, and `type` either top-level or under `metadata` | `scan/markdown.go`, `write/memory.go` | Headers count as unreadable. The doctor fails above 20% (canary). Both shapes are read, each file keeps its own shape, and unknown keys (such as `modified`, added in 2.1.214) are kept. **cca never migrates the shape**, because it changed with no changelog entry. |
| `MEMORY.md` load limit: about 200 lines / 25 KB | `scan.IndexMaxLines`, `scan.IndexMaxBytes` | Shown as "about". The limit changed in 2.1.83 and 2.1.211 (it now ignores frontmatter and comments), so the meter is approximate by design. |
| CLAUDE.md / CLAUDE.local.md / AGENTS.md, `.claude/rules`, `@imports` (max depth 4) | `scan/instructions.go`, `loads/loads.go` | The budget is wrong. Re-read the memory docs. |
| `instructionFiles` setting (2.1.277+): `claude-md`, `claude-md-or-agents-md` (default: a project with no CLAUDE.md of its own gets its AGENTS.md), `claude-md-and-agents-md`, `managed-only`. The old key `projectInstructions` (`none`/`claude`/`agents-fallback`/`both`) is still honoured | `scan.InstructionMode`, `loads.Compute`, `audit.agentsMD` | **Medium churn**: renamed within weeks of shipping. Unknown values fall back to the default. Read only; cca never writes this setting. |
| Skills `skills/*/SKILL.md`, commands `commands/**/*.md`, agents `agents/*.md` | `scan/toolkit.go` | Items disappear. A count of zero against known files means look here. |
| Hooks in the `hooks` key of each settings file | `scan/toolkit.go`, `write/config.go` | Hooks disappear. Unknown settings keys are kept on write. |
| Precedence: MCP local > project > user > plugin; personal skills beat project ones | `loads.Shadows` | "Overridden" is wrong. Check [MCP](https://code.claude.com/docs/en/mcp) and [skills](https://code.claude.com/docs/en/skills). |
| Session index: `projects/<dir>/sessions-index.json` `originalPath` | `scan/scan.go` (`resolveDir`, only when no transcripts are left) | A pruned project shows under its raw folder name and the moved-folder repair can't find it. Nothing else depends on it. |

## Official CLI: stable, preferred for every change

| Command | Used by | If it changes |
|---|---|---|
| `claude plugin list --json` | `scan/plugins.go` | Plugins disappear. The doctor reports **Plugin list** (canary). |
| `claude plugin enable/disable/uninstall <id> --json --scope` | `server/act.go` | The action shows the CLI's error message. |
| `claude mcp remove <name> --scope` | `server/act.go` | Same as above. |
| `claude mcp add-json <name> <json> --scope` | `server/act.go` (`mcp-add`) | Adding MCP servers fails with Claude Code's message. The canary runs it daily. |
| `claude plugin marketplace add --json` and `claude plugin install --json [--accept-command <sha256>]` | `server/act.go` (`plugin-add`) | Reads the `outcome`, `message`, `pluginId` and `shownCommand.sha256` fields of the last line. A changed shape shows Claude Code's message instead of installing; nothing is ever confirmed with `-y`. |
| `claude --version` | `doctor` | Shown in the doctor (canary). |

**Missing upstream; worth a feature request:**
- `claude plugin details --json`
- `claude mcp list --json`
- a supported way to turn an MCP server off per project
- a supported way to turn a skill or agent off

## Undocumented internals: fragile, isolated, degrade to "unknown"

| What | Where | Degrades to | Canary |
|---|---|---|---|
| `claude plugin details` **text** output (token costs) | `scan.parseDetails` | Costs show as unknown | **Plugin costs** |
| Transcript JSONL: `message.content[].tool_use` with `name`/`input` | `usage.parseFile` | Usage shows as unknown | **Usage stats** |
| Transcript `cwd` field (resolving project folders) | `scan.cwds`, `scan.resolveDir` | Falls back to matching folder names | **Project folders** |
| Project folder naming: `/` and `.` become `-` | `scan.Encode` | Falls back to the transcript `cwd` | **Project folders** |
| `~/.claude.json` keys `mcpServers` and `projects[path].mcpServers` / `disabledMcpServers` | `scan/toolkit.go` (read only, never written) | MCP list goes empty; a scan warning appears | Scanner warning |
| `.consolidate-lock` (memory consolidation, "autoDream") | `write.CheckLock` | **Opaque:** writes are blocked for 1 hour after the lock is touched, matching Claude Code's own staleness window. Its contents are never read. | none (no changelog history to track) |

## Rules that keep this list short

1. **Writes go through the CLI wherever one exists.** We never write `~/.claude.json`.
2. **Read tolerantly.** Skip what isn't understood, never fail the whole scan, and keep unknown keys on write.
3. **Every internal format is read in exactly one function** (named above) and has a doctor check.
4. **Claude Code behaviour lives in named constants or one function**, with a docs link here.
5. **When the doctor fails:** fix the named function, add the new shape to that package's fixture test, and bump "Last verified" above.

## Release history (2.0.0 to 2.1.287): what changes and how often

Reviewed 2026-10-01. Churn decides how deep cca goes into each area.

| Area | Churn | What cca does |
|---|---|---|
| Hooks schema | Low; new events only, no renames | Full read and remove. Event names are treated as an open set. |
| CLAUDE.md, rules, skills and agents layout | Low to medium; changes add things | Full read and edit. Frontmatter is read tolerantly (keys appear in kebab, snake or camel case). |
| Auto memory (limits, header, cleanup) | **High**; about 13 changes in 7 months, some silent | Read and edit; the header shape is never migrated; the lock is opaque; limits are approximate. |
| Transcripts | Medium; new record types appear silently | Read only, unknown lines skipped, canary. |
| Plugins (CLI, layout, Mods in 2.1.287) | **Highest**; changes most weeks | Only the `claude plugin … --json` CLI (added in 2.1.268). Plugin internals are never read except where the CLI's `installPath` points. Canary. |
| MCP config in `~/.claude.json` | Medium; preferences are moving to settings.json (2.1.280) | Read only and tolerant; changes go through `claude mcp`. We never parse `mcp list` or `mcp get` text. |

**Deliberately not shipped:** migrating memory headers, reading the lock's contents, toggling individual skills (`skillOverrides` changed several times), reading `installed_plugins.json` or plugin cache folders directly, and parsing `claude mcp list` text.
