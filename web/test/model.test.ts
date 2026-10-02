// Run with `bun test` (bun's built-in runner; no extra dependency).
import { expect, test } from 'bun:test';
import cases from '../../internal/scan/testdata/links.json';
import { buildModel, LINK } from '../src/model';
import type { Entry, State } from '../src/types';

test('LINK reads [[links]] exactly as the Go scanner does (shared cases)', () => {
  for (const c of cases as { text: string; targets: string[] }[]) {
    const got = [...c.text.matchAll(LINK)].map((m) => m[1].trim().replace(/\.md$/, ''));
    expect({ text: c.text, got }).toEqual({ text: c.text, got: c.targets });
  }
});

const home = '/h';
const glob = `${home}/.claude/projects/-h/memory`, app = `${home}/.claude/projects/-h-app/memory`, other = `${home}/.claude/projects/-h-other/memory`;
const mem = (dir: string, stem: string, name: string, links: string[] = [], project?: string): Entry => ({
  kind: 'memory', scope: project ? 'project' : 'user', project, path: `${dir}/${stem}.md`, name, enabled: true, links, meta: { stem },
});
const state = (entries: Entry[]): State => ({
  home, projects: [], entries, usage: {}, report: { counts: {}, findings: [] }, proposals: null, health: null, readOnly: false, os: 'darwin',
  globalMemoryDir: glob, indexMaxLines: 200, indexMaxBytes: 25600,
});

test('resolve: own folder first, then Everywhere; by slug or file name; .md and spaces ignored', () => {
  const m = buildModel(state([
    mem(glob, 'feedback_style', 'style'),
    mem(glob, 'reference_dup', 'dup'),
    mem(app, 'project_launch', 'launch', ['style', 'dup', 'gone', 'other-only'], '/h/app'),
    mem(app, 'reference_dup', 'dup', [], '/h/app'),
    mem(other, 'project_other', 'other-only', [], '/h/other'),
  ]));
  const by = (stem: string, dir = app) => m.mems.find((x) => x.stem === stem && x.path.startsWith(dir))!;
  expect(m.resolve(app, 'style')?.stem).toBe('feedback_style');            // falls back to Everywhere
  expect(m.resolve(app, 'feedback_style.md')?.stem).toBe('feedback_style'); // file name, with .md
  expect(m.resolve(app, ' dup ')?.path).toBe(`${app}/reference_dup.md`);    // own folder wins
  expect(m.resolve(app, 'other-only')).toBeUndefined();                     // other projects never load here
  const launch = by('project_launch');
  expect(launch.missing).toEqual(['gone', 'other-only']);
  expect(launch.out.map((x) => x.stem).sort()).toEqual(['feedback_style', 'reference_dup']);
  expect(launch.slug).toBe('launch');
  expect(m.linkable(app).map((x) => x.stem).sort()).toEqual(['feedback_style', 'project_launch', 'reference_dup', 'reference_dup']);
});

import { commandText } from '../src/add';

test('commandText: a usual install command reads as a command line; anything else stays exact', () => {
  expect(commandText({ command: 'npx', args: ['-y', 'some pkg', "it's"], sha256: 'x' })).toBe(`npx -y 'some pkg' 'it'\\''s'`);
  expect(commandText({ command: 'sh', args: ['install.sh'], cwd: '/tmp/p' })).toBe('sh install.sh\ncwd: /tmp/p');
  expect(commandText({ run: ['a', 'b'] })).toBe(JSON.stringify({ run: ['a', 'b'] }, null, 2));
  expect(commandText({ command: 'x', args: [1] })).toBe(JSON.stringify({ command: 'x', args: [1] }, null, 2));
});

import { composeTool, splitDoc, splitTool } from '../src/doc';

test('splitTool / composeTool: edits only what changed, keeps everything else byte for byte', () => {
  const src = '---\r\nname: notes\r\ndescription: Draft release notes\r\ntools: Read, Grep\r\n---\r\nDo it.\r\n';
  const d = splitTool(src);
  expect([d.desc, d.others, d.body]).toEqual(['Draft release notes', ['tools: Read, Grep'], 'Do it.\r\n']);
  expect(composeTool(d, d.desc!, d.body)).toBe(src); // untouched: exact bytes, CRLF kept
  expect(composeTool(d, 'Say "hi"', 'New.')).toBe('---\nname: notes\ndescription: "Say \\"hi\\""\ntools: Read, Grep\n---\nNew.');
  expect(splitTool("---\ndescription: 'It''s fast'\n---\nx").desc).toBe("It's fast");
  expect(splitTool('---\ndescription: "a \\"b\\""\n---\nx').desc).toBe('a "b"');
  for (const odd of ['description: >\n  folded', 'description: [a, b]', 'description: text # note', "description: 'bad ' quote'"]) {
    const t = splitTool(`---\n${odd}\n---\nbody`);
    expect({ odd, desc: t.desc, di: t.di }).toEqual({ odd, desc: null, di: -1 }); // not safely editable: read-only
  }
  expect(splitTool('no header').desc).toBeNull();
  expect(composeTool(splitTool('no header'), '', 'changed')).toBe('changed');
});

test('splitDoc: header in plain words for History', () => {
  const d = splitDoc('---\nname: deploy-checklist\ndescription: "Migrations first"\nmetadata:\n  type: feedback\nmodified: x\n---\nBody');
  expect(d.fields).toEqual([['Title', 'Deploy checklist'], ['Summary', 'Migrations first'], ['Kind', 'Rule']]);
  expect(d.rest).toBe('modified: x');
  expect(d.body).toBe('Body');
});

import { richText } from '../src/doc';

test('richText: code blocks, inline code, bold and links; nothing inside code is formatted', () => {
  const L = (k: string) => `<L:${k}>`;
  expect(richText('See [[a]] and **b**.', L)).toBe('See <L:a> and <b>b</b>.');
  expect(richText('Run `make [[x]]` now', L)).toBe('Run <code>make [[x]]</code> now');
  expect(richText('Before\n```ts\nconst a = 1 < 2; // [[not-a-link]] **no**\n```\nAfter [[b]]', L))
    .toBe('Before<pre class="code"><span class="lang">ts</span><code>const a = 1 &lt; 2; // [[not-a-link]] **no**</code></pre>After <L:b>');
  expect(richText('````\n```inner```\n````', L)).toBe('<pre class="code"><code>```inner```</code></pre>');
  expect(richText('```\nunclosed [[c]]', L)).toBe('```\nunclosed <L:c>'); // no closing fence: plain text
  expect(richText('<script>', L)).toBe('&lt;script&gt;');
});

test('richText: headings, quotes and lists', () => {
  const L = (k: string) => `<L:${k}>`;
  expect(richText('# Title\n> moved, see [[a]]\n> line 2\n- one `x`\n- two\n1. first\nplain', L))
    .toBe('<div class="h h1">Title</div><blockquote>moved, see <L:a>\nline 2</blockquote><ul><li>one <code>x</code></li><li>two</li></ul><ol><li>first</li></ol>plain');
  expect(richText('a\nb', L)).toBe('a\nb');
});

test('header splitting matches scan.SplitHeader, including an empty header', () => {
  expect(splitDoc('---\n---\nBody').body).toBe('Body');
  expect(splitTool('---\n---\nBody').body).toBe('Body');
  expect(splitDoc('---\nname: x\n---').body).toBe('');
  expect(splitDoc('no header').body).toBe('no header');
});
