// Turns the raw inventory into the view models each screen renders.
import type { Entry, Finding, Prefs, State, Stat } from './types';
import { platform } from './ui';

export const KIND: Record<string, string> = { feedback: 'Rule', project: 'Project note', reference: 'Reference', user: 'About you' };
export const PLURAL: Record<string, string> = { feedback: 'Rules', project: 'Project notes', reference: 'References', user: 'About you' };
export const KHELP: Record<string, string> = {
  feedback: 'How Claude should work with you', project: 'Context about ongoing work',
  reference: 'Where to find things', user: 'Facts about you',
};
export const GLOBAL = 'Global';

export interface ProjectView { key: string; label: string; defaultLabel: string; path: string; color: string; exists: boolean; favorite: boolean; hidden: boolean }

export interface Mem {
  id: string; path: string; project: string; stem: string; slug: string; type: string; title: string; desc: string;
  modified: string; mtime: string; badYaml: boolean; links: string[];
  out: Mem[]; inn: Mem[]; missing: string[]; uses: number; lastUsed: string; agent?: string;
}

export const human = (s: string): string => {
  const t = s.replace(/^(feedback|project|reference|user)[_-]/, '').replace(/[_-]+/g, ' ').trim();
  return t.charAt(0).toUpperCase() + t.slice(1);
};
const base = (p: string): string => p.split('/').filter(Boolean).pop() ?? p;
const titleCase = (s: string): string => s.replace(/[-_]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
const day = (iso?: string): string => (iso ?? '').slice(0, 10);

export interface Model {
  state: State;
  projects: ProjectView[];
  projectOf: (key: string) => ProjectView;
  mems: Mem[];
  byId: Map<string, Mem>;
  usage: Record<string, Stat>;
  entries: (kind: Entry['kind']) => Entry[];
  /** The memory a [[key]] in a memory in dir opens, the same way Claude Code resolves it. */
  resolve: (dir: string, key: string) => Mem | undefined;
  /** Memories a memory in dir can link to: its own folder's, then the ones loaded everywhere. */
  linkable: (dir: string) => Mem[];
}

/** One [[link]] in memory text: the whole match, its target and where it starts. */
export const LINK = /\[\[([^[\]|#\n]+)(?:[|#][^\]\n]*)?\]\]/g; // = scan.Wikilink
export const dirOf = (p: string): string => p.slice(0, p.lastIndexOf('/'));

export function buildModel(state: State, prefs: Prefs = { projects: {}, order: [] }): Model {
  const usage = state.usage ?? {};
  const keyOf = (e: Entry): string => (e.scope === 'user' || !e.project ? GLOBAL : e.project);

  const mems: Mem[] = state.entries.filter((e) => e.kind === 'memory').map((e) => {
    const stem = String(e.meta?.stem ?? e.name);
    const u = usage['file:' + (e.path ?? '')];
    return {
      id: e.path ?? stem, path: e.path ?? '', project: keyOf(e), stem, slug: e.name || stem, type: e.type || stem.split('_')[0] || 'reference',
      title: typeof e.meta?.title === 'string' ? e.meta.title : human(e.name.includes('-') || e.name.includes('_') ? e.name : stem), desc: e.description ?? '',
      modified: day(e.modified), mtime: e.modified ?? '',
      badYaml: (e.issues ?? []).includes('bad-frontmatter'), links: e.links ?? [], out: [], inn: [], missing: [],
      uses: u?.count ?? 0, lastUsed: day(u?.last), agent: e.meta?.agent as string | undefined,
    };
  });

  // Resolve [[links]] the same way the audit does: stem, then name, same dir first, then global.
  const byPath = new Map(state.entries.map((e) => [e.path ?? '', e]));
  const index = new Map<string, Map<string, Mem>>();
  for (const m of mems) {
    const d = dirOf(m.path);
    if (!index.has(d)) index.set(d, new Map());
    const byKey = index.get(d)!;
    byKey.set(m.stem, m);
    const fmName = byPath.get(m.path)?.name;
    if (fmName) byKey.set(fmName, m);
  }
  const globalDir = mems.find((m) => m.project === GLOBAL && !m.agent)?.path;
  const globalIndex = globalDir ? index.get(dirOf(globalDir)) : undefined;
  const resolve = (dir: string, key: string): Mem | undefined => {
    const k = key.trim().replace(/\.md$/, ''); // as the scanner reads it
    return index.get(dir)?.get(k) ?? globalIndex?.get(k);
  };
  const linkable = (dir: string): Mem[] => mems.filter((m) => !m.agent && (dirOf(m.path) === dir || (globalDir && dirOf(m.path) === dirOf(globalDir))));
  for (const m of mems) {
    for (const l of m.links) {
      const t = resolve(dirOf(m.path), l);
      if (!t) m.missing.push(l);
      else if (t !== m && !m.out.includes(t)) { m.out.push(t); t.inn.push(m); }
    }
  }

  // Projects that have memories or project-scoped config, Global first, then by size.
  const counts = new Map<string, number>();
  for (const e of state.entries) if (e.kind !== 'plugin') counts.set(keyOf(e), (counts.get(keyOf(e)) ?? 0) + 1);
  const keys = [...counts.keys()].sort((a, b) => (a === GLOBAL ? -1 : b === GLOBAL ? 1 : (counts.get(b) ?? 0) - (counts.get(a) ?? 0)));
  const exists = new Set(state.projects.filter((p) => p.exists).map((p) => p.path));
  const repos = new Set(state.projects.filter((p) => p.repo).map((p) => p.path));
  const encHome = state.home.replace(/[/.]/g, '-') + '-';
  const label = (k: string): string => {
    if (k === GLOBAL) return 'Everywhere';
    if (!k.startsWith('/')) return titleCase(k.replace(encHome, '').replace(/^dev-/, '')) + ' (folder gone)';
    const parent = keys.filter((o) => o !== k && o.startsWith('/') && k.startsWith(o + '/')).sort((a, b) => b.length - a.length)[0];
    // Prefix only a subfolder of another project's repo (app/web), not a repo inside a plain folder (~/code/app).
    const name = parent && !repos.has(k) ? `${titleCase(base(parent))} / ${titleCase(k.slice(parent.length + 1))}` : titleCase(base(k));
    return exists.has(k) ? name : name + ' (folder gone)';
  };
  // Colors follow the default order so they stay stable when the user reorders.
  const projects: ProjectView[] = keys.map((k, i) => {
    const pp = prefs.projects[k] ?? {};
    return {
      key: k, path: k === GLOBAL ? state.home : k, defaultLabel: label(k), label: pp.alias || label(k),
      exists: k === GLOBAL || exists.has(k), favorite: !!pp.favorite, hidden: !!pp.hidden && k !== GLOBAL,
      color: `var(--c${(i % 7) + 1})`,
    };
  });
  const rank = (k: string): number => { const i = prefs.order.indexOf(k); return i < 0 ? 1e6 + keys.indexOf(k) : i; };
  projects.sort((a, b) => (a.key === GLOBAL ? -1 : b.key === GLOBAL ? 1 : rank(a.key) - rank(b.key)));
  const pmap = new Map(projects.map((p) => [p.key, p]));
  const projectOf = (key: string): ProjectView =>
    pmap.get(key) ?? { key, path: key, label: titleCase(base(key)), defaultLabel: titleCase(base(key)), color: 'var(--faint)', exists: false, favorite: false, hidden: false };

  return {
    state, projects, projectOf, mems, byId: new Map(mems.map((m) => [m.id, m])), usage,
    entries: (kind) => state.entries.filter((e) => e.kind === kind), resolve, linkable,
  };
}

// ---------- Review ----------

export type Tone = 'fix' | 'warn' | 'del';
export interface Action { label: string; op: string; args: Record<string, unknown>; danger?: boolean }
export interface ReviewItem {
  id: string; tone: Tone; icon: string; title: string; body: string;
  finding?: Finding; // instruction-health: confidence, file and evidence lines
  preview?: string; // the text a Claude suggestion would write, shown before you accept
  mems?: Mem[]; paths?: string[]; pills?: [string, 'old' | 'new'][];
  primary: Action; secondary: Action; source: string;
  /** Items a group fix applies to, each with a checkbox: actions with a `paths` argument act
   *  only on the checked ones. pick 'none' starts unchecked (destructive fixes). */
  targets?: Target[]; pick?: 'all' | 'none';
  /** More choices in a menu (e.g. other memories to link to). */
  more?: { label: string; items: [string, Action][] };
}
export interface Target { path: string; label: string; mem?: Mem }

const tok = (s: string): Set<string> =>
  new Set(s.toLowerCase().split(/[^a-z0-9]+/).filter((w) => w.length > 2 && !['feedback', 'project', 'reference', 'user', 'the', 'and'].includes(w)));
export const similarity = (a: string, b: string): number => {
  const A = tok(a), B = tok(b);
  let i = 0;
  A.forEach((x) => { if (B.has(x)) i++; });
  return i / Math.max(1, Math.min(A.size, B.size));
};
const group = <T>(xs: T[], key: (x: T) => string): Map<string, T[]> => {
  const m = new Map<string, T[]>();
  for (const x of xs) { const k = key(x); m.set(k, [...(m.get(k) ?? []), x]); }
  return m;
};

const OP_TITLE: Record<string, string> = {
  'memory-save': 'edit a memory', 'memory-create': 'add a memory', 'memory-trash': 'move a memory to the Trash', 'memory-promote': 'make a memory global',
  relink: 'fix a broken link', unlink: 'remove a broken link', 'fix-frontmatter': 'repair a memory header', 'merge-global': 'merge two memories',
  'make-global': 'make skills global', 'remove-hooks': 'remove dead hooks', 'quiet-caps': 'rewrite all-caps emphasis', 'convert-to-agents': 'convert CLAUDE.md to AGENTS.md',
  'plugin-enable': 'turn on a plugin', 'plugin-disable': 'turn off a plugin', 'plugin-uninstall': 'uninstall a plugin', 'mcp-remove': 'remove an MCP server', bulk: 'change several memories',
};

/** 'on', 'missing', or the plugin id when it's installed but off. */
export function pluginState(m: Model): string {
  const p = m.entries('plugin').find((e) => String(e.meta?.id ?? '').startsWith('context-admin@'));
  return !p ? 'missing' : p.enabled ? 'on' : String(p.meta?.id);
}

/** One line per argument of a Claude proposal (lists in full), long texts shown separately. */
function proposalDetails(a: Record<string, unknown>, home: string): string {
  const show = (v: unknown): string => (typeof v === 'string' ? v.replace(home, '~') : JSON.stringify(v));
  // Files first: a long argument can't push what gets changed out of sight.
  const first = (k: string): number => (['paths', 'path', 'keep', 'drop'].includes(k) ? 0 : 1);
  return Object.entries(a).filter(([k]) => !['body', 'content', 'description'].includes(k)).sort(([x], [y]) => first(x) - first(y)).map(([k, v]) =>
    Array.isArray(v) ? `${k} (${v.length}):\n${v.map((x) => '  ' + show(x)).join('\n')}` : `${k}: ${show(v)}`).join('\n');
}

export function buildReview(m: Model): ReviewItem[] {
  const out: ReviewItem[] = [];
  // Claude's suggestions (from cca mcp) come first: the user asked Claude for them.
  for (const p of m.state.proposals ?? []) {
    const a = p.args ?? {};
    const files = [a.path, a.keep, a.drop, ...(Array.isArray(a.paths) ? a.paths : [])].filter((x): x is string => typeof x === 'string');
    const target = typeof a.id === 'string' ? a.id.split('@')[0] : typeof a.name === 'string' ? a.name : typeof a.title === 'string' ? a.title : '';
    out.push({ id: 'proposal:' + p.id, tone: 'fix', icon: '✦', title: `Claude suggests: ${OP_TITLE[p.op] ?? p.op}${target ? ` (${target})` : ''}`,
      body: p.reason, pills: files.slice(0, 6).map((f) => [f.split('/').pop() ?? f, 'new'] as [string, 'new']),
      primary: { label: 'Accept', op: 'proposal-accept', args: { id: p.id } }, secondary: { label: 'Dismiss', op: 'proposal-dismiss', args: { id: p.id } },
      source: 'Suggested by Claude',
      // Everything accepting will do: every argument, every path, not just the reason Claude gave.
      preview: [proposalDetails(a, m.state.home), typeof a.description === 'string' && a.description ? 'Summary: ' + a.description : '',
        typeof a.body === 'string' ? a.body : typeof a.content === 'string' ? a.content : ''].filter(Boolean).join('\n\n') || undefined });
  }
  // Setup: the context-admin plugin lets Claude read all this and suggest changes for Review.
  const plug = pluginState(m);
  if (plug === 'missing') out.push({ id: 'connect', tone: 'fix', icon: '✦', title: 'Let Claude help keep this tidy',
    body: 'Install the context-admin plugin and Claude can look things up here, flag a memory it writes badly, and suggest cleanups that wait in Review for you. Run the two commands in a terminal.',
    primary: { label: 'Copy install commands', op: 'copy-connect', args: {} }, secondary: { label: 'Not now', op: 'connect-later', args: {} }, source: 'Setup' });
  else if (plug !== 'on') out.push({ id: 'connect', tone: 'fix', icon: '✦', title: 'Turn on the context-admin plugin',
    body: 'It is installed but off, so Claude can’t see this inventory or suggest changes.',
    primary: { label: 'Turn on', op: 'plugin-enable', args: { id: plug } }, secondary: { label: 'Not now', op: 'connect-later', args: {} }, source: 'Setup' });

  const f = (code: string): Finding[] => m.state.report.findings.filter((x) => x.code === code);
  const plural = (n: number, one: string, many = one + 's'): string => `${n} ${n === 1 ? one : many}`;

  // A renamed or moved repo: Claude Code keys memory by folder, so these load nowhere.
  const tilde = (p: string): string => p.replace(m.state.home, '~');
  for (const g of f('folder-moved')) {
    const mems = m.mems.filter((x) => x.path.startsWith(g.path + '/'));
    const to = g.detail ?? '';
    out.push({ id: 'moved:' + g.path, tone: 'warn', icon: '↪', title: `${plural(mems.length, 'memory', 'memories')} for a folder that's gone: ${tilde(g.project ?? '')}`,
      body: `Claude Code finds a project's memories by its folder, so these load in no project.${to ? ` It looks like the folder became ${tilde(to)}.` : ' Pick the folder it moved to.'}`,
      mems, primary: to ? { label: `Move to ${tilde(to)}`, op: 'project-relocate', args: { from: g.path, to } } : { label: 'Choose folder…', op: 'relocate-pick', args: { from: g.path } },
      secondary: to ? { label: 'Somewhere else…', op: 'relocate-pick', args: { from: g.path } } : { label: 'Leave it', op: 'dismiss', args: {} }, source: 'Built-in check', finding: undefined });
  }

  // Broken links, grouped by target, with the closest existing memory as the suggested fix.
  const missing = group(m.mems.flatMap((mm) => mm.missing.map((t) => ({ t, mm }))), (x) => x.t);
  for (const [target, refs] of [...missing].sort((a, b) => b[1].length - a[1].length)) {
    const src = refs.map((r) => r.mm);
    const ranked = m.mems.filter((c) => c.project === src[0].project || c.project === GLOBAL)
      .map((c) => ({ c, s: similarity(target, c.stem) })).sort((a, b) => b.s - a.s);
    const best: Mem | undefined = ranked[0]?.c, score = ranked[0]?.s ?? 0;
    const targets = src.map((s) => ({ path: s.path, label: s.title, mem: s }));
    const others = { label: 'Link to another…', items: ranked.slice(best && score >= 0.5 ? 1 : 0, 8).filter((x) => x.s >= 0.25).map(({ c }) => // only plausible matches
      [`${c.title} · ${m.projectOf(c.project).label}`, { label: c.title, op: 'relink', args: { from: target, to: c.stem, paths: src.map((s) => s.path) } }] as [string, Action]) };
    const where = best ? m.projectOf(best.project).label : '';
    if (best && score >= 0.5) {
      out.push({ id: 'link:' + target, tone: 'fix', icon: '↺', title: `Broken link to “${human(target)}” in ${plural(src.length, 'memory', 'memories')}`,
        body: `They point to “${human(target)}”, which isn't a file Claude can open. ${best.title.toLowerCase() === human(target).toLowerCase() ? 'A memory with that title exists' : `The closest match is “${best.title}”`} in ${where}.`,
        targets, more: others, pills: [[human(target) + ' (missing)', 'old'], [`${best.title} · ${KIND[best.type] ?? best.type} · ${where}`, 'new']],
        primary: { label: 'Link to this', op: 'relink', args: { from: target, to: best.stem, paths: src.map((s) => s.path) } },
        secondary: { label: 'Remove link', op: 'unlink', args: { target, paths: src.map((s) => s.path) } }, source: 'Built-in check' });
    } else {
      out.push({ id: 'link:' + target, tone: 'warn', icon: '?', title: `“${human(target)}” is linked but was never written`,
        body: `${plural(src.length, 'memory links', 'memories link')} to it. Create it as a short note, link to a memory that exists, or remove the links.`, targets, more: others,
        primary: { label: 'Create note', op: 'create-stub', args: { name: target, dir: src[0].path.slice(0, src[0].path.lastIndexOf('/')) } },
        secondary: { label: 'Remove links', op: 'unlink', args: { target, paths: src.map((s) => s.path) } }, source: 'Built-in check' });
    }
  }

  for (const b of m.mems.filter((x) => x.badYaml)) out.push({ id: 'yaml:' + b.path, tone: 'warn', icon: '!', title: `“${b.title}” has a header Claude may not read`,
    body: 'Its description isn’t valid YAML (a quoted phrase with more text after it). Quoting the whole line fixes it.', mems: [b],
    primary: { label: 'Fix header', op: 'fix-frontmatter', args: { path: b.path } }, secondary: { label: 'Keep', op: 'dismiss', args: {} }, source: 'Built-in check' });

  const copies = f('copied-across-projects');
  if (copies.length) {
    const projects = (copies[0].detail ?? '').split(' in ')[1] ?? '';
    out.push({ id: 'copies', tone: 'fix', icon: '⧉', title: `${plural(copies.length, 'skill or command is', 'skills and commands are')} copied into several projects`,
      body: `Identical copies live in ${projects}. Keep one copy that every project uses.`,
      targets: copyPaths(m, copies).map((p) => { const e = m.state.entries.find((x) => x.path === p); return { path: p, label: `${e?.kind === 'command' ? '/' : ''}${e?.name} · ${m.projectOf(e?.project ?? '').label}` }; }),
      primary: { label: 'Make global', op: 'make-global', args: { paths: copyPaths(m, copies) } },
      secondary: { label: 'Keep copies', op: 'dismiss', args: {} }, source: 'Built-in check' });
  }

  const dead = f('hook-script-missing');
  if (dead.length) out.push({ id: 'dead-hooks', tone: 'del', icon: '⌁', title: `${plural(dead.length, 'hook points', 'hooks point')} to a script that no longer exists`,
    body: `${dead[0].detail} is gone. The hook still runs on every matching event but does nothing.`, paths: dead.map((d) => d.path),
    primary: { label: dead.length > 1 ? 'Remove hooks' : 'Remove hook', op: 'remove-hooks', args: { script: dead[0].detail } },
    secondary: { label: 'Keep', op: 'dismiss', args: {} }, source: 'Built-in check' });

  for (const a of f('agents-md-ignored')) out.push({ id: 'ignored:' + a.path, tone: 'warn', icon: '!',
    title: `AGENTS.md in ${m.projectOf(a.project ?? '').label} is ignored`,
    body: 'This project also has a CLAUDE.md, so Claude Code skips its AGENTS.md (the default instructionFiles setting). Merge them into one file.',
    paths: [a.path], primary: { label: 'Show instructions', op: 'view-ins', args: {} }, secondary: { label: 'Keep', op: 'dismiss', args: {} }, source: 'Built-in check' });
  for (const c of f('claude-md-convertible')) out.push({ id: 'convert:' + c.path, tone: 'fix', icon: 'A',
    title: `Convert ${m.projectOf(c.project ?? '').label}'s CLAUDE.md to AGENTS.md`,
    body: 'Claude Code 2.1.277+ reads AGENTS.md, and so do other agent tools. The content stays the same; the old file goes to the Trash.',
    paths: [c.path], primary: { label: 'Convert', op: 'convert-to-agents', args: { path: c.path } }, secondary: { label: 'Keep CLAUDE.md', op: 'dismiss', args: {} }, source: 'Built-in check' });

  // Instruction health (the mechanical part of /claude-api prompt-audit).
  const file = (p: string): string => p.replace(m.state.home, '~');
  const askClaude = (x: Finding): Action => ({ label: 'Ask Claude to fix', op: 'copy-audit', args: { path: x.path, project: x.project ?? '' } });
  const keep: Action = { label: 'Keep', op: 'dismiss', args: {} };
  const HEALTH: Record<string, (x: Finding) => Omit<ReviewItem, 'id' | 'source' | 'finding'>> = {
    'pressure-language': (x) => ({ tone: 'fix', icon: '¶', title: `${x.detail} all-caps MUST / NEVER lines in ${file(x.path)}`,
      body: 'Current Claude models follow instructions closely, so stacked emphasis makes them over-apply rules and hedge. Stating them at normal volume keeps every rule; the reason next to each one does the work.',
      primary: { label: 'Preview changes', op: 'preview-caps', args: { path: x.path } }, secondary: keep }),
    'dated-scaffold': (x) => ({ tone: 'warn', icon: '¶', title: `Prompting written for older models in ${file(x.path)}`,
      body: '“Think step by step”, scratchpad tags and “don’t be lazy” were workarounds for earlier models. Current models plan and reason on their own, and these lines can make them over-plan.',
      primary: askClaude(x), secondary: keep }),
    'retired-model': (x) => ({ tone: 'warn', icon: '¶', title: `${file(x.path)} names retired Claude models`,
      body: 'Instructions pinned to an old model drift as models change. State the current rule without the model name.',
      primary: askClaude(x), secondary: keep }),
    'missing-path': (x) => ({ tone: 'warn', icon: '¶', title: `${file(x.path)} mentions paths that aren’t on this computer`,
      body: 'They may live on another machine, so nothing is changed automatically. Update them if they moved.',
      primary: { label: platform.show, op: 'reveal', args: { path: x.path } }, secondary: { label: 'They’re elsewhere', op: 'dismiss', args: {} } }),
    'verbose-skill': (x) => ({ tone: 'warn', icon: '¶', title: `${file(x.path).split('/').slice(-2, -1)[0]} skill is ${x.detail} lines`,
      body: 'Its whole text loads every time it runs. Keep what only you know (accounts, quirks, decisions) and drop general explanations Claude already knows.',
      primary: askClaude(x), secondary: keep }),
  };
  for (const x of m.state.report.findings) {
    const h = HEALTH[x.code];
    if (h) out.push({ id: `health:${x.code}:${x.path}`, ...h(x), finding: x, source: 'Instruction check' });
  }

  for (const name of new Set(m.entries('plugin').filter((p) => p.issues?.includes('plugin-installed-twice')).map((p) => p.name))) {
    const ids = m.entries('plugin').filter((p) => p.name === name).map((p) => String(p.meta?.id));
    out.push({ id: 'twice:' + name, tone: 'warn', icon: '⧉', title: `${name} is installed twice`,
      body: `It comes from ${ids.map((i) => i.split('@')[1]).join(' and ')}. Both copies load its skills.`,
      primary: { label: 'Remove the duplicate', op: 'plugin-uninstall', args: { id: ids[ids.length - 1] } },
      secondary: { label: 'Keep both', op: 'dismiss', args: {} }, source: 'Built-in check' });
  }

  for (const p of f('plugin-unused')) {
    const e = m.entries('plugin').find((x) => x.path === p.path);
    const cost = Number(e?.meta?.alwaysOnTokens ?? 0);
    out.push({ id: 'unused:' + p.detail, tone: 'warn', icon: '↓', title: `${p.detail} costs ${cost.toLocaleString()} tokens every session and wasn't used in 90 days`,
      body: 'Turning it off frees that context in every project. You can turn it back on any time.',
      primary: { label: 'Turn off', op: 'plugin-disable', args: { id: e?.meta?.id } }, secondary: { label: 'Keep on', op: 'dismiss', args: {} }, source: 'Usage' });
  }
  for (const s of f('mcp-unused')) out.push({ id: 'mcp:' + s.detail, tone: 'warn', icon: '↓', title: `MCP server ${s.detail} wasn't used in 90 days`,
    body: 'Unused servers still add tool definitions Claude can search. Remove it if you no longer need it.',
    primary: { label: 'Remove', op: 'mcp-remove', args: { name: s.detail, project: s.project ?? '', scope: m.entries('mcp').find((e) => e.name === s.detail && (e.project ?? '') === (s.project ?? ''))?.scope ?? 'user' } }, secondary: { label: 'Keep', op: 'dismiss', args: {} }, source: 'Usage' });

  const stale = group(f('memory-never-recalled'), (x) => (x.project ? x.project : GLOBAL));
  for (const [proj, xs] of stale) {
    const ms = xs.map((x) => m.byId.get(x.path)).filter((x): x is Mem => !!x);
    out.push({ id: 'stale:' + proj, tone: 'del', icon: '⌫', title: `${plural(ms.length, 'memory', 'memories')} in ${m.projectOf(proj).label} ${ms.length === 1 ? 'was' : 'were'} never opened in 90 days`,
      body: 'Claude saw them in the index but never read them. Tick the ones that are no longer true and move them to the Trash (undo brings them back), or open one to check it.',
      targets: ms.map((x) => ({ path: x.path, label: x.title, mem: x })), pick: 'none',
      primary: { label: 'Move to Trash', op: 'bulk', args: { action: 'trash', paths: [] }, danger: true }, secondary: { label: 'Not now', op: 'dismiss', args: {} }, source: 'Usage' });
  }

  for (const i of [...f('index-near-cap'), ...f('index-over-cap')]) {
    const e = m.state.entries.find((x) => x.path === i.path);
    out.push({ id: 'cap:' + i.path, tone: i.code === 'index-over-cap' ? 'del' : 'warn', icon: '!',
      title: `Memory index for ${m.projectOf(i.project ?? GLOBAL).label} is about ${Math.round(((e?.lines ?? 0) / 200) * 100)}% full`,
      body: 'Claude reads only the first 200 lines of MEMORY.md. Archive or merge memories before new ones become invisible.', paths: [i.path],
      primary: { label: 'Show oldest', op: 'filter-project', args: { project: i.project } }, secondary: { label: 'Not now', op: 'dismiss', args: {} }, source: 'Built-in check' });
  }

  // Same memory written in two projects: offer one shared copy.
  const seen = new Set<string>();
  for (let i = 0; i < m.mems.length; i++) for (let j = i + 1; j < m.mems.length; j++) {
    const a = m.mems[i], b = m.mems[j];
    if (a.project === b.project || a.type !== b.type || seen.has(a.id) || seen.has(b.id) || similarity(a.stem, b.stem) < 0.75) continue;
    seen.add(a.id); seen.add(b.id);
    out.push({ id: `dup:${a.id}|${b.id}`, tone: 'warn', icon: '⇄', title: 'These two look like the same memory',
      body: `One is in ${m.projectOf(a.project).label}, the other in ${m.projectOf(b.project).label}. Merging keeps one copy in Everywhere.`, mems: [a, b],
      primary: { label: 'Merge into Everywhere', op: 'merge-global', args: { keep: a.path, drop: b.path } }, secondary: { label: 'Keep both', op: 'dismiss', args: {} }, source: 'Built-in check' });
  }
  return out;
}

// Every project copy of each copied skill/command (the finding names only the first).
function copyPaths(m: Model, copies: Finding[]): string[] {
  const names = new Set(copies.map((c) => (c.detail ?? '').split(' in ')[0]));
  return m.state.entries.filter((e) => (e.kind === 'skill' || e.kind === 'command') && e.scope === 'project' && names.has(`${e.kind} ${e.name}`)).map((e) => e.path ?? '');
}

// ---------- Toolkit ----------

export interface Row {
  key: string; name: string; desc: string; source: string; scope: string; project?: string;
  enabled: boolean; readonly: boolean; canToggle?: boolean; uses: number; last: string; tokens?: number; tokensLabel?: string;
  tags: string[]; warn: string[]; entry?: Entry;
}

const sumUsage = (u: Record<string, Stat>, match: (k: string) => boolean): [number, string] => {
  let c = 0, l = '';
  for (const [k, v] of Object.entries(u)) if (match(k)) { c += v.count; if (v.last > l) l = v.last; }
  return [c, l.slice(0, 10)];
};

export function plugins(m: Model): Row[] {
  const skillsOf = new Map<string, string[]>();
  for (const s of m.entries('skill')) if (s.scope === 'plugin') {
    const p = String(s.meta?.plugin ?? '');
    skillsOf.set(p, [...(skillsOf.get(p) ?? []), s.name]);
  }
  return m.entries('plugin').map((p) => {
    const own = new Set(skillsOf.get(p.name) ?? []);
    const [uses, last] = sumUsage(m.usage, (k) => k.startsWith(`skill:${p.name}:`) || k.startsWith(`mcp:plugin_${p.name}_`) || own.has(k.slice(6)));
    const comps = (p.meta?.components ?? {}) as Record<string, number>;
    const cost = Number(p.meta?.alwaysOnTokens ?? 0);
    const warn: string[] = [];
    if ((p.issues ?? []).includes('plugin-installed-twice')) warn.push('installed twice');
    return { key: String(p.meta?.id), name: p.name, desc: p.description ?? '', source: String(p.meta?.marketplace ?? ''), scope: 'user',
      enabled: p.enabled, readonly: false, canToggle: true, uses, last, tokens: cost, tokensLabel: 'every session',
      tags: Object.entries(comps).filter(([, v]) => v).map(([k, v]) => `${v} ${v === 1 ? k.replace(/s$/, '') : k}`), warn, entry: p };
  }).sort((a, b) => (b.tokens ?? 0) - (a.tokens ?? 0));
}

export function mcpServers(m: Model): Row[] {
  const rows: Row[] = m.entries('mcp').map((e) => {
    const [uses, last] = sumUsage(m.usage, (k) => k === 'mcp:' + e.name);
    const env = (e.meta?.envKeys ?? []) as string[];
    return { key: `${e.scope}:${e.project ?? ''}:${e.name}`, name: e.name, desc: String(e.meta?.url ?? e.meta?.command ?? ''),
      source: e.scope === 'user' ? 'Yours · everywhere' : e.scope === 'local' ? `Only you · ${base(e.project ?? '')}` : `Project · ${base(e.project ?? '')}`,
      scope: e.scope, project: e.project, enabled: e.enabled, readonly: false, uses, last,
      tags: [String(e.meta?.transport ?? ''), ...env.map((k) => k + ' ••••••')], warn: [], entry: e };
  });
  const known = new Set(rows.map((r) => r.name));
  for (const [k, v] of Object.entries(m.usage)) {
    if (!k.startsWith('mcp:')) continue;
    const n = k.slice(4);
    if (known.has(n)) continue;
    const [source, name] = n.startsWith('plugin_') ? ['From plugin · ' + n.split('_')[1], n.split('_').slice(2).join('_') || n.split('_')[1]]
      : n.startsWith('claude_ai_') ? ['claude.ai connector', n.slice(10).replace(/_/g, ' ')] : ['Built into Claude Code', n];
    rows.push({ key: k, name, desc: '', source, scope: 'external', enabled: true, readonly: true, uses: v.count, last: v.last.slice(0, 10), tags: [], warn: [] });
  }
  return rows.sort((a, b) => b.uses - a.uses);
}

export function skills(m: Model): Row[] {
  const copies = new Map<string, Set<string>>();
  const mine = [...m.entries('skill'), ...m.entries('command')].filter((e) => e.scope !== 'plugin');
  for (const e of mine) if (e.scope === 'project') copies.set(`${e.kind}:${e.name}`, new Set([...(copies.get(`${e.kind}:${e.name}`) ?? []), e.project ?? '']));
  return mine.map((e) => {
    const [uses, last] = sumUsage(m.usage, (k) => k === 'skill:' + e.name || k.endsWith(':' + e.name) && e.kind === 'command');
    const n = copies.get(`${e.kind}:${e.name}`)?.size ?? 0;
    return { key: e.path ?? e.name, name: e.kind === 'command' ? '/' + e.name : e.name, desc: e.description ?? '', source: e.scope === 'user' ? 'Everywhere' : base(e.project ?? ''),
      scope: e.scope, project: e.project, enabled: e.enabled, readonly: false, uses, last,
      tokens: Math.round((e.bytes ?? 0) / 4), tokensLabel: 'when used',
      tags: e.kind === 'command' ? ['command'] : [], warn: e.scope === 'project' && n > 1 ? [`copied in ${n} projects`] : [], entry: e };
  });
}

export function agents(m: Model): Row[] {
  const rows: Row[] = m.entries('agent').map((e) => {
    const [uses, last] = sumUsage(m.usage, (k) => k === 'agent:' + e.name);
    return { key: e.path ?? e.name, name: e.name, desc: e.description ?? '',
      source: e.scope === 'plugin' ? 'From plugin · ' + String(e.meta?.plugin) : e.scope === 'user' ? 'Everywhere' : base(e.project ?? ''),
      scope: e.scope, enabled: e.scope === 'plugin' ? e.meta?.pluginEnabled !== false : e.enabled, readonly: e.scope === 'plugin', uses, last, tags: [], warn: [], entry: e };
  });
  const known = new Set(rows.map((r) => r.name));
  for (const [k, v] of Object.entries(m.usage)) if (k.startsWith('agent:') && !known.has(k.slice(6))) {
    rows.push({ key: k, name: k.slice(6), desc: '', source: 'Built into Claude Code', scope: 'external', enabled: true, readonly: true,
      uses: v.count, last: v.last.slice(0, 10), tags: [], warn: [] });
  }
  return rows.sort((a, b) => b.uses - a.uses);
}
