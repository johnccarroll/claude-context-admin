#!/usr/bin/env bash
# Builds "Claude Context Admin.app" (universal: Apple Silicon + Intel) into dist/mac, with the cca
# engine inside at Contents/Resources/cca. With Developer ID signing and notarization it also makes
# the zip to share; a local build makes none, since its signature works only on this Mac and names
# the developer's own certificate.
#
#   scripts/build-mac-app.sh 0.1.0
#
# Signing: with CCA_SIGN_ID="Developer ID Application: <name> (<TEAMID>)" the app is signed with
# the hardened runtime, and with CCA_NOTARY=<notarytool keychain profile> also notarized and
# stapled. Without them it is signed for this Mac only: with an Apple Development certificate if
# one is installed (so macOS remembers permissions across rebuilds), otherwise ad hoc.
set -euo pipefail

VERSION="${1:-0.0.0-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist/mac"
APP="$OUT/Claude Context Admin.app"
ZIP="$OUT/ClaudeContextAdmin-$VERSION-macos.zip"

rm -rf "$OUT"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
[ -n "${SKIP_WEB:-}" ] || (cd "$ROOT/web" && bun install --frozen-lockfile >/dev/null && bun run build >/dev/null)

# The engine: the same cca binary as every other install, universal.
for arch in arm64 amd64; do
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/cca-$arch" ./cmd/cca)
done
lipo -create -output "$APP/Contents/Resources/cca" "$OUT/cca-arm64" "$OUT/cca-amd64"
"$ROOT/scripts/notices.sh" "$OUT/cca-arm64" > "$APP/Contents/Resources/THIRD_PARTY_NOTICES.txt"

# The window: macos/App.swift, universal.
for arch in arm64 x86_64; do
  xcrun swiftc -parse-as-library -O -target "$arch-apple-macos13.0" "$ROOT/macos/App.swift" -o "$OUT/app-$arch"
done
lipo -create -output "$APP/Contents/MacOS/Claude Context Admin" "$OUT/app-arm64" "$OUT/app-x86_64"
rm -f "$OUT"/cca-* "$OUT"/app-*

sed "s/__VERSION__/$VERSION/g" "$ROOT/macos/Info.plist" > "$APP/Contents/Info.plist"
cp "$ROOT/macos/AppIcon.icns" "$APP/Contents/Resources/"
cp "$ROOT/LICENSE" "$APP/Contents/Resources/"

if [ -n "${CCA_SIGN_ID:-}" ] && [ -n "${CCA_NOTARY:-}" ]; then
  echo "Built $APP"
  "$ROOT/scripts/sign-mac-app.sh" "$APP" "$ZIP"
  echo "      $ZIP ($(du -h "$ZIP" | cut -f1))"
else
  # Local builds: an Apple Development certificate if there is one. A stable identity lets macOS
  # remember folder permissions across rebuilds (ad hoc signing asks again after every build).
  # No zip: this signature works only on this Mac and names the developer's own certificate.
  ID="$(security find-identity -v -p codesigning 2>/dev/null | grep -o '"Apple Development[^"]*"' | head -1 | tr -d '"' || true)"
  echo "note: signing for this Mac only with ${ID:-an ad hoc signature} (set CCA_SIGN_ID and CCA_NOTARY for a release)" >&2
  codesign --force --sign "${ID:--}" "$APP/Contents/Resources/cca"
  codesign --force --sign "${ID:--}" "$APP"
  codesign --verify --strict --deep "$APP"
  echo "Built $APP"
fi
