#!/usr/bin/env bash
# Linux smoke test (CI): cca serves a demo home, a memory deleted through the API lands in the
# freedesktop.org Trash with its .trashinfo, and undo puts it back. Needs a built ./cca.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export HOME
HOME="$(python3 "$ROOT/scripts/demo-home.py" "$(mktemp -d)/home")" # the "real" home for cca
export XDG_DATA_HOME="$HOME/.local/share"
LOG="$(mktemp)"
"$ROOT/cca" --no-open --port 0 >"$LOG" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true' EXIT
for _ in $(seq 50); do grep -q http "$LOG" && break; sleep 0.2; done
URL="$(grep -o 'http://[^ ]*' "$LOG")"
BASE="${URL%%/?t=*}"
JAR="$(mktemp)"
curl -fsS -c "$JAR" -o /dev/null "$URL"
api() { curl -fsS -b "$JAR" -H 'Content-Type: application/json' "$@"; }

MEM="$(find "$HOME/.claude/projects" -path '*/memory/feedback_small_prs.md' | head -1)"
ACT="$(api -d "{\"op\":\"memory-trash\",\"args\":{\"path\":\"$MEM\"}}" "$BASE/api/act")"
test ! -e "$MEM"
test -f "$XDG_DATA_HOME/Trash/files/feedback_small_prs.md"
grep -q '^Path=/' "$XDG_DATA_HOME/Trash/info/feedback_small_prs.md.trashinfo"
ID="$(printf '%s' "$ACT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["activity"])')"
api -d "{\"id\":\"$ID\"}" "$BASE/api/undo" >/dev/null
test -f "$MEM"
test ! -e "$XDG_DATA_HOME/Trash/info/feedback_small_prs.md.trashinfo"
echo "linux smoke: trash and undo OK"
