#!/usr/bin/env bash
# Prints THIRD_PARTY_NOTICES.txt: the licenses of everything compiled into cca (Go modules from the
# built binary's build info, Go itself, and every npm package the web bundle can include). MIT, BSD, ISC,
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
# Every npm package the web bundle can include: the UI's dependencies and theirs, recursively.
node -e '
const fs = require("fs"), path = require("path"), root = process.argv[1], nm = path.join(root, "node_modules");
const seen = new Set(), queue = Object.keys(require(path.join(root, "package.json")).dependencies);
while (queue.length) {
  const name = queue.shift();
  if (seen.has(name)) continue;
  seen.add(name);
  const pkg = require(path.join(nm, name, "package.json"));
  queue.push(...Object.keys(pkg.dependencies || {}));
}
for (const name of [...seen].sort()) console.log(path.join(nm, name));
' "$ROOT/web" | while read -r d; do
  section "$(node -p "const p=require('$d/package.json'); p.name + ' ' + p.version + ' (' + p.license + ')'")"
  found=0
  for f in "$d"/LICENSE* "$d"/LICENCE* "$d"/license* "$d"/NOTICE*; do if [ -f "$f" ]; then cat "$f"; found=1; fi; done
  if [ "$found" = 0 ]; then echo "No license file ships with this package; its package.json declares the license above."; fi
done
