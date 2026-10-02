#!/usr/bin/env bash
# Takes the README screenshots from the real Mac app window, attached (CCA_URL) to an engine
# serving the made-up demo home. Nothing from your own setup is shown.
#
#   scripts/build-mac-app.sh 0.0.0-dev && scripts/screenshots.sh
#
# macOS only. The screen must be unlocked, and the terminal needs Screen Recording and
# Accessibility permission (to capture and size the window).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/dist/mac/Claude Context Admin.app/Contents/MacOS/Claude Context Admin"
OUT="$ROOT/docs/screenshots"
ID=dev.johncarroll.claude-context-admin
TMP="$(mktemp -d)"
trap 'osascript -e "quit app \"Claude Context Admin\"" >/dev/null 2>&1 || true; kill "${ENGINE:-}" 2>/dev/null || true; defaults delete "$ID" NSRequiresAquaSystemAppearance 2>/dev/null || true; rm -rf "$TMP"' EXIT
[ -x "$APP" ] || { echo "build the app first: scripts/build-mac-app.sh 0.0.0-dev" >&2; exit 1; }

# The app's window id, for screencapture -l.
cat > "$TMP/wid.swift" <<'SWIFT'
import CoreGraphics
let l = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as! [[String: Any]]
for w in l where (w["kCGWindowOwnerName"] as? String) == "Claude Context Admin" && (w["kCGWindowLayer"] as? Int) == 0 {
  print(w["kCGWindowNumber"]!)
}
SWIFT
xcrun swiftc -O "$TMP/wid.swift" -o "$TMP/wid" 2>/dev/null

HOME_DIR=/tmp/cca-demo
python3 "$ROOT/scripts/demo-home.py" "$HOME_DIR" >/dev/null
(cd "$ROOT" && go build -o "$TMP/cca" ./cmd/cca)
"$TMP/cca" --home "$HOME_DIR" --no-open --port 0 > "$TMP/log" 2>&1 &
ENGINE=$!
for _ in $(seq 50); do grep -q "http://" "$TMP/log" && break; sleep 0.1; done
URL="$(grep -o 'http://[^ ]*' "$TMP/log")"

enc() { python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$1"; }
REAL="$(python3 -c "import os;print(os.path.realpath('$HOME_DIR'))")"
ST="$REAL/code/storefront"
MEM="$(ls "$REAL"/.claude/projects/*storefront/memory/project_launch_q4.md)"

# shot <file> <page hash> [light]
shot() {
  osascript -e 'quit app "Claude Context Admin"' >/dev/null 2>&1 || true
  while pgrep -f "Claude Context Admin.app/Contents/MacOS" >/dev/null; do sleep 0.3; done
  if [ "${3:-}" = light ]; then defaults write "$ID" NSRequiresAquaSystemAppearance -bool YES
  else defaults delete "$ID" NSRequiresAquaSystemAppearance 2>/dev/null || true; fi
  CCA_URL="$URL#$2" "$APP" >/dev/null 2>&1 &
  sleep 5
  osascript -e 'tell application "Claude Context Admin" to activate' \
    -e 'tell application "System Events" to tell process "Claude Context Admin" to set size of window 1 to {1440, 1000}' >/dev/null 2>&1 || true
  sleep 2 # the map settles after a resize
  screencapture -x -o -l "$("$TMP/wid" | sort -n | tail -1)" "$OUT/$1.png"
  echo "$1"
}
shot memories "memories?p=$(enc "$ST")"
shot memory-editor "memories?p=$(enc "$ST")&m=$(enc "$MEM")"
shot what-loads "what?p=$(enc "$ST")"
shot review review
shot map map
shot plugins plugins
shot hooks hooks
shot activity activity
shot skills-light skills light
