#!/usr/bin/env bash
# Signs bare macOS cca binaries (the npm packages carry them) with a Developer ID, hardened
# runtime and secure timestamp, and notarizes them. A bare binary can't be stapled, so macOS
# looks the ticket up online the first time it runs; signing alone already satisfies endpoint
# security tools that block unsigned code.
#
#   scripts/sign-mac-bin.sh <binary>...
#
# Same settings as sign-mac-app.sh: CCA_SIGN_ID, CCA_KEYCHAIN, and CCA_NOTARY or NOTARY_KEY*.
set -euo pipefail

[ $# -gt 0 ] || { echo "usage: sign-mac-bin.sh <binary>..." >&2; exit 1; }
: "${CCA_SIGN_ID:?set CCA_SIGN_ID to the Developer ID Application identity}"
# shellcheck source=scripts/notarize.sh
. "$(dirname "$0")/notarize.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
for b in "$@"; do
  codesign --force --options runtime --timestamp --sign "$CCA_SIGN_ID" "${KC[@]}" "$b"
  codesign --verify --strict "$b"
  cp "$b" "$TMP/cca-$(lipo -archs "$b" | tr ' ' '-')"
done
ditto -c -k "$TMP" "$TMP.zip"
notarize "$TMP.zip"
rm -f "$TMP.zip"
