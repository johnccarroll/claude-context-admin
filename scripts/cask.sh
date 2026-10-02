#!/usr/bin/env bash
# Prints the Homebrew cask for a signed, notarized app zip (the johnccarroll/homebrew-tap repo
# keeps it at Casks/claude-context-admin.rb). It installs the app and puts its cca on the PATH.
#
#   scripts/cask.sh 0.1.0 dist/mac/ClaudeContextAdmin-0.1.0-macos.zip > claude-context-admin.rb
set -euo pipefail

VERSION="${1:?usage: cask.sh <version> <zip>}"
ZIP="${2:?usage: cask.sh <version> <zip>}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] || { echo "not a version: $VERSION" >&2; exit 1; }
SHA="$(shasum -a 256 "$ZIP" | cut -d' ' -f1)"

cat <<RUBY
cask "claude-context-admin" do
  version "$VERSION"
  sha256 "$SHA"

  url "https://github.com/johnccarroll/claude-context-admin/releases/download/v#{version}/ClaudeContextAdmin-#{version}-macos.zip"
  name "Claude Context Admin"
  desc "Control panel for Claude Code memories, skills, plugins and MCP servers"
  homepage "https://github.com/johnccarroll/claude-context-admin"

  depends_on macos: :ventura

  app "Claude Context Admin.app"
  binary "#{appdir}/Claude Context Admin.app/Contents/Resources/cca"

  uninstall quit: "dev.johncarroll.claude-context-admin"

  zap trash: [
    "~/Library/Application Support/claude-context-admin",
    "~/Library/Caches/claude-context-admin",
    "~/Library/Caches/dev.johncarroll.claude-context-admin",
    "~/Library/HTTPStorages/dev.johncarroll.claude-context-admin",
    "~/Library/Logs/Claude Context Admin.log",
    "~/Library/Preferences/dev.johncarroll.claude-context-admin.plist",
    "~/Library/Saved Application State/dev.johncarroll.claude-context-admin.savedState",
    "~/Library/WebKit/dev.johncarroll.claude-context-admin",
  ]
end
RUBY
