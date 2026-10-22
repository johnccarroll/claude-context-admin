# Claude Context Admin

**Take control of your Claude config. Less bloat. Fewer wasted tokens.**

[![Tests](https://github.com/johnccarroll/claude-context-admin/actions/workflows/test.yml/badge.svg)](https://github.com/johnccarroll/claude-context-admin/actions/workflows/test.yml)
[![CodeQL](https://github.com/johnccarroll/claude-context-admin/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/johnccarroll/claude-context-admin/actions/workflows/github-code-scanning/codeql)
[![Canary](https://github.com/johnccarroll/claude-context-admin/actions/workflows/canary.yml/badge.svg)](https://github.com/johnccarroll/claude-context-admin/actions/workflows/canary.yml)

![Every memory across your projects as a 3D map: glowing clusters, named, turning; a hand swings it around and a hover lights up one memory's links](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/clips/map.webp)

Newer models need less instruction, but most Claude Code setups keep growing. Rules and agent
instructions run to hundreds of lines, memories pile up details that went stale weeks ago, and all
of it loads into every session. You pay for it in tokens every time, and past a point the extra
instruction makes results worse, not better.

Claude Context Admin puts everything Claude Code loads in one local app. Nothing changes until you
approve it, and every version is kept, whether you or Claude made the change.

```bash
brew install --cask johnccarroll/tap/claude-context-admin   # macOS app
npm install -g claude-context-admin                         # macOS or Linux, in the browser
```

[Install](#install) · [Use](#use) · [Safety and privacy](#safety-and-privacy) · [Security](#security)

## What it does

### See how your memories connect

Every memory and its links, across every project, as a map you can turn. Hover to light up what a
memory links to; click to open it, and follow its links from memory to memory. Orphans and
near-duplicates stand out. The flat 2D map is one tap away.

![Opening a memory from the 3D map: the camera flies in, the editor opens beside it, each link followed flies to the next memory, then the same map in flat 2D and back](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/clips/navigate.webp)

### Edit memories like notes

Title, kind, the one-line summary Claude reads every session, and the details. Type `[[` to link
another memory; links read as their names, and a broken one is flagged with a one-click fix.
Renames keep links and MEMORY.md in sync, and every version is kept with a diff and one-click
restore.

![Typing [[ in a memory lists the memories it can link to; the picked one reads as its name](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/clips/editor.webp)

### See what Claude loads, and what it costs

Every project's startup cost at a glance, then each source within it: your instructions, the
project's CLAUDE.md and AGENTS.md, MEMORY.md, plugins, skills and MCP servers. Open any row to see
the files behind it.

![What loads here: tokens per project, then one project's sources and the files behind them](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/clips/loads.webp)

### A review queue for upkeep

Broken links, copies across projects, memories Claude never opens, unused MCP servers and
plugins, hooks pointing at missing scripts, a MEMORY.md near its limit, memories stranded when a
repo was renamed, and instructions written for older models. Each card shows the evidence and the
exact change; nothing happens until you accept it, and Activity can undo it.

![Review: a link to a note that was never written, with its evidence, fixed in one click](https://raw.githubusercontent.com/johnccarroll/claude-context-admin/main/docs/clips/review.webp)

### How it differs from `/context` and `/doctor`

Claude Code's `/context` shows how full the context window is in your current session, and
`/doctor` checks that your installation is healthy. Both are useful, and both work one session at
a time in the terminal. Claude Context Admin covers every project at once, lets you edit what it
finds, keeps a history of every change, and waits for your approval before anything is written.

`cca doctor` is something else again. It checks that cca still understands the version of Claude
Code you have installed.

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
Without admin rights the app goes to `~/Applications` instead, so no password is needed.

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
- Every change is scanned by CodeQL and by Go's vulnerability checker (again daily), and a test
  fails the build if the code gains any network use beyond 127.0.0.1. Releases are signed,
  notarized, carry build provenance (`gh attestation verify`) and come with an SBOM
  (`claude-context-admin-<version>.cdx.json` on each release).
- When a Claude Code update changes something cca relies on, the app says which feature is
  affected instead of showing wrong numbers.

---

## Security

Report vulnerabilities privately through GitHub: **Security → Report a vulnerability** on this
repository. See [SECURITY.md](SECURITY.md).

To check a download came from this repository's release workflow, unchanged:

```bash
gh attestation verify ClaudeContextAdmin-<version>-macos.zip --repo johnccarroll/claude-context-admin
```

[SECURITY.md](SECURITY.md) lists what each release ships with (SBOM, attestations) and what is
checked on every change. [CHANGELOG.md](CHANGELOG.md) has the history, and
[CONTRIBUTING.md](CONTRIBUTING.md) the rules a change must keep.

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
