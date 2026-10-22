#!/usr/bin/env bash
# Records the README tour (docs/tour.webp) and a launch clip (dist/tour.mp4, not committed) from the real Mac app
# on the made-up demo home. Nothing from your own setup is shown.
#
#   scripts/build-mac-app.sh 0.0.0-dev && scripts/tour.sh
#
# The app serves a copy of the UI with scripts/tour-driver.js added (the shipped UI never has it);
# the driver draws a cursor and walks the tour, and screencapture records the app's window.
# macOS only. The screen must be unlocked, and the terminal needs Screen Recording and
# Accessibility permission (to record the window, place it and press the start key). Needs
# cliclick, ffmpeg and img2webp (libwebp).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/dist/mac/Claude Context Admin.app/Contents/MacOS/Claude Context Admin"
TMP="$(mktemp -d)"
LENGTH=46  # seconds recorded: the whole tour
CLIP=27    # seconds in the launch clip: up to the reset back to the whole map
trap 'osascript -e "quit app \"Claude Context Admin\"" >/dev/null 2>&1 || true; kill "${ENGINE:-}" 2>/dev/null || true; rm -rf "$TMP"' EXIT
[ -x "$APP" ] || { echo "build the app first: scripts/build-mac-app.sh 0.0.0-dev" >&2; exit 1; }
for c in cliclick ffmpeg img2webp; do command -v "$c" >/dev/null || { echo "needs $c" >&2; exit 1; }; done

# The UI, built, plus the driver.
(cd "$ROOT/web" && bun run build >/dev/null)
cp -R "$ROOT/web/dist" "$TMP/web"
cp "$ROOT/scripts/tour-driver.js" "$TMP/web/tour-driver.js"
sed -i '' 's|</body>|<script src="./tour-driver.js"></script></body>|' "$TMP/web/index.html"
grep -q tour-driver "$TMP/web/index.html" || { echo "couldn't add the driver to index.html" >&2; exit 1; }

python3 "$ROOT/scripts/demo-home.py" "$TMP/home" >/dev/null
(cd "$ROOT" && go build -o "$TMP/cca" ./cmd/cca)
CCA_WEB_DIR="$TMP/web" "$TMP/cca" --home "$TMP/home" --no-open --port 0 > "$TMP/log" 2>&1 &
ENGINE=$!
for _ in $(seq 50); do grep -q "http://" "$TMP/log" && break; sleep 0.1; done
URL="$(grep -o 'http://[^ ]*' "$TMP/log")&hue=260&tour"

# The app, 1440x900 at the top left of the main screen, on the map.
osascript -e 'quit app "Claude Context Admin"' >/dev/null 2>&1 || true
while pgrep -f "Claude Context Admin.app/Contents/MacOS" >/dev/null; do sleep 0.3; done
CCA_URL="$URL#map" "$APP" >/dev/null 2>&1 &
sleep 5
osascript -e 'tell application "Claude Context Admin" to activate' \
  -e 'tell application "System Events" to tell process "Claude Context Admin" to set position of window 1 to {0, 40}' \
  -e 'tell application "System Events" to tell process "Claude Context Admin" to set size of window 1 to {1440, 900}' >/dev/null
sleep 2

# Record the window while the driver runs: a click in the header's empty middle starts it (the page
# reloads, so the map lifts on camera).
cliclick c:860,70 w:400 # brings the app forward; then the real cursor leaves, the driver draws its own
cliclick m:1500,600
screencapture -v -x -V "$LENGTH" -R0,40,1440,900 "$TMP/tour.mov" &
REC=$!
sleep 0.4
cliclick c:860,70 m:1500,600
wait "$REC"

# The launch clip at full resolution, and the README's GIF of the whole tour.
ffmpeg -loglevel error -y -i "$TMP/tour.mov" -t "$CLIP" -vf "fps=60" -c:v libx264 -crf 17 -preset slow -pix_fmt yuv420p -movflags +faststart "$ROOT/dist/tour.mp4"
# The README's tour as an animated WebP: full colour at 1200 wide and 15 fps in ~8 MB. A GIF of the
# same quality is ~40 MB, because the turning, glowing map changes every pixel of every frame.
mkdir "$TMP/wf"
ffmpeg -loglevel error -i "$TMP/tour.mov" -vf "fps=15,scale=1200:-1:flags=lanczos" "$TMP/wf/f%04d.png"
img2webp -loop 0 -lossy -q 72 -m 6 -d 67 "$TMP"/wf/f*.png -o "$ROOT/docs/tour.webp" >/dev/null
cp "$TMP/tour.mov" "${TOUR_KEEP:-/dev/null}" 2>/dev/null || true
ls -lh "$ROOT/dist/tour.mp4" "$ROOT/docs/tour.webp"
