#!/usr/bin/env bash
# Linux smoke test (CI): cca serves a demo home, a memory deleted through the API lands in the
# freedesktop.org Trash with its .trashinfo, and undo puts it back. Needs a built ./cca.
set -euo pipefail

# SMOKE_NO_NET=1 (CI; needs sudo) runs all of this again in a network namespace that has only
# loopback, as the same user: the real binary, dependencies included, must work with no network,
# listen only on 127.0.0.1 and never try to reach anything else.
if [ "${SMOKE_NO_NET:-}" = 1 ]; then
  # shellcheck disable=SC2016 # expanded by the inner sh
  exec sudo unshare -n -- sh -c 'ip link set lo up && exec sudo -u "#$1" env PATH="$2" SMOKE_NO_NET=inside "$3"' \
    sh "$(id -u)" "$PATH" "$0"
fi
# The namespace's own counters: a packet with no route out (any non-loopback address) and a UDP
# packet nobody listens for (a DNS query to the local stub resolver).
attempts() {
  awk '$1 == "Ip:" || $1 == "Udp:" {
    if (!($1 in seen)) { for (i = 2; i <= NF; i++) name[$1, i] = $i; seen[$1] = 1; next }
    for (i = 2; i <= NF; i++) if (name[$1, i] == "OutNoRoutes" || name[$1, i] == "NoPorts") printf "%s%s=%s ", $1, name[$1, i], $i
  }' /proc/net/snmp
  awk '$1 == "Ip6OutNoRoutes" || $1 == "Udp6NoPorts" { printf "%s=%s ", $1, $2 }' /proc/net/snmp6
}
[ "${SMOKE_NO_NET:-}" != inside ] || BEFORE="$(attempts)"

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

if [ "${SMOKE_NO_NET:-}" = inside ]; then
  [ "$(ip -o link | wc -l)" = 1 ] || { echo "the namespace has more than loopback" >&2; exit 1; }
  LISTEN="$(ss -Hlntu)"
  [ -n "$LISTEN" ] || { echo "cca is not listening" >&2; exit 1; }
  if grep -v ' 127\.0\.0\.1:[0-9]* ' <<<"$LISTEN"; then echo "a listener is not on 127.0.0.1" >&2; exit 1; fi
  AFTER="$(attempts)"
  [ "$AFTER" = "$BEFORE" ] || { echo "cca tried to reach the network: $BEFORE -> $AFTER" >&2; exit 1; }
  # The check must be able to fail: an outside address has to move the counters.
  curl -s -m 2 -o /dev/null http://192.0.2.1/ || true
  [ "$(attempts)" != "$AFTER" ] || { echo "the network counters did not notice an outside connection" >&2; exit 1; }
  echo "linux smoke: no network needed or tried; listening on 127.0.0.1 only"
fi
