// Bundles the UI into dist/, which the Go binary embeds.
import { cpSync, rmSync } from 'node:fs';

rmSync('dist', { recursive: true, force: true });
const res = await Bun.build({
  entrypoints: ['src/main.ts'],
  outdir: 'dist',
  minify: true,
  target: 'browser',
  naming: { entry: '[name].[ext]', asset: 'assets/[name]-[hash].[ext]' },
});
if (!res.success) {
  for (const log of res.logs) console.error(log);
  process.exit(1);
}
cpSync('index.html', 'dist/index.html');
console.log(res.outputs.map((o) => `${o.path.replace(process.cwd() + '/', '')} ${(o.size / 1024).toFixed(1)} KB`).join('\n'));
