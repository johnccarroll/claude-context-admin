#!/usr/bin/env bash
# Live dev server. Edits to web/src rebuild and reload the open page; edits to Go files rebuild
# and restart cca, and the page reloads when it's back. The sign-in link stays the same across
# restarts, and each request is logged here (paths and status only).
#
#   scripts/dev.sh          a made-up demo home, editable (safe to break)   = make dev
#   scripts/dev.sh real     your real config, read-only                     = make dev-real
#   scripts/dev.sh write    your real config, editable (undo still works)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
PORT="${PORT:-4317}"
case "${1:-demo}" in
  demo) DEMO="$(mktemp -d)/home"; python3 scripts/demo-home.py "$DEMO" >/dev/null; ARGS=(--home "$DEMO") ;;
  real) ARGS=(--read-only) ;;
  write) ARGS=() ;;
  *) echo "usage: scripts/dev.sh [demo|real|write]" >&2; exit 2 ;;
esac
export CCA_WEB_DIR="$ROOT/web/dist" CCA_DEV_TOKEN
CCA_DEV_TOKEN="$(openssl rand -hex 24)"
BIN="$(mktemp -d)/cca"

(cd web && bun install --frozen-lockfile >/dev/null && bun run build >/dev/null)
# Unminified with inline source maps, so the browser's dev tools show the TypeScript.
(cd web && exec bun build src/main.ts --outdir dist --watch --sourcemap=inline --entry-naming='[name].[ext]') &
WEB=$!
PID=""
stop() { [ -n "$PID" ] && kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null || true; }
trap 'stop; kill "$WEB" 2>/dev/null || true' EXIT
start() {
  if go build -o "$BIN" ./cmd/cca; then
    "$BIN" --no-open --port "$PORT" "${ARGS[@]}" &
    PID=$!
  else
    echo "dev: Go build failed; fix it and save again" >&2
  fi
}

STAMP="$(mktemp)"
start
URL="http://127.0.0.1:$PORT/?t=$CCA_DEV_TOKEN"
echo "dev: $URL"
open "$URL" 2>/dev/null || xdg-open "$URL" 2>/dev/null || true
while sleep 1; do # Go changes: rebuild and restart
  if [ -n "$(find cmd internal web/embed.go -name '*.go' -newer "$STAMP" -print -quit)" ]; then
    touch "$STAMP"
    echo "dev: Go changed, restarting"
    stop
    start
  fi
done
