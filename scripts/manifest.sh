#!/usr/bin/env bash
# Prints one SHA-256 over every file in a folder (names and contents), so a later job can check an
# artifact is exactly what an earlier job built. Same output on macOS and Linux.
#
#   scripts/manifest.sh dist/release/0.1.0
set -euo pipefail
cd "${1:?usage: manifest.sh <dir>}"
find . -type f -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256 | shasum -a 256 | cut -d' ' -f1
