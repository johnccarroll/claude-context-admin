# Sourced by sign-mac-app.sh and sign-mac-bin.sh: notarization settings and the submit step.
# CCA_NOTARY=<notarytool keychain profile>, or NOTARY_KEY=<.p8> NOTARY_KEY_ID=… NOTARY_ISSUER_ID=…
# shellcheck shell=bash
# shellcheck disable=SC2034 # KC is used by the scripts that source this
KC=()
# shellcheck disable=SC2034
[ -n "${CCA_KEYCHAIN:-}" ] && KC=(--keychain "$CCA_KEYCHAIN")
if [ -n "${CCA_NOTARY:-}" ]; then
  AUTH=(--keychain-profile "$CCA_NOTARY")
else
  AUTH=(--key "${NOTARY_KEY:?}" --key-id "${NOTARY_KEY_ID:?}" --issuer "${NOTARY_ISSUER_ID:?}")
fi

# notarize <zip>: submits, waits, and on a rejection prints Apple's reasons and fails.
notarize() {
  local log
  log="$(xcrun notarytool submit "$1" "${AUTH[@]}" --wait --timeout 30m)"
  echo "$log"
  if ! grep -q "status: Accepted" <<<"$log"; then
    local id
    id="$(awk '/^  id: /{print $2; exit}' <<<"$log")"
    [ -n "$id" ] && xcrun notarytool log "$id" "${AUTH[@]}" >&2
    echo "notarization was not accepted" >&2
    return 1
  fi
}
