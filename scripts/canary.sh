#!/usr/bin/env bash
# Daily canary: does cca still understand the latest Claude Code? Builds a made-up home
# (scripts/demo-home.py), installs the context-admin plugin into it with the real `claude` CLI,
# and runs `cca doctor`, which exits 1 when a check fails.
#
# It covers the CLI and plugin JSON (the most volatile couplings in docs/COMPAT.md). Its
# transcripts are made up, so a change to Claude Code's real transcript format shows up only in
# `cca doctor` on a real machine.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
H="$(python3 "$ROOT/scripts/demo-home.py" "$WORK/home")"

(cd "$ROOT/web" && bun install --frozen-lockfile >/dev/null && bun run build >/dev/null)
(cd "$ROOT" && go build -o "$WORK/cca" ./cmd/cca)
claude --version
HOME="$H" claude plugin marketplace add "$ROOT"
HOME="$H" claude plugin install context-admin@context-admin
# The commands "Add to Claude" uses for MCP servers
# shellcheck disable=SC2016 # ${K} stays literal: Claude Code stores the reference, not a value
HOME="$H" claude mcp add-json canary '{"type":"stdio","command":"true","env":{"K":"${K}"}}' --scope user
HOME="$H" claude mcp remove canary --scope user
"$WORK/cca" doctor --home "$H"
