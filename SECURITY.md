# Security

Please report vulnerabilities privately with GitHub's **Report a vulnerability** button
(Security tab of this repository), not in a public issue. Include what you found, how to
reproduce it, and the `cca version` you used.

## What cca promises

- **Local only.** The app binds to 127.0.0.1, signs in with a link whose token changes every run, and rejects other
  hosts, cross-origin requests and non-JSON writes. cca makes no internet connections of its
  own; installing a plugin is Claude Code's own download.
- **Claude Code does the config writes.** MCP and plugin changes go through the `claude` CLI
  with argument lists, never a shell; cca never writes `~/.claude.json`.
- **Secrets stay out.** MCP key and header values are never read into the app, shown, logged or
  sent to Claude, and neither are credentials in MCP URLs or hook command lines. When adding a
  server, keys must be `${ENV_VAR}` references, in every scope. Saved versions of
  `settings.json` leave out its `env` block.
- **Every change can be undone.** The previous version is kept; deleted files go to the Trash.
  Undo refuses when the file changed again since, so it never erases a later edit.
- **Claude only suggests.** Changes Claude proposes wait for your approval, shown in full. It
  can't propose adding MCP servers or plugins, or moving a project's skill into every project,
  and it reads only Markdown that Claude Code loads (never a file a `.md` symlink points to).

## What each release ships with

- **An SBOM.** `claude-context-admin-<version>.cdx.json` on the GitHub release lists everything
  inside cca at the versions that were built: the Go modules and Go version read from the
  binaries, and the web UI's packages as the lockfile resolved them (CycloneDX 1.6).
- **Attestations.** The downloads carry signed build provenance and an SBOM attestation:
  `gh attestation verify <file> --repo johnccarroll/claude-context-admin`, adding
  `--predicate-type https://cyclonedx.org/bom` for the SBOM.
- **Checks on every change.** CodeQL, `govulncheck` and `bun audit` (again daily), and tests
  that fail on any network use beyond 127.0.0.1, in the source and in the running binary.
