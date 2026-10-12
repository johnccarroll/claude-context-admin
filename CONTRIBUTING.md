# Contributing

Issues and pull requests are welcome, under the [code of conduct](CODE_OF_CONDUCT.md). For a vulnerability, use the private report in
[SECURITY.md](SECURITY.md), not an issue.

## Build and test

Needs Go 1.26 and Bun.

```bash
make build    # web UI, then the cca binary
make check    # gofmt, go vet, go test -race, tsc
make dev      # live dev server on a made-up demo home
```

## Rules a change must keep

- **No network.** cca makes no outbound connections and listens only on 127.0.0.1. Two tests
  enforce it: `cmd/cca/netpolicy_test.go` and `SMOKE_NO_NET=1 scripts/linux-smoke.sh`.
- **No secrets.** MCP env and header values are never read, logged, returned or committed.
- **Claude Code owns its config.** Plugin and MCP changes go through the `claude` CLI; nothing
  writes `~/.claude.json`.
- **Made-up data only.** Tests run against `testdata/` and demos against
  `scripts/demo-home.py`, never a real `~/.claude`. No real memories, paths or hostnames in the
  repo.
- **Pinned actions.** Workflows pin every action to a full commit SHA.

Commits follow [Conventional Commits](https://www.conventionalcommits.org). A change users will
notice gets a line in [CHANGELOG.md](CHANGELOG.md). Releases are described in
[docs/RELEASING.md](docs/RELEASING.md).
