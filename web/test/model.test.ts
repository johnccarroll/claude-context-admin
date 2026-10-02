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
