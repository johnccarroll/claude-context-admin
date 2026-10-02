#!/usr/bin/env bash
# Prints THIRD_PARTY_NOTICES.txt: the licenses of everything compiled into cca (Go modules from the
# built binary's build info, Go itself, and the web bundle's npm packages). MIT, BSD, ISC,
# Apache-2.0 and OFL all require shipping these with the binary, so the npm packages, the Linux
# archives and the Mac app all carry it.
#
#   scripts/notices.sh <cca binary> > THIRD_PARTY_NOTICES.txt
set -euo pipefail
bin="${1:?usage: notices.sh <cca binary>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
modcache="$(go env GOMODCACHE)"
echo "Claude Context Admin includes the third-party software below, each under its own license."
section() { printf '\n%s\n%s\n' "================================================================" "$1"; }
section "Go (golang.org)"
cat "$(go env GOROOT)/LICENSE"
go version -m "$bin" | awk '$1 == "dep" { print $2 "@" $3 }' | while read -r m; do
  section "${m%@*} ${m#*@}"
  for f in "$modcache/$m"/LICENSE* "$modcache/$m"/NOTICE*; do if [ -f "$f" ]; then cat "$f"; fi; done
done
for d in "$ROOT"/web/node_modules/d3-* "$ROOT"/web/node_modules/@fontsource-variable/*; do
  section "$(node -p "const p=require('$d/package.json'); p.name + ' ' + p.version")"
  cat "$d"/LICENSE*
done
