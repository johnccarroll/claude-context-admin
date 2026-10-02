#!/usr/bin/env bash
# Builds the command-line side of a Claude Context Admin release into dist/release/<version>: the
# cca binary for macOS and Linux (arm64 + x64), Linux archives with checksums, and npm packages.
# It publishes nothing. On macOS the app is the main install (scripts/build-mac-app.sh,
# sign-mac-app.sh, cask.sh); the macOS npm binaries are signed by sign-mac-bin.sh and repacked
# before publishing. docs/RELEASING.md has the whole flow.
#
#   scripts/release.sh 0.1.0
set -euo pipefail

VERSION="${1:?usage: scripts/release.sh <version>}"
REPO="johnccarroll/claude-context-admin" # source, releases and the plugin marketplace
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist/release/$VERSION"
TARGETS=(darwin/arm64 darwin/amd64 linux/arm64 linux/amd64)

rm -rf "$OUT"
mkdir -p "$OUT/archives" "$OUT/npm"
(cd "$ROOT/web" && bun install --frozen-lockfile >/dev/null && bun run build >/dev/null)

# Archive entries owned by root, not whoever ran the build (GNU tar on CI, bsdtar on macOS).
if tar --version 2>/dev/null | grep -q GNU; then
  TAR_OWNER=(--owner=0 --group=0 --numeric-owner)
else
  TAR_OWNER=(--uid 0 --gid 0 --uname root --gname root)
fi

npm_arch() { [ "$1" = amd64 ] && echo x64 || echo "$1"; }

for t in "${TARGETS[@]}"; do
  os="${t%/*}"
  arch="${t#*/}"
  stage="$OUT/stage/${os}_${arch}"
  mkdir -p "$stage"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/cca" ./cmd/cca)
  cp "$ROOT/LICENSE" "$ROOT/README.md" "$stage/"
  [ -f "$OUT/THIRD_PARTY_NOTICES.txt" ] || "$ROOT/scripts/notices.sh" "$stage/cca" > "$OUT/THIRD_PARTY_NOTICES.txt"
  cp "$OUT/THIRD_PARTY_NOTICES.txt" "$stage/"
  # Archives for Linux only: on macOS a downloaded bare binary is quarantined; the app or npm it is.
  [ "$os" = linux ] && tar "${TAR_OWNER[@]}" -C "$stage" -czf "$OUT/archives/cca_${VERSION}_${os}_${arch}.tar.gz" cca LICENSE README.md THIRD_PARTY_NOTICES.txt

  # npm: one package per platform carrying the binary
  p="$OUT/npm/claude-context-admin-$os-$(npm_arch "$arch")"
  mkdir -p "$p/bin"
  cp "$stage/cca" "$p/bin/cca"
  cp "$ROOT/LICENSE" "$stage/THIRD_PARTY_NOTICES.txt" "$p/"
  cat > "$p/package.json" <<JSON
{
  "name": "claude-context-admin-$os-$(npm_arch "$arch")",
  "version": "$VERSION",
  "description": "The cca binary for $os $(npm_arch "$arch"). Install claude-context-admin instead.",
  "license": "MIT",
  "os": ["$os"],
  "cpu": ["$(npm_arch "$arch")"],
  "author": "John Carroll",
  "repository": "github:$REPO",
  "files": ["bin/cca", "LICENSE", "THIRD_PARTY_NOTICES.txt"]
}
JSON
done

(cd "$OUT/archives" && shasum -a 256 ./*.tar.gz > checksums.txt)

# npm: the package people install; it depends on whichever platform package fits.
w="$OUT/npm/claude-context-admin"
mkdir -p "$w/bin"
cp "$ROOT/packaging/npm/bin/cca.js" "$w/bin/cca.js"
cp "$ROOT/README.md" "$ROOT/LICENSE" "$w/"
deps=""
for t in "${TARGETS[@]}"; do
  deps+="    \"claude-context-admin-${t%/*}-$(npm_arch "${t#*/}")\": \"$VERSION\",\n"
done
cat > "$w/package.json" <<JSON
{
  "name": "claude-context-admin",
  "version": "$VERSION",
  "description": "See and tidy everything Claude Code loads: memories, instructions, plugins, MCP, skills, agents and hooks.",
  "license": "MIT",
  "author": "John Carroll",
  "homepage": "https://github.com/$REPO",
  "repository": "github:$REPO",
  "bugs": "https://github.com/$REPO/issues",
  "keywords": ["claude-code", "claude", "memory", "mcp", "agents-md"],
  "bin": { "cca": "bin/cca.js" },
  "files": ["bin/cca.js", "README.md", "LICENSE"],
  "engines": { "node": ">=18" },
  "optionalDependencies": {
$(printf "%b" "${deps%,\\n}")
  }
}
JSON
for d in "$OUT"/npm/*/; do (cd "$OUT/npm" && npm pack "$d" --silent >/dev/null); done

rm -rf "$OUT/stage"
echo "Built $VERSION in $OUT"
find "$OUT/archives" "$OUT/npm" -maxdepth 1 -type f | sed "s|$OUT/||" | sort
