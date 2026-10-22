#!/usr/bin/env bash
# Records the README's clips from the real Mac app on the made-up demo home: one short scene per
# feature (docs/clips/<scene>.webp) plus a full-quality MP4 of each for sharing (dist/clips/, not
# committed). Nothing from your own setup is shown.
#
#   scripts/build-mac-app.sh 0.0.0-dev && scripts/tour.sh [scene...]
#
# Scenes are defined in scripts/tour-driver.js, which the app serves only in this recording's copy
# of the UI (the shipped UI never has it); it draws a cursor and walks the scene while screencapture
# records the window. macOS only. The screen must be unlocked, and the terminal needs Screen
# Recording and Accessibility permission. Needs cliclick, ffmpeg and img2webp (libwebp).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/dist/mac/Claude Context Admin.app/Contents/MacOS/Claude Context Admin"
TMP="$(mktemp -d)"
trap 'rm -rf /tmp/cca-tour; osascript -e "quit app \"Claude Context Admin\"" >/dev/null 2>&1 || true; kill "${ENGINE:-}" 2>/dev/null || true; rm -rf "$TMP"' EXIT
[ -x "$APP" ] || { echo "build the app first: scripts/build-mac-app.sh 0.0.0-dev" >&2; exit 1; }
for c in cliclick ffmpeg img2webp; do command -v "$c" >/dev/null || { echo "needs $c" >&2; exit 1; }; done

# scene  page  seconds  (the page it starts on, and how long its timeline runs in tour-driver.js)
SCENES="map map 13
navigate map 14
editor all 12
loads what 9.5
review review 10"
WANT=("$@")
DEMO=/tmp/cca-tour # a short home, so paths shown in the app read cleanly

# The UI, built, plus the driver.
(cd "$ROOT/web" && bun run build >/dev/null)
cp -R "$ROOT/web/dist" "$TMP/web"
cp "$ROOT/scripts/tour-driver.js" "$TMP/web/tour-driver.js"
sed -i '' 's|</body>|<script src="./tour-driver.js"></script></body>|' "$TMP/web/index.html"
grep -q tour-driver "$TMP/web/index.html" || { echo "couldn't add the driver to index.html" >&2; exit 1; }
(cd "$ROOT" && go build -o "$TMP/cca" ./cmd/cca)
mkdir -p "$ROOT/docs/clips" "$ROOT/dist/clips"

while read -r scene page secs; do
  if [ ${#WANT[@]} -gt 0 ] && [[ " ${WANT[*]} " != *" $scene "* ]]; then continue; fi
  # A fresh demo home and engine per scene, so one scene's changes (Review accepts a fix) never leak.
  kill "${ENGINE:-}" 2>/dev/null || true
  rm -rf "$DEMO"; python3 "$ROOT/scripts/demo-home.py" "$DEMO" >/dev/null
  CCA_WEB_DIR="$TMP/web" "$TMP/cca" --home "$DEMO" --no-open --port 0 > "$TMP/log" 2>&1 &
  ENGINE=$!
  for _ in $(seq 50); do grep -q "http://" "$TMP/log" && break; sleep 0.1; done
  URL="$(grep -o 'http://[^ ]*' "$TMP/log")&hue=260&tour&scene=$scene"

  # The app, 1440x900 at the top left of the main screen.
  osascript -e 'quit app "Claude Context Admin"' >/dev/null 2>&1 || true
  while pgrep -f "Claude Context Admin.app/Contents/MacOS" >/dev/null; do sleep 0.3; done
  CCA_URL="$URL#$page" "$APP" >/dev/null 2>&1 &
  sleep 5
  osascript -e 'tell application "Claude Context Admin" to activate' \
    -e 'tell application "System Events" to tell process "Claude Context Admin" to set position of window 1 to {0, 40}' \
    -e 'tell application "System Events" to tell process "Claude Context Admin" to set size of window 1 to {1440, 900}' >/dev/null
  sleep 4 # let the page finish loading, or the start click can land before the driver listens

  # A click in the header's empty middle starts the scene: the page reloads, so it opens on camera.
  cliclick c:640,52 w:400 m:1500,600 # brings the app forward (a harmless spot); the driver draws its own cursor
  screencapture -v -x -V "$(printf '%.0f' "$(echo "$secs + 2" | bc)")" -R0,40,1440,900 "$TMP/$scene.mov" &
  REC=$!
  sleep 0.4
  cliclick c:860,70 m:1500,600
  wait "$REC"

  # Skip the reload flash, keep the scene. Full quality for sharing; a WebP for the README.
  ffmpeg -nostdin -loglevel error -y -ss 1.6 -t "$secs" -i "$TMP/$scene.mov" -vf "fps=60" -c:v libx264 -crf 16 -preset slow -pix_fmt yuv420p -movflags +faststart "$ROOT/dist/clips/$scene.mp4"
  # The WebP: 1440 wide (sharp on Retina in GitHub's ~880 px column), 60 fps, full colour.
  rm -rf "$TMP/wf"; mkdir "$TMP/wf"
  ffmpeg -nostdin -loglevel error -i "$ROOT/dist/clips/$scene.mp4" -vf "fps=60,scale=1440:-1:flags=lanczos" "$TMP/wf/f%04d.png"
  img2webp -loop 0 -lossy -q 80 -m 4 -d 17 "$TMP"/wf/f*.png -o "$ROOT/docs/clips/$scene.webp" >/dev/null
  ls -lh "$ROOT/docs/clips/$scene.webp"
done <<< "$SCENES"
