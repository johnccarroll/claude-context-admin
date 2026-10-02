#!/usr/bin/env bash
# Signs "Claude Context Admin.app" with a Developer ID (hardened runtime, secure timestamp),
# notarizes it, staples the ticket, and writes the zip people download.
#
#   scripts/sign-mac-app.sh <app> <zip>
#
# CCA_SIGN_ID   "Developer ID Application: <name> (<TEAMID>)"
# CCA_KEYCHAIN  keychain holding that identity (CI's throwaway one); default: the search list
# Notarization, either:
#   CCA_NOTARY=<notarytool keychain profile>                       (a Mac set up once)
#   NOTARY_KEY=<path to .p8> NOTARY_KEY_ID=… NOTARY_ISSUER_ID=…     (CI)
set -euo pipefail

APP="${1:?usage: sign-mac-app.sh <app> <zip>}"
ZIP="${2:?usage: sign-mac-app.sh <app> <zip>}"
: "${CCA_SIGN_ID:?set CCA_SIGN_ID to the Developer ID Application identity}"
# shellcheck source=scripts/notarize.sh
. "$(dirname "$0")/notarize.sh"

# Inside out: the engine first, then the app around it.
codesign --force --options runtime --timestamp --sign "$CCA_SIGN_ID" "${KC[@]}" "$APP/Contents/Resources/cca"
codesign --force --options runtime --timestamp --sign "$CCA_SIGN_ID" "${KC[@]}" "$APP"
codesign --verify --strict --deep "$APP"

rm -f "$ZIP"
ditto -c -k --keepParent "$APP" "$ZIP"
notarize "$ZIP"
xcrun stapler staple "$APP"
rm "$ZIP" && ditto -c -k --keepParent "$APP" "$ZIP" # the zip people download carries the ticket

# What a user's Mac will check: Gatekeeper accepts it as a notarized Developer ID app.
verdict="$(spctl --assess --type execute --verbose=2 "$APP" 2>&1)"
echo "$verdict" >&2
grep -q "source=Notarized Developer ID" <<<"$verdict"
