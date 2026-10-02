#!/usr/bin/env node
// Runs the native cca binary from the platform package npm installed alongside this one
// (claude-context-admin-<os>-<arch>, an optional dependency).
'use strict';
const { spawnSync } = require('node:child_process');

const pkg = `claude-context-admin-${process.platform}-${process.arch}`;
let bin;
try {
  bin = require.resolve(`${pkg}/bin/cca`);
} catch {
  console.error(`cca: no build for ${process.platform}-${process.arch}. Supported: macOS and Linux on arm64 and x64.`);
  console.error('If you installed with --no-optional or --omit=optional, reinstall without it.');
  process.exit(1);
}
const r = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
if (r.error) {
  console.error(`cca: ${r.error.message}`);
  process.exit(1);
}
process.exit(r.status ?? (r.signal ? 1 : 0));
