# Claude Context Admin

**See what Claude remembers. Keep it true.**

Claude Code loads a lot before you type: memories, CLAUDE.md and AGENTS.md, rules, skills,
plugins, MCP servers and hooks, spread across your home folder and every repo. Claude Context
Admin puts all of it in one local app. It shows what loads in each project and what it costs,
finds what's broken, stale or duplicated, and fixes it, with undo for every change.

![A tour: open a memory, follow its links, fix a broken one, then the map, what loads and Review](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/tour.gif)

## What it does

### See what Claude loads, and what it costs

Pick a project and every page shows what Claude loads there: memories, CLAUDE.md and AGENTS.md,
rules, skills, plugins, MCP servers and hooks. **What loads here** adds up the tokens each source
spends before your first message, and how close MEMORY.md is to its load limit. Click any row to
open the files behind it.

![What loads at the start of a session in one project, by source](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/screenshots/what-loads.png)

### Edit memories like notes

Title, kind, the one-line summary Claude reads every session, and the details. Links to other
memories read as their names; a broken one is flagged with a one-click fix, and typing `[[` offers
the memories you can link to. Renames keep links and MEMORY.md in sync, and every version is kept,
whether you or Claude changed it, with a diff and one-click restore. Follow links or the graph
from memory to memory, and step back with the arrows.

![A memory open in the editor, with its links, connections and history](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/screenshots/memory-editor.png)

### A review queue for upkeep

Broken links, copies across projects, memories Claude never opens, unused MCP servers and
plugins, hooks pointing at missing scripts, a MEMORY.md near its limit, memories stranded when a
repo was renamed, and instructions written for older models (stacked ALL-CAPS rules, "think step
by step", retired model names). Each card shows the evidence and the exact change; nothing happens
until you accept it, and Activity can undo it.

![Review: what to fix, with the evidence and the exact change](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/screenshots/review.png)

### See how it connects

The map draws every memory and its links across projects, so orphans and near-duplicates stand
out. Hover to preview, click to open.

![Map of how memories connect across projects](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/screenshots/map.png)

### And

- **Add MCP servers, plugins and skills by pasting** the JSON or command an install guide gives
  you. cca shows exactly what will run, asks where it applies, and has Claude Code add it. Keys
  become `${ENV_VAR}` references, so a key never sits in a config file or on a command line.
- **Claude helps, you decide.** The optional plugin gives Claude read access and lets it *suggest*
  changes, which appear in Review with the exact text they would write.
- **Bulk edits, search, undo.** Select many memories to move, retype or trash them. ⌘K finds
  anything. Activity lists every change, by you or Claude, with Undo.
- **Light and dark**, a resizable editor, and back/forward everywhere.

## Install

**macOS (recommended): the app**

```bash
brew install --cask johnccarroll/tap/claude-context-admin
```

A native Mac app, signed with a Developer ID and notarized by Apple. It also puts the `cca`
command on your PATH. Closing the window keeps it running in the menu bar, so it can record
memory changes Claude makes; Quit stops it. macOS 13 or later, Apple Silicon or Intel.

**macOS or Linux (including WSL): in the browser**

```bash
npm install -g claude-context-admin
```

The same app, opened in your browser instead of its own window. Native Windows is planned.
Built for Claude Code 2.1.277 or later; `cca doctor` checks that the parts it relies on still
work.

## Use

```bash
cca              # open the app (on macOS with the app installed; otherwise in your browser)
cca --browser    # open it in your browser even where the app is installed
cca --read-only  # look around without changing anything (in the browser)
cca audit        # problems and counts, in the terminal (--json for scripts)
cca doctor       # check cca still understands your Claude Code version
```

### Let Claude help (optional)

```bash
claude plugin marketplace add johnccarroll/claude-context-admin
claude plugin install context-admin@context-admin
```

Then ask Claude to review your memories, or to remember something: the plugin's `memory-review`
and `remember` skills handle it. The plugin adds a small MCP
server and a hook that tells Claude when a memory it just wrote is malformed. It never writes
files itself.

## Safety and privacy

- Runs on your computer and makes no internet connections of its own. The engine listens only
  on 127.0.0.1, behind a sign-in token that changes every run; the Mac app loads nothing else.
- Every change keeps the previous version and can be undone from Activity, unless the file
  changed again since (then restore from its history instead). Deleted files go to the Trash.
- It won't write while Claude Code is consolidating memories, or over a file that changed
  since you opened it.
- Plugin and MCP changes go through Claude Code's own `claude` command, which also does any
  downloading when you install a plugin. MCP keys and other secret values are never read or
  shown, and a plugin that installs by running a command shows you that command first.
- When a Claude Code update changes something cca relies on, the app says which feature is
  affected instead of showing wrong numbers.

---

## Security

Report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on this
repository. See [SECURITY.md](SECURITY.md).

## Build from source

Needs Go 1.26 and Bun.

```bash
make build    # web UI, then the cca binary
make check    # gofmt, go vet, go test -race, tsc
make dev      # live dev server on a made-up demo home: UI edits reload, Go edits restart
make dev-real # the same on your real config, read-only (scripts/dev.sh write: editable)
```

## License

[MIT](LICENSE) © 2026 John Carroll. Release builds list their open-source components in
`THIRD_PARTY_NOTICES.txt`.

Claude Context Admin is an independent project and is not affiliated with, endorsed by or
sponsored by Anthropic. "Claude" and "Claude Code" are trademarks of Anthropic, PBC.
Screenshots use made-up data.
