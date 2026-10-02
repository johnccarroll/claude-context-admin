import './styles.css';
import { act, decide, loadActivity, prefsSaves, rescan, savePrefs, type Activity, loadBudget, loadFile, loadPrefs, loadState, loadVersions, previewCaps, reveal, subscribe, undo, type Version } from './api';
import { checkbox, clearSelection, renderSelectionBar, selected, wireCheckboxes } from './bulk';
import { select } from 'd3-selection';
import 'd3-transition'; // .transition() on the expanded graph's zoom
import { zoom as d3zoom } from 'd3-zoom';
import { diffLines, hunks } from './diff';
import { composeTool, richText, splitDoc, splitTool, type ToolDoc } from './doc';
import { openPalette } from './palette';
import { drawMap, markMap } from './map';
import { openAdd } from './add';
import {
  agents, buildModel, buildReview, closest, dirOf, pluginState, human, KHELP, LINK, linkKey, KIND, mcpServers, PLURAL, plugins, skills,
  type Action, type Mem, type Model, type ReviewItem, type Row,
} from './model';
import { renderProjects } from './sidebar';
import { GLOBAL } from './model';
import type { Entry, Prefs, Source } from './types';
import { $, $$, ago, copyText, cssVar, esc, fmtDate, fmtN, menu, platform, toast, toggle, usageCell, usageState, wireToast, type MenuItem } from './ui';

type View = 'all' | 'review' | 'map' | 'activity' | 'ins' | 'what' | 'plugins' | 'mcp' | 'skills' | 'agents' | 'hooks';
const VIEWS: View[] = ['all', 'review', 'map', 'activity', 'ins', 'what', 'plugins', 'mcp', 'skills', 'agents', 'hooks'];

const ui = {
  view: 'all' as View, proj: '' as string, kind: '' as string, layer: '' as string, rcat: '', q: '',
  open: null as Mem | null, file: null as string | null, showDismissed: false,
};
let model: Model;
let prefs: Prefs = { projects: {}, order: [] };
let review: ReviewItem[] = [];

// ---------- data ----------

async function refresh(): Promise<void> {
  const before = prefsSaves();
  const [state, p] = await Promise.all([loadState(), loadPrefs().catch(() => prefs)]);
  // Update the one prefs object in place: the sidebar's handlers hold it, and replacing it would
  // leave them changing (and saving) a stale copy. A save that started meanwhile wins.
  if (prefsSaves() === before && before < 1e9) Object.assign(prefs, { projects: p.projects ?? {}, order: p.order ?? [], dismissed: p.dismissed, drawerWidth: p.drawerWidth });
  if (!document.body.classList.contains('resizing')) setDrawerWidth(prefs.drawerWidth ?? DW);
  platform.show = state.os === 'darwin' ? 'Show in Finder' : 'Show in folder';
  model = buildModel(state, prefs);
  review = buildReview(model).filter((r) => r.id !== 'connect' || !connectLater());
  renderPluginStatus();
  $('#watch').textContent = state.readOnly ? 'Read-only · watching ~/.claude' : 'Watching ~/.claude';
  $('.foot .live').classList.toggle('ro', state.readOnly);
  if (ui.open) {
    ui.open = model.byId.get(ui.open.id) ?? null;
    if (!ui.open) closeDrawer(); // renamed or deleted: its old path is gone
  }
  render();
  // Never rebuild a drawer the user is typing in. If the file changed underneath, Save reports it.
  const tab = (document.querySelector<HTMLElement>('#drawer .dtabs [aria-selected="true"]')?.dataset.tab ?? 'edit') as 'edit' | 'history';
  if (ui.open && !dirty) openMemory(ui.open, tab);
  if (ui.file) {
    const f = model.state.entries.find((e) => e.path === ui.file);
    if (!f) closeDrawer(); // converted, trashed or renamed
    else if (!dirty) openFile(f, tab);
  }
}

/** Set when the user types in the open drawer; cleared when a drawer opens or a change succeeds. */
let dirty = false;
/** Marks the open drawer as having unsaved edits (Save shows a dot) or not. */
function setDirty(v: boolean): void { dirty = v; $('#drawer').classList.toggle('dirty', v); }
const drawerOpen = (): boolean => $('#drawer').classList.contains('open');

/** Runs then() now, or after the user agrees to drop unsaved edits in the open drawer. */
function leave(then: () => void): void {
  if (!dirty || !drawerOpen()) { then(); return; }
  const d = $('#drawer');
  d.querySelector('.leavebar')?.remove();
  d.insertAdjacentHTML('beforeend', '<div class="leavebar" role="alertdialog" aria-label="Unsaved changes"><span>You have unsaved changes.</span><button class="btn sm" id="lkeep">Keep editing</button><button class="btn sm dng" id="ldrop">Discard</button></div>');
  const focus = document.activeElement as HTMLElement | null;
  $('#lkeep').onclick = () => {
    d.querySelector('.leavebar')?.remove();
    history.replaceState({ n: navPos }, '', hashNow()); // Back already changed the address: put it back
    focus?.focus();
  };
  $('#ldrop').onclick = () => { setDirty(false); d.querySelector('.leavebar')?.remove(); then(); };
  $<HTMLButtonElement>('#lkeep').focus();
}
addEventListener('beforeunload', (e) => { if (dirty && drawerOpen()) e.preventDefault(); });
const watchEdits = (d: HTMLElement): void => { setDirty(false); d.oninput = () => setDirty(true); };

/** The ops an open editor sends to save itself; any other change leaves its unsaved text alone. */
const SAVES = new Set(['memory-save', 'file-save', 'memory-create', 'restore']);

async function run(op: string, args: Record<string, unknown>, done?: string): Promise<boolean> {
  if (model.state.readOnly) { toast('Read-only mode: start cca without --read-only to make changes.'); return false; }
  const r = await act(op, args);
  if (r.ok) { if (SAVES.has(op)) setDirty(false); await refresh(); }
  const id = r.activity;
  toast(r.ok ? done ?? r.message : r.message, r.ok && r.canUndo && id ? () => void undoChange(id) : undefined);
  return r.ok;
}

async function undoChange(id: string): Promise<void> {
  const r = await undo(id);
  if (r.ok) await refresh(); // what was fixed shows again (e.g. its Review card) right away
  toast(r.ok ? 'Undone.' : r.message);
  if (ui.view === 'activity') void renderActivity();
}

// ---------- shell ----------

function setView(v: View): void {
  if (v !== ui.view && dirty && drawerOpen()) { leave(() => setView(v)); return; }
  if (v !== ui.view && $('#drawer').classList.contains('open')) closeDrawer(false); // the page we leave keeps its open drawer in history
  ui.view = v;
  if (v !== 'all') clearSelection();
  $$('#nav button, #nav2 button, #mnav button').forEach((b) => b.setAttribute('aria-current', String(b.dataset.v === v)));
  document.querySelector<HTMLElement>(`#mnav button[data-v="${v}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' }); // phones: keep the current page in view
  for (const x of VIEWS) $('#v-' + x).hidden = x !== v;
  $('#searchwrap').hidden = v !== 'all' && v !== 'map';
  $('#newmem').hidden = v !== 'all' && v !== 'map';
  const add = ({ mcp: '+ Add MCP server', plugins: '+ Add plugin', skills: '+ New skill' } as Partial<Record<View, string>>)[v];
  $('#addbtn').hidden = !add;
  $('#hacts').innerHTML = ''; // page actions; a page that has some fills it on render
  $('#addbtn').textContent = add ?? '';
  render();
}

function renderHealth(): void {
  document.querySelector('.healthbar')?.remove();
  const bad = model.state.health ?? [];
  usageState.broken = bad.some((c) => c.name === 'Usage stats' && c.status === 'fail');
  const fails = bad.filter((c) => c.status === 'fail');
  if (!fails.length) return;
  const el = document.createElement('div');
  el.className = 'banner warn healthbar';
  el.style.margin = '12px 24px 0';
  el.innerHTML = `<span class="ic2">!</span><div><b>${fails.length === 1 ? 'Part of this app is' : `${fails.length} parts of this app are`} unavailable on your Claude Code version</b>${fails.map((c) => `${esc(c.name)}: ${esc(c.detail)}.`).join('<br>')} Everything else works; an update to Claude Context Admin will fix this.</div>`;
  $('.top').after(el);
}

function render(): void {
  renderHealth();
  renderSide();
  renderChips();
  if (ui.view === 'all') renderAll();
  else (VIEW_RENDER[ui.view] ?? (() => {}))();
  syncURL();
}

// ---------- address ----------
// The page, scope and open item live in the address (#map?p=<project>&m=<memory>), so reload,
// back/forward and bookmarks work. A new page or scope is a history entry; opening an item isn't.

let applying = true; // until the address has been read once at startup

function hashNow(): string {
  const q = new URLSearchParams();
  if (ui.proj) q.set('p', ui.proj);
  // Filters too, so Back returns to exactly what you were looking at.
  if (ui.layer) q.set('l', ui.layer);
  if (ui.kind) q.set('k', ui.kind);
  if (ui.rcat && ui.view === 'review') q.set('c', ui.rcat);
  if (ui.q && (ui.view === 'all' || ui.view === 'map')) q.set('q', ui.q);
  if (ui.open) q.set('m', ui.open.id);
  else if (ui.file) q.set('f', ui.file);
  return '#' + (ui.view === 'all' ? 'memories' : ui.view) + (q.size ? '?' + q : '');
}

function syncURL(): void {
  if (applying || !model) return;
  const hash = hashNow();
  if (hash === location.hash) return;
  const [cur, curQ] = [location.hash.slice(1).split('?')[0], new URLSearchParams(location.hash.split('?')[1] ?? '')];
  const mem = ui.open?.id ?? '', was = curQ.get('m') ?? '';
  // A new page, or one memory to another (following a link): both are steps Back returns to.
  const moved = cur !== hash.slice(1).split('?')[0] || (curQ.get('p') ?? '') !== ui.proj || (!!was && !!mem && was !== mem);
  if (moved) {
    navPos += 1;
    navMax = navPos; // a new page drops anything ahead
    history.pushState({ n: navPos }, '', hash);
  } else {
    history.replaceState({ n: navPos }, '', hash);
  }
  navMem[navPos] = mem;
  navButtons();
}

// Back and forward: each history entry records its position, so the buttons know when there is
// somewhere to go. An entry without one was made by setting location.hash (the Mac app's menus).
let navPos = 0, navMax = 0;
/** The memory open at each history position, so the panel's arrows step between memories only. */
const navMem: string[] = [];
function navButtons(): void {
  $<HTMLButtonElement>('#navback').disabled = navPos <= 0;
  $<HTMLButtonElement>('#navfwd').disabled = navPos >= navMax;
  const b = document.getElementById('dback') as HTMLButtonElement | null, f = document.getElementById('dfwd') as HTMLButtonElement | null;
  if (b) b.disabled = !(navPos > 0 && navMem[navPos - 1]);
  if (f) f.disabled = !(navPos < navMax && navMem[navPos + 1]);
}
function navArrived(state: unknown): void {
  const n = (state as { n?: number } | null)?.n;
  if (typeof n === 'number') navPos = n;
  else { navPos += 1; navMax = navPos; navMem[navPos] = ''; history.replaceState({ n: navPos }, '', location.hash); }
  navButtons();
}

function applyURL(): void {
  const [name, query] = location.hash.slice(1).split('?');
  const q = new URLSearchParams(query ?? '');
  const v = (name === 'memories' || !name ? 'all' : name) as View;
  applying = true;
  try {
    ui.proj = q.get('p') ?? '';
    ui.layer = q.get('l') ?? '';
    ui.kind = q.get('k') ?? '';
    ui.rcat = q.get('c') ?? '';
    ui.q = (q.get('q') ?? '').toLowerCase();
    ($('#q') as HTMLInputElement).value = ui.q;
    if (drawerOpen() && !q.get('m') && !q.get('f')) closeDrawer();
    setView(VIEWS.includes(v) ? v : 'all');
    const m = q.get('m') ? model.byId.get(q.get('m')!) : undefined;
    const f = q.get('f') ? model.state.entries.find((e) => e.path === q.get('f')) : undefined;
    if (m && ui.open?.id !== m.id) openMemory(m);
    else if (f && ui.file !== f.path) openFile(f);
  } finally {
    applying = false;
  }
}
addEventListener('popstate', (e) => { navArrived(e.state); if (model) leave(applyURL); });
history.replaceState({ n: 0 }, '', location.hash || location.pathname);
$('#navback').onclick = () => history.back();
$('#navfwd').onclick = () => history.forward();

// ---------- scope ----------
// The sidebar picks one scope for every page: all projects (''), Everywhere (GLOBAL), or one
// project. A project shows what Claude loads there: its own things plus everything from the
// Everywhere, plugin and built-in layers. Layer chips narrow a page to one layer.

type Layer = 'user' | 'project' | 'local' | 'plugin' | 'external';
const LAYER: Record<Layer, [string, string]> = {
  user: ['Everywhere', 'Yours, in every project'], project: ['Project', 'In the repo, shared with anyone who clones it'],
  local: ['Only you', 'This project, on this computer only'], plugin: ['Plugins', 'Comes with an installed plugin'], external: ['Built in', 'Claude Code or claude.ai'],
};
const layerOf = (scope: string): Layer => (scope === 'import' ? 'project' : scope in LAYER ? scope : 'user') as Layer;
const memLayer = (m: Mem): Layer => (m.project === GLOBAL ? 'user' : 'project');
const shown = (key: string): boolean => ui.proj === key || !model.projectOf(key).hidden;

/** Whether something in this layer (and project) applies in the current scope, before layer chips. */
const inScope = (layer: Layer, project?: string): boolean => {
  if (!ui.proj) return !project || shown(project);
  return layer === 'project' || layer === 'local' ? project === ui.proj : true;
};
const pass = (layer: Layer, project?: string): boolean => inScope(layer, project) && (!ui.layer || ui.layer === layer);
const scoped = (rows: Row[]): Row[] => rows.filter((r) => pass(layerOf(r.scope), r.project));

/** Whether a memory passes the scope, layer, kind and filter (the list and the map agree). */
const memShown = (m: Mem): boolean => pass(memLayer(m), m.project) && (!ui.kind || m.type === ui.kind) &&
  (!ui.q || (m.title + ' ' + m.desc).toLowerCase().includes(ui.q));
const visibleMems = (): Mem[] => model.mems.filter(memShown);

function renderSide(): void {
  const counts = new Map<string, number>();
  for (const m of model.mems) counts.set(m.project, (counts.get(m.project) ?? 0) + 1);
  for (const e of model.state.entries) if (e.project && !counts.has(e.project)) counts.set(e.project, 0); // has tools or instructions only
  renderProjects(model, prefs, counts, {
    current: () => ui.proj,
    select: (key) => { ui.proj = key; ui.layer = ''; render(); }, // scope applies to every page; stay on this one
    changed: () => { model = buildModel(model.state, prefs); render(); },
    relocate: relocateMenu,
  });
  const ms = $<HTMLSelectElement>('#mscope'); // phones have no sidebar: the same scope as a picker
  ms.innerHTML = `<option value="">All projects</option>` + model.projects.filter((p) => counts.has(p.key) && (!p.hidden || p.key === ui.proj))
    .map((p) => `<option value="${esc(p.key)}" ${p.key === ui.proj ? 'selected' : ''}>${esc(p.label)}</option>`).join('');
  ms.onchange = () => { ui.proj = ms.value; ui.layer = ''; render(); };
  const open = openReview().length;
  const badge = $('#rvcount');
  badge.textContent = open ? String(open) : '';
  badge.hidden = !open;
  const n = (k: string, v: number): void => { $('#c-' + k).textContent = String(v); };
  n('plugins', model.entries('plugin').length);
  n('mcp', mcpServers(model).length);
  n('skills', skills(model).length);
  n('agents', agents(model).length);
  n('hooks', model.entries('hook').length);
}

function heading(title: string, sub: string): void {
  $('#h1').textContent = title;
  const p = ui.proj ? model.projectOf(ui.proj) : null;
  // The project chip sits beside the title; the subtitle line keeps only text.
  $('#scope').innerHTML = p ? `<button class="scopepill" id="scopex" title="Show all projects"><span class="dot" style="background:${p.color}"></span>${esc(p.label)}<span aria-hidden="true">×</span></button>` : '';
  $('#sub').textContent = sub;
  const x = document.getElementById('scopex');
  if (x) x.onclick = () => { ui.proj = ''; ui.layer = ''; render(); };
}

// ---------- chips ----------

/** Every in-scope item on the current page, with its layer (and kind, for memories). */
function pageItems(): { layer: Layer; kind: string }[] {
  type It = { layer: Layer; project?: string; kind?: string };
  const rows = (rs: Row[]): It[] => rs.map((r) => ({ layer: layerOf(r.scope), project: r.project }));
  const ents = (es: Entry[]): It[] => es.map((e) => ({ layer: layerOf(e.scope), project: e.project }));
  const mems = (): It[] => model.mems.map((m) => ({ layer: memLayer(m), project: m.project, kind: m.type }));
  const all: It[] = ({
    all: mems, map: mems,
    mcp: () => rows(mcpServers(model)), skills: () => rows(skills(model)), agents: () => rows(agents(model)),
    hooks: () => ents(model.entries('hook')),
    ins: () => ents(model.state.entries.filter((e) => (e.kind === 'instructions' || e.kind === 'rule') && e.scope !== 'import')),
  } as Partial<Record<View, () => It[]>>)[ui.view]?.() ?? [];
  return all.filter((x) => inScope(x.layer, x.project)).map((x) => ({ layer: x.layer, kind: x.kind ?? '' }));
}

function renderChips(): void {
  if (ui.view === 'review') { // categories instead of layers
    const open = openReview();
    const n = new Map<string, number>();
    for (const r of open) n.set(rcat(r), (n.get(rcat(r)) ?? 0) + 1);
    if (ui.rcat && !n.has(ui.rcat)) ui.rcat = '';
    const cats = [...RCAT.map(([c]) => c), 'Other'].filter((c) => n.has(c));
    $('#chips').innerHTML = cats.length > 1 ? `<button class="chip" data-c="" aria-pressed="${!ui.rcat}">All · ${open.length}</button>` +
      cats.map((c) => `<button class="chip" data-c="${esc(c)}" aria-pressed="${ui.rcat === c}">${esc(c)} · ${n.get(c)}</button>`).join('') : '';
    $('#chips').hidden = cats.length < 2;
    $$('#chips .chip').forEach((b) => (b.onclick = () => { ui.rcat = b.dataset.c ?? ''; render(); }));
    return;
  }
  const items = pageItems();
  const byLayer = new Map<Layer, number>();
  for (const x of items) byLayer.set(x.layer, (byLayer.get(x.layer) ?? 0) + 1);
  if (ui.layer && !byLayer.has(ui.layer as Layer)) ui.layer = '';
  const order = Object.keys(LAYER);
  const layers = [...byLayer].sort((a, b) => order.indexOf(a[0]) - order.indexOf(b[0]));
  const name = (l: Layer): string => (l === 'project' && ui.proj && ui.proj !== GLOBAL ? model.projectOf(ui.proj).label : LAYER[l][0]);
  const layerChips = layers.length > 1
    ? `<button class="chip" data-l="" aria-pressed="${!ui.layer}">All · ${items.length}</button>` +
      layers.map(([l, n]) => `<button class="chip" data-l="${l}" aria-pressed="${ui.layer === l}" title="${esc(LAYER[l][1])}">${esc(name(l))} · ${n}</button>`).join('')
    : '';
  let kindChips = '';
  if ((ui.view === 'all' || ui.view === 'map') && items.length) {
    const c = new Map<string, number>();
    for (const x of items) if (!ui.layer || x.layer === ui.layer) c.set(x.kind, (c.get(x.kind) ?? 0) + 1);
    if (ui.kind && !c.has(ui.kind)) ui.kind = '';
    kindChips = `<button class="chip" data-k="" aria-pressed="${!ui.kind}">Any kind</button>` +
      Object.keys(KIND).filter((k) => c.get(k)).map((k) => `<button class="chip" data-k="${k}" aria-pressed="${ui.kind === k}" title="${KHELP[k]}">${PLURAL[k]} · ${c.get(k)}</button>`).join('');
  }
  $('#chips').innerHTML = layerChips + (layerChips && kindChips ? '<span class="chipsep"></span>' : '') + kindChips;
  $('#chips').hidden = !layerChips && !kindChips;
  $$('#chips .chip').forEach((b) => (b.onclick = () => {
    if (b.dataset.l !== undefined) ui.layer = b.dataset.l; else ui.kind = b.dataset.k ?? '';
    render();
  }));
}

const linkIcon = '<svg width="13" height="13" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="5" cy="10" r="2.5"/><circle cx="15" cy="5" r="2.5"/><circle cx="15" cy="15" r="2.5"/><path d="M7.3 9l5.4-3M7.3 11l5.4 3"/></svg>';

function rowHTML(m: Mem): string {
  const issue = m.badYaml || m.missing.length;
  return `<div class="row ${selected.has(m.id) ? 'picked' : ''}" tabindex="0" data-id="${esc(m.id)}" aria-selected="${ui.open?.id === m.id}">${checkbox(m.id, m.title)}
    <div class="t">${esc(m.title)}</div><div class="d">${esc(m.desc || KHELP[m.type] || '')}</div>
    <div class="m">${issue ? '<span class="flag" title="Needs review"></span>' : ''}<span class="kind">${esc(KIND[m.type] ?? m.type)}</span>
    <span class="links-n" title="Connections">${linkIcon}${m.out.length + m.inn.length}</span>
    <span style="width:44px;text-align:right">${esc(fmtDate(m.modified))}</span></div></div>`;
}

/** The one empty state every page uses: what belongs here, and what to do about it. */
function emptyHTML(title: string, body: string, action = ''): string {
  return `<div class="empty"><b class="etitle">${esc(title)}</b>${esc(body)}${action ? `<div class="eact">${action}</div>` : ''}</div>`;
}
const addButton = (label: string): string => `<button class="btn" data-addbtn>${esc(label)}</button>`;
function wireEmpty(root: string): void {
  document.querySelector<HTMLButtonElement>(`${root} [data-addbtn]`)?.addEventListener('click', () => $('#addbtn').click());
}

function renderAll(): void {
  const list = visibleMems();
  const filtered = !!(ui.q || ui.layer || ui.kind);
  heading('Memories', `${list.length} ${filtered ? (list.length === 1 ? 'matches' : 'match') : ui.proj && ui.proj !== GLOBAL ? 'load in this project' : 'memories'}`);
  if (!model.mems.length) {
    $('#v-all').innerHTML = emptyHTML('No memories yet', 'Claude Code saves memories on its own as you work: your preferences, decisions and how things are set up. They appear here as it writes them. You can also add one now.', '<button class="btn" id="firstmem">+ New memory</button>');
    $('#firstmem').onclick = openNewMemory;
    return;
  }
  if (!list.length) {
    $('#v-all').innerHTML = emptyHTML('No memories match', 'Nothing here fits the search and filters.', '<button class="btn" id="clearf">Clear filters</button>');
    $('#clearf').onclick = () => { ui.q = ''; ui.layer = ''; ui.kind = ''; ($('#q') as HTMLInputElement).value = ''; render(); };
    return;
  }
  $('#v-all').innerHTML = model.projects.map((p) => {
    const g = list.filter((m) => m.project === p.key).sort((a, b) => b.modified.localeCompare(a.modified));
    if (!g.length) return '';
    return `<div class="group"><div class="ghead"><span class="dot" style="background:${p.color}"></span>${esc(p.label)}<span class="n">${g.length}</span></div>${g.map(rowHTML).join('')}</div>`;
  }).join('');
  const bulkHooks = { rerender: () => render(), memoryDir: memoryDirFor, bulk: (action: string, extra: Record<string, unknown> = {}) => {
    const paths = [...selected].map((id) => model.byId.get(id)?.path).filter(Boolean);
    clearSelection();
    void run('bulk', { action, paths, ...extra });
    render();
  } };
  wireCheckboxes($('#v-all'), bulkHooks);
  renderSelectionBar(model, list.map((m) => m.id), bulkHooks);
  $$('#v-all .row').forEach((r) => {
    const m = model.byId.get(r.dataset.id ?? '');
    if (!m) return;
    r.onclick = () => openMemory(m);
    r.onkeydown = (e) => { if (e.key === 'Enter') openMemory(m); };
    r.oncontextmenu = (e) => { e.preventDefault(); menu(e, memoryMenu(m)); };
  });
}

// ---------- connect Claude ----------

const CONNECT = 'claude plugin marketplace add johnccarroll/claude-context-admin\nclaude plugin install context-admin@context-admin';
const connectLater = (): boolean => { try { return localStorage.getItem('cca-connect-later') === '1'; } catch { return false; } };

function renderPluginStatus(): void {
  const st = pluginState(model);
  $('#plugstat').innerHTML = st === 'on' ? '<span class="ok2">●</span> Connected: Claude can see this and suggest changes'
    : st === 'missing' ? 'Not installed. <button class="link2" id="pcopy">Copy install commands</button>'
    : 'Installed but off. Turn it on under Plugins.';
  const c = document.getElementById('pcopy');
  if (c) c.onclick = () => void copyText(CONNECT, 'Copied. Paste them in a terminal, then start a new Claude session.');
}

// ---------- review ----------

/** Review categories, for the chips above the cards. */
const RCAT: [string, RegExp][] = [['From Claude', /^proposal:/], ['Links', /^link:/], ['Duplicates', /^(dup:|copies|twice:)/],
  ['Instructions', /^(health:|ignored:|convert:|yaml:)/], ['Unused', /^(unused:|mcp:|stale:)/]];
const rcat = (r: ReviewItem): string => RCAT.find(([, re]) => re.test(r.id))?.[0] ?? 'Other';

/** Checked targets per Review card, kept across re-renders. */
const picks = new Map<string, Set<string>>();

function picked(r: ReviewItem): Set<string> | null {
  if (!r.targets) return null;
  if (r.targets.length === 1) return new Set([r.targets[0].path]); // one item: nothing to choose
  const cur = new Set(r.targets.map((t) => t.path));
  let sel = picks.get(r.id);
  let fixed = 0;
  if (sel) for (const p of [...sel]) if (!cur.has(p)) { sel.delete(p); fixed++; } // fixed ones drop out
  // A new card starts with its default; after a fix took every ticked item, tick the rest again.
  // Unticking everything yourself stays unticked.
  if (!sel || (fixed && !sel.size && r.pick !== 'none')) sel = new Set(r.pick === 'none' ? [] : cur);
  picks.set(r.id, sel);
  return sel;
}

/** A card's projects, so Review follows the sidebar scope. Cards about no project always show. */
function reviewProjects(r: ReviewItem): string[] {
  const of = (p: string): string | undefined => model.state.entries.find((e) => e.path === p)?.project ?? undefined;
  return [...(r.mems ?? []).map((m) => m.project), ...(r.targets ?? []).map((t) => t.mem?.project ?? of(t.path)),
    r.finding?.project, ...(r.paths ?? []).map(of)].filter((x): x is string => !!x);
}

const dismissedIds = (): Set<string> => new Set(prefs.dismissed ?? []);
function setDismissed(id: string, on: boolean): void {
  prefs.dismissed = [...(prefs.dismissed ?? []).filter((x) => x !== id), ...(on ? [id] : [])];
  void savePrefs(prefs).catch((e: Error) => toast(e.message));
}
const reviewInScope = (r: ReviewItem): boolean => {
  if (!ui.proj) return true;
  const ps = reviewProjects(r);
  return !ps.length || ps.includes(ui.proj) || ps.includes(GLOBAL);
};
/** Review cards for the current scope, without the dismissed ones (the sidebar badge counts these). */
const openReview = (): ReviewItem[] => { const d = dismissedIds(); return review.filter((r) => !d.has(r.id) && reviewInScope(r)); };

function renderReview(): void {
  const d = dismissedIds();
  const scoped = review.filter(reviewInScope);
  const open = scoped.filter((r) => !d.has(r.id) && (!ui.rcat || rcat(r) === ui.rcat));
  const hidden = scoped.filter((r) => d.has(r.id));
  const total = scoped.length - hidden.length;
  heading('Review', `${ui.rcat ? `${open.length} of ` : ''}${total} suggestion${total === 1 ? '' : 's'}${hidden.length ? ` · ${hidden.length} dismissed` : ''}`);
  const pill = (m: Mem): string => `<span class="pill" data-id="${esc(m.id)}">${esc(m.title)}</span>`;
  const label = (r: ReviewItem, a: Action): string => {
    const sel = picked(r);
    return sel && sel.size && Array.isArray(a.args.paths) && sel.size !== r.targets?.length ? `${a.label} (${sel.size})` : a.label;
  };
  const off = (r: ReviewItem, a: Action): string => (picked(r)?.size === 0 && Array.isArray(a.args.paths) ? 'disabled' : '');
  const targets = (r: ReviewItem): string => {
    const sel = picked(r)!;
    const rows = r.targets!.map((t) => `<span class="tgt"><input type="checkbox" data-t="${esc(t.path)}" ${sel.has(t.path) ? 'checked' : ''} aria-label="Include ${esc(t.label)}">${t.mem ? `<button class="pill" data-id="${esc(t.mem.id)}">${esc(t.label)}</button>` : `<span class="pill">${esc(t.label)}</span>`}</span>`).join('');
    const bulk = r.targets!.length > 2 ? `<span class="tgtall"><button class="link2" data-all="1">All</button><button class="link2" data-all="0">None</button></span>` : '';
    return `<div class="tpanel"><div class="tlabel">Applies to <b>${sel.size}</b> of ${r.targets!.length}${bulk}</div><div class="tgts">${rows}</div></div>`;
  };
  // Three zones: what it is (header), why and what to (body), what you can do (action bar,
  // main action rightmost).
  const card = (r: ReviewItem, dismissed = false): string => `<div class="card ${dismissed ? 'dim' : ''}" data-r="${esc(r.id)}">
    <div class="chead"><div class="ic ${r.tone}">${esc(r.icon)}</div><h3>${esc(r.title)}</h3><span class="src">${esc(r.source)}</span></div>
    <div class="cbody"><p>${esc(r.body)}</p>${r.finding ? evidenceHTML(r) : ''}${r.preview ? `${r.preview.split('\n').length > 18 ? `<div class="hint">${r.preview.split('\n').length} lines: scroll to read all of it before accepting.</div>` : ''}<div class="diff2 preview">${esc(r.preview).split('\n').map((l) => `<div class="a">${l || ' '}</div>`).join('')}</div>` : ''}${r.previewBody ? `<div class="hint" style="margin-top:8px">The text it would write:</div><div class="rich preview">${richHTML(r.previewBody.text, r.previewBody.dir)}</div>` : ''}${r.pills ? `<div class="pillrow">${r.pills.map(([t, c]) => `<span class="pill ${c}">${esc(t)}</span>`).join('<span style="color:var(--faint)">→</span>')}</div>` : ''}
    ${r.targets && r.targets.length > 1 ? targets(r) : r.targets ? `<div class="pillrow">${r.targets.map((t) => t.mem ? pill(t.mem) : `<span class="pill">${esc(t.label)}</span>`).join('')}</div>` : r.mems?.length ? `<div class="pillrow">${r.mems.map(pill).join('')}</div>` : ''}</div>
    <div class="acts">${dismissed ? '<span class="hint">Dismissed</span><span class="spacer"></span><button class="btn sm" data-a="restore">Show again</button>'
      : `<span class="spacer"></span><button class="btn sm ${r.secondary.danger ? 'dng' : ''}" data-a="secondary" ${off(r, r.secondary)}>${esc(label(r, r.secondary))}</button>${r.more?.items.length ? `<button class="btn sm" data-a="more">${esc(r.more.label)} ▾</button>` : ''}<button class="btn pri sm ${r.primary.danger ? 'dng' : ''}" data-a="primary" ${off(r, r.primary)}>${esc(label(r, r.primary))}</button>`}</div></div>`;
  $('#hacts').innerHTML = '<button class="btn" id="recheck" title="Re-read everything now">Check again</button><button class="btn" id="fullaudit" title="Copy a command that runs the official prompt audit in a project">Audit with Claude</button>';
  $('#v-review').innerHTML = `<div class="rv">` +
    (open.length ? open.map((r) => card(r)).join('') : '<div class="done">All caught up. New suggestions appear here as things change.</div>') +
    (hidden.length ? `<button class="link2" id="showdis" style="margin:14px 0">${ui.showDismissed ? 'Hide' : 'Show'} ${hidden.length} dismissed</button>${ui.showDismissed ? hidden.map((r) => card(r, true)).join('') : ''}` : '') + '</div>';
  $('#fullaudit').onclick = (e) => menu(e.currentTarget as HTMLElement, model.projects.filter((p) => p.key !== 'Global' && p.exists && !p.hidden)
    .map((p) => [p.label, () => void copyText(`cd ${shq(p.path)} && claude '/claude-api prompt-audit'`, 'Copied. Paste it in a terminal: Claude audits that project and proposes fixes.')] as MenuItem));
  $('#recheck').onclick = async () => {
    const r = await rescan();
    await refresh();
    toast(r.ok ? 'Checked again: Review is up to date.' : r.message);
  };
  document.getElementById('showdis')?.addEventListener('click', () => { ui.showDismissed = !ui.showDismissed; render(); });
  $$('#v-review .pill[data-id], #v-review .rich .lchip[data-id]').forEach((p) => (p.onclick = () => { const m = model.byId.get(p.dataset.id ?? ''); if (m) openMemory(m); }));
  $$('#v-review .card').forEach((c) => {
    const r = review.find((x) => x.id === c.dataset.r);
    if (!r) return;
    $$<HTMLInputElement>('input[data-t]', c).forEach((box) => (box.onchange = () => {
      const sel = picked(r)!;
      if (box.checked) sel.add(box.dataset.t ?? ''); else sel.delete(box.dataset.t ?? '');
      render();
    }));
    $$<HTMLButtonElement>('[data-all]', c).forEach((b) => (b.onclick = () => {
      picks.set(r.id, new Set(b.dataset.all === '1' ? r.targets!.map((t) => t.path) : []));
      render();
    }));
    $$<HTMLButtonElement>('.acts button', c).forEach((b) => (b.onclick = async () => {
      if (b.dataset.a === 'restore') { setDismissed(r.id, false); render(); return; }
      if (b.dataset.a === 'more') { menu(b, r.more!.items.map(([l, act]) => [l, () => void busy(b, () => act_(act))] as MenuItem)); return; }
      await busy(b, () => act_(b.dataset.a === 'primary' ? r.primary : r.secondary));
    }));
    // Immediate feedback while a change runs and Review re-checks (the card leaves once it's fixed).
    const busy = async (b: HTMLButtonElement, f: () => Promise<void>): Promise<void> => {
      const text = b.textContent;
      c.classList.add('busy');
      $$<HTMLButtonElement>('.acts button', c).forEach((x) => (x.disabled = true));
      b.textContent = 'Working…';
      try { await f(); } finally { if (c.isConnected) { c.classList.remove('busy'); b.textContent = text; $$<HTMLButtonElement>('.acts button', c).forEach((x) => (x.disabled = false)); } }
    };
    // Runs one of the card's actions, on the checked targets only.
    const act_ = async (a: Action): Promise<void> => {
      const sel = picked(r);
      const args = sel && Array.isArray(a.args.paths) ? { ...a.args, paths: [...sel] } : a.args;
      if (a.op === 'copy-connect') { void copyText(CONNECT, 'Copied. Paste them in a terminal, then start a new Claude session.'); return; }
      if (a.op === 'connect-later') { try { localStorage.setItem('cca-connect-later', '1'); } catch { /* the card returns next visit */ } c.classList.add('gone'); setTimeout(render, 220); return; }
      if (a.op === 'dismiss') { setDismissed(r.id, true); c.classList.add('gone'); setTimeout(render, 220); return; }
      if (a.op === 'open') { const m = model.byId.get(String(a.args.path)); if (m) openMemory(m); return; }
      if (a.op === 'filter-project') { ui.proj = String(a.args.project ?? ''); setView('all'); return; }
      if (a.op === 'view-ins') { setView('ins'); return; }
      if (a.op === 'proposal-accept' || a.op === 'proposal-dismiss') {
        const res = await decide(String(a.args.id), a.op === 'proposal-accept');
        const id = res.activity;
        toast(res.message, res.ok && res.canUndo && id ? () => void undoChange(id) : undefined);
        if (res.ok) await refresh();
        return;
      }
      if (a.op === 'reveal') { void reveal(String(a.args.path)); return; }
      if (a.op === 'convert-many') { // one conversion per checked project, each with its own Undo in Activity
        const paths = (args.paths as string[]) ?? [];
        if (!paths.length) { toast('Tick at least one project.'); return; }
        if (model.state.readOnly) { toast('Read-only mode: start cca without --read-only to make changes.'); return; }
        const res = [];
        for (const path of paths) res.push(await act('convert-to-agents', { path }));
        const bad = res.find((x) => !x.ok);
        await refresh();
        toast(bad ? bad.message : paths.length === 1 ? res[0].message : `Converted ${paths.length} files to AGENTS.md. Undo any of them in Activity.`);
        return;
      }
      if (a.op === 'relocate-pick') { relocateMenu(c.querySelector<HTMLElement>('[data-a="secondary"]') ?? c, String(a.args.from)); return; }
      if (a.op === 'preview-caps') { void openCapsPreview(String(a.args.path), () => render()); return; }
      if (a.op === 'copy-audit') { void copyText(auditCommand(String(a.args.path), String(a.args.project)), 'Copied. Paste it in a terminal: Claude runs the official prompt audit and proposes fixes.'); return; }
      // The card stays until the next check says it's fixed: a partial fix leaves the rest, and
      // Undo brings it back.
      await run(a.op, args);
    };
  });
}

/** Where a gone project's memories can go: folders near the old one, or Everywhere. */
function relocateMenu(at: HTMLElement | MouseEvent, from: string): void {
  const g = model.state.report.findings.find((x) => x.code === 'folder-moved' && x.path === from);
  const paths = model.mems.filter((x) => x.path.startsWith(from + '/')).map((x) => x.path);
  menu(at, [
    ...(g?.candidates ?? []).map((to) => [to.replace(model.state.home, '~'), () => void run('project-relocate', { from, to })] as MenuItem),
    null,
    ['Everywhere (every project)', () => void run('bulk', { action: 'global', paths })],
  ]);
}

function evidenceHTML(r: ReviewItem): string {
  const x = r.finding!;
  const lines = (x.evidence ?? []).map((e) => `<div class="ev"><span class="ln">${esc(e.line)}</span><span>${esc(e.text.trim().slice(0, 240))}</span></div>`).join('');
  return `<div class="evid"><div class="meta2"><span class="conf ${esc(x.confidence)}" title="How sure the check is that this needs a change">${esc(({ High: 'Likely', Medium: 'Possible', Low: 'Unsure' } as Record<string, string>)[x.confidence ?? ''] ?? x.confidence ?? '')}</span><span class="mono">${esc(x.path.replace(model.state.home, '~'))}</span></div>${lines ? `<div class="diff2">${lines}</div>` : ''}</div>`;
}

/** The command that runs the official prompt audit on one file, from its project folder. */
function auditCommand(path: string, project: string): string {
  return `cd ${shq(project || path.slice(0, path.lastIndexOf('/')))} && claude ${shq(`/claude-api prompt-audit ${path}`)}`;
}

/** Single-quotes s for a POSIX shell. */
const shq = (s: string): string => `'${s.replace(/'/g, `'\\''`)}'`;

async function openCapsPreview(path: string, applied: () => void): Promise<void> {
  if (dirty && drawerOpen()) { leave(() => void openCapsPreview(path, applied)); return; }
  ui.open = null;
  ui.file = null;
  const d = $('#drawer');
  const p = await previewCaps(path);
  const lines = hunks(diffLines(p.before, p.after), 1);
  d.innerHTML = `<div class="dhead"><div class="where">Preview · ${p.changes} word${p.changes === 1 ? '' : 's'} at normal volume</div><button class="x" id="dx" aria-label="Close">×</button></div>
   <div class="dbody"><div class="hint mono">${esc(path.replace(model.state.home, '~'))}</div>
    <div class="diff2">${lines.map((l) => `<div class="${l.op === '-' ? 'r' : l.op === '+' ? 'a' : 'c'}">${esc(l.op + ' ' + l.text)}</div>`).join('')}</div>
    <p class="hint">Only all-caps words change. Code blocks, inline code and the file’s header are left alone. You can undo this from Activity.</p></div>
   <div class="dfoot"><button class="btn pri" id="capply">Apply</button><span class="spacer"></span></div>`;
  d.classList.add('open');
  watchEdits(d);
  $('#dx').onclick = () => leave(closeDrawer);
  $('#capply').onclick = () => void run('quiet-caps', { path }).then((ok) => { if (ok) { closeDrawer(); applied(); } });
}

// ---------- instructions ----------

function renderIns(): void {
  heading('Instructions', 'Files Claude reads when a session starts');
  const es = model.state.entries;
  const children = (path: string): Entry[] => es.filter((e) => e.scope === 'import' && e.meta?.importedBy === path);
  const short = (p: string): string => p.replace(model.state.home, '~');
  const codes = (path: string): string[] => model.state.report.findings.filter((f) => f.path === path).map((f) => f.code);
  const item = (e: Entry, depth = 0): string => {
    const c = codes(e.path ?? '');
    const issues = (e.issues ?? []).map((i) => { const [code, ...rest] = i.split(':'); const what = rest.join(':'); return `<span class="tag bad"${what ? ` title="${esc(what)}"` : ''}>${esc(code.replace(/-/g, ' '))}${what ? ': ' + esc(what.split('/').pop() ?? what) : ''}</span>`; }).join('') +
      (c.includes('agents-md-ignored') ? '<span class="tag warn" title="This project also has a CLAUDE.md">ignored by Claude</span>' : '');
    const convert = c.includes('claude-md-convertible') ? `<button class="btn sm" data-convert="${esc(e.path)}">Convert to AGENTS.md</button>` : '';
    return `<div class="tnode ${depth ? 'child' : ''}" data-path="${esc(e.path)}" tabindex="0" style="${depth > 1 ? `margin-left:${28 * depth}px` : ''}"><div style="min-width:0"><div style="font-weight:600">${esc(e.name)} ${issues}</div><div class="path">${esc(short(e.path ?? ''))}</div></div>${convert}<span class="meta">~${fmtN(Math.round((e.bytes ?? 0) / 4))} tokens</span></div>` +
      children(e.path ?? '').map((c) => item(c, depth + 1)).join('');
  };
  const user = es.filter((e) => (e.kind === 'instructions' || e.kind === 'rule') && e.scope === 'user' && pass('user'));
  const proj = model.projects.filter((p) => p.key !== 'Global').map((p) => [p, es.filter((e) => (e.kind === 'instructions' || e.kind === 'rule') && e.scope !== 'import' && e.project === p.key && pass(layerOf(e.scope), e.project))] as const).filter(([, xs]) => xs.length);
  if (!user.length && !proj.length) {
    $('#v-ins').innerHTML = emptyHTML('No instructions yet', 'A CLAUDE.md in ~/.claude applies to every project; one in a repo applies to that project. They appear here once they exist.');
    return;
  }
  $('#v-ins').innerHTML = `<div class="ins">${user.length ? `<div class="sect" style="padding:0">For every project</div><div class="tree">${user.map((e) => item(e)).join('')}</div>` : ''}` +
    proj.map(([p, xs]) => `<div class="sect" style="padding:12px 0 0">${esc(p.label)}</div><div class="tree">${xs.map((e) => item(e)).join('')}</div>`).join('') +
    '<p class="hint">Token counts are estimates. Files in subfolders load only when Claude works in that folder. By default Claude Code reads a project\'s AGENTS.md only when the project has no CLAUDE.md (setting: instructionFiles).</p></div>';
  $$<HTMLButtonElement>('[data-convert]').forEach((b) => (b.onclick = (ev) => { ev.stopPropagation(); void run('convert-to-agents', { path: b.dataset.convert }); }));
  $$('#v-ins .tnode').forEach((n) => {
    const e = es.find((x) => x.path === n.dataset.path);
    if (!e) return;
    n.onclick = () => openFile(e);
    n.onkeydown = (k) => { if (k.key === 'Enter') openFile(e); };
  });
}

// ---------- what loads here ----------

async function renderWhat(): Promise<void> {
  const projects = model.projects.filter((p) => p.key !== GLOBAL && p.exists && !p.hidden);
  const proj = ui.proj && ui.proj !== GLOBAL ? ui.proj : '';
  heading('What loads here', proj ? '' : 'What Claude reads before you type, and its cost');
  if (!proj && !projects.length) {
    $('#v-what').innerHTML = emptyHTML('No projects yet', 'Each folder you run Claude Code in loads a different set of instructions, memories and tools. Projects appear here once Claude has run in one.');
    return;
  }
  if (!proj) { // a session always runs in one folder: ask which
    $('#v-what').innerHTML = `<div class="tk"><div class="banner"><span class="ic2">i</span><div><b>Pick a project</b>${ui.proj === GLOBAL ? 'Everywhere isn\'t a folder Claude runs in. Choose a project to see what loads there.' : 'Claude loads a different set in every folder. Choose one in the sidebar, or here.'}</div></div><div class="seg" id="wseg">${projects.map((p) => `<button data-p="${esc(p.key)}">${esc(p.label)}</button>`).join('')}</div></div>`;
    $$('#wseg button').forEach((b) => (b.onclick = () => { ui.proj = b.dataset.p ?? ''; render(); }));
    return;
  }
  $('#v-what').innerHTML = '<div class="tk"><div class="budget"><div class="bnum"><b>…</b><span>calculating</span></div></div></div>';
  const { budget, shadows } = await loadBudget(proj);
  if (ui.view !== 'what' || ui.proj !== proj) return; // the scope changed while loading
  const colors = ['var(--c4)', 'var(--c5)', 'var(--c2)', 'var(--c1)', 'var(--c6)', 'var(--c7)'];
  const total = Math.max(1, budget.total);
  const pct = Math.round((budget.indexLines / model.state.indexMaxLines) * 100);
  const label = model.projectOf(proj).label;
  const home = model.state.home;
  const entryAt = (path: string): Entry | undefined => model.state.entries.find((e) => e.path === path);
  const short = (path: string): string => path.replace(home, '~');
  // Each source is a row: files open in the editor; the rest go to the page that manages them.
  const row = (s: Source, i: number): string => {
    const files = s.paths?.length ? `<div class="bfiles" id="bf${i}" hidden>${s.paths.map((p) => `<button class="bfile" data-path="${esc(p)}" ${entryAt(p) ? '' : 'disabled'}><span>${esc(p.split('/').pop() ?? p)}</span><small>${esc(short(p.slice(0, p.lastIndexOf('/'))))}</small><span class="ftok">${entryAt(p)?.bytes ? '~' + fmtN(Math.round((entryAt(p)?.bytes ?? 0) / 4)) : ''}</span></button>`).join('')}</div>` : '';
    const action = s.paths?.length ? `aria-expanded="false" aria-controls="bf${i}"` : s.view ? `data-view="${esc(s.view)}"` : 'disabled';
    return `<button class="bl" data-i="${i}" ${action}><span class="sw2" style="background:${colors[i]}"></span><span class="blabel">${esc(s.label)}<small>${esc(s.detail)}</small></span><span class="tok">${s.tokens ? '~' + fmtN(s.tokens) : s.view === 'mcp' ? 'on demand' : '–'}</span><span class="pc">${s.tokens ? Math.round((s.tokens / total) * 100) + '%' : ''}</span><span class="chev" aria-hidden="true">${s.paths?.length ? '›' : s.view ? '→' : ''}</span></button>${files}`;
  };
  const index = budget.sources.find((s) => s.label === 'Memory index')?.paths?.[0];
  const html = `<div class="budget"><div class="bnum"><b>~${fmtN(budget.total)}</b><span>tokens load at the start of every ${esc(label)} session, before your first message</span></div>
    <div class="bar">${budget.sources.map((s, i) => (s.tokens ? `<i style="width:${(s.tokens / total) * 100}%;background:${colors[i]}" title="${esc(s.label)}"></i>` : '')).join('')}</div>
    <div class="blist">${budget.sources.map(row).join('')}</div></div>
   ${pct > 70 ? `<div class="banner warn"><span class="ic2">!</span><div style="flex:1"><b>Memory index is about ${pct}% full</b>Claude loads only about the first ${model.state.indexMaxLines} lines (${Math.round(model.state.indexMaxBytes / 1024)} KB) of MEMORY.md; anything past that is invisible until it is trimmed.<div class="meter warn" style="margin-top:8px"><i style="width:${Math.min(100, pct)}%"></i></div></div>${index && entryAt(index) ? '<button class="btn sm" id="openidx">Open MEMORY.md</button>' : ''}</div>` : ''}
   ${shadows.length ? `<div class="banner"><span class="ic2">⇅</span><div><b>${shadows.length} name${shadows.length > 1 ? 's are' : ' is'} defined more than once</b>${shadows.map((s) => `${esc(s.kind)} <b style="display:inline">${esc(s.name)}</b>: the <button class="link2" data-path="${esc(s.winner.path ?? '')}">${esc(s.winner.scope)} one</button> wins over ${s.hidden.map((h) => `<button class="link2" data-path="${esc(h.path ?? '')}">${esc(h.scope)}</button>`).join(', ')}`).join('<br>')}</div></div>` : ''}`;
  $('#v-what').innerHTML = `<div class="tk">${html}</div>`;
  const open = (path: string | undefined): void => { const e = path ? entryAt(path) : undefined; if (e) openFile(e); };
  $$<HTMLButtonElement>('#v-what .bl').forEach((b) => (b.onclick = () => {
    if (b.dataset.view) { setView(b.dataset.view as View); return; }
    const list = document.getElementById(b.getAttribute('aria-controls') ?? '');
    if (!list) return;
    const on = b.getAttribute('aria-expanded') !== 'true';
    b.setAttribute('aria-expanded', String(on));
    list.hidden = !on;
  }));
  $$<HTMLButtonElement>('#v-what [data-path]').forEach((b) => (b.onclick = () => open(b.dataset.path)));
  document.getElementById('openidx')?.addEventListener('click', () => open(index));
}

// ---------- toolkit lists ----------

function rowsHTML(rows: Row[], opts: { tokens?: boolean } = {}): string {
  return rows.map((r) => `<div class="trow" data-k="${esc(r.key)}" tabindex="0" role="button" aria-label="${esc(r.name)}: open details"><div style="min-width:0"><div class="nm">${esc(r.name)}<small>${esc(r.source)}</small>${r.warn.map((w) => `<span class="tag warn">${esc(w)}</span>`).join('')}</div>${r.desc ? `<div class="ds">${esc(r.desc)}</div>` : ''}${r.tags.length ? `<div class="comps">${r.tags.filter(Boolean).map((t) => `<span class="tag${t.includes('••') ? ' mono' : ''}">${esc(t)}</span>`).join('')}</div>` : ''}</div>
    ${opts.tokens && r.tokens !== undefined ? `<div class="stat2 hide-sm"><b>~${fmtN(r.tokens)} tokens</b>${esc(r.tokensLabel ?? '')}</div>` : '<span></span>'}${usageCell(r.uses, r.last, !!opts.tokens)}${r.canToggle ? toggle(r.enabled, r.key) : ''}</div>`).join('');
}

function wireRows(root: HTMLElement, rows: Row[], onToggle?: (r: Row, on: boolean) => Promise<boolean>): void {
  $$('.trow', root).forEach((el) => {
    const r = rows.find((x) => x.key === el.dataset.k);
    if (!r) return;
    el.onclick = () => openTool(r);
    el.onkeydown = (e) => { if ((e.key === 'Enter' || e.key === ' ') && e.target === el) { e.preventDefault(); openTool(r); } };
    const path = r.entry?.path;
    el.oncontextmenu = (e) => {
      e.preventDefault();
      menu(e, [['Open', () => openTool(r)], ...(path ? [null, ['Copy path', () => void copyText(path)], [platform.show, () => void reveal(path)]] as MenuItem[] : [])]);
    };
  });
  $$<HTMLButtonElement>('.switch', root).forEach((b) => (b.onclick = async (e) => {
    e.stopPropagation();
    const r = rows.find((x) => x.key === b.dataset.sw);
    if (!r) return;
    const on = b.getAttribute('aria-checked') !== 'true';
    b.setAttribute('aria-checked', String(on));
    if (!onToggle || !(await onToggle(r, on))) b.setAttribute('aria-checked', String(!on));
  }));
}

function renderPlugins(): void {
  const rows = scoped(plugins(model));
  heading('Plugins', `${rows.filter((r) => r.enabled).length} of ${rows.length} on · bundles of skills, hooks and MCP`);
  if (!rows.length) {
    $('#v-plugins').innerHTML = emptyHTML('No plugins', 'Plugins bundle skills, hooks and MCP servers. Paste an install command from a plugin\'s docs to add one.', addButton('+ Add plugin'));
    wireEmpty('#v-plugins');
    return;
  }
  $('#v-plugins').innerHTML = `<div class="tk"><div class="tgroup">${rowsHTML(rows, { tokens: true })}</div><p class="hint">Changes go through Claude Code's own <span class="mono">claude plugin</span> command. Token costs come from <span class="mono">claude plugin details</span>.</p></div>`;
  wireRows($('#v-plugins'), rows, (r, on) => run(on ? 'plugin-enable' : 'plugin-disable', { id: r.key }, `${on ? 'Enabled' : 'Disabled'} ${r.name}. New sessions pick it up.`));
}

function renderMCP(): void {
  const rows = scoped(mcpServers(model));
  heading('MCP servers', 'Tools Claude can call');
  const groups: [string, Row[]][] = [['Yours', rows.filter((r) => !r.readonly)], ['From plugins', rows.filter((r) => r.source.startsWith('From plugin'))],
    ['claude.ai connectors', rows.filter((r) => r.source === 'claude.ai connector')], ['Built in', rows.filter((r) => r.source.startsWith('Built'))]];
  if (!rows.length) {
    $('#v-mcp').innerHTML = emptyHTML('No MCP servers', 'MCP servers give Claude tools, like a browser or a database. Paste the JSON or command an install guide gives you to add one.', addButton('+ Add MCP server'));
    wireEmpty('#v-mcp');
    return;
  }
  $('#v-mcp').innerHTML = `<div class="tk"><div class="banner"><span class="ic2">⚿</span><div><b>Keys stay hidden</b>Secret values are never read into this app or shown to Claude. Only key names appear.</div></div>` +
    groups.filter(([, xs]) => xs.length).map(([g, xs]) => `<div class="tgroup"><div class="gh">${g}<span class="n">${xs.length}</span></div>${rowsHTML(xs)}</div>`).join('') + '</div>';
  wireRows($('#v-mcp'), rows);
}

function renderSkills(): void {
  const rows = scoped(skills(model));
  heading('Skills & commands', 'Loaded when a task matches, or as /commands');
  const byScope = (pred: (r: Row) => boolean): Row[] => rows.filter(pred);
  const copies = rows.filter((r) => r.warn.length);
  const pluginSkills = pass('plugin') ? model.entries('skill').filter((s) => s.scope === 'plugin') : [];
  const perPlugin = new Map<string, number>();
  for (const s of pluginSkills) perPlugin.set(String(s.meta?.plugin), (perPlugin.get(String(s.meta?.plugin)) ?? 0) + 1);
  const projectsWith = model.projects.filter((p) => rows.some((r) => r.project === p.key));
  if (!rows.length && !pluginSkills.length) {
    $('#v-skills').innerHTML = emptyHTML('No skills or commands', 'Skills are instructions Claude uses when a task matches; commands run as /name. Write one, or paste a SKILL.md.', addButton('+ New skill'));
    wireEmpty('#v-skills');
    return;
  }
  $('#v-skills').innerHTML = `<div class="tk">${copies.length ? `<div class="banner warn"><span class="ic2">⧉</span><div style="flex:1"><b>${((n) => `${n} ${n === 1 ? 'skill or command is' : 'skills and commands are'}`)(new Set(copies.map((c) => c.name)).size)} copied into several projects</b>Identical files live in each repo. One shared copy would serve every project.</div><button class="btn sm pri" id="mkglobal">Make global</button></div>` : ''}
   ${byScope((r) => r.scope === 'user').length ? `<div class="tgroup"><div class="gh">Everywhere<span class="n">${byScope((r) => r.scope === 'user').length}</span></div>${rowsHTML(byScope((r) => r.scope === 'user'), { tokens: true })}</div>` : ''}
   ${projectsWith.map((p) => { const xs = byScope((r) => r.project === p.key); return `<div class="tgroup"><div class="gh"><span class="dot" style="background:${p.color}"></span>${esc(p.label)}<span class="n">${xs.length}</span></div>${rowsHTML(xs, { tokens: true })}</div>`; }).join('')}
   ${pluginSkills.length ? `<div class="tgroup"><div class="gh">From plugins<span class="n">${pluginSkills.length}</span></div><div class="comps">${[...perPlugin].map(([p, n]) => `<span class="tag">${esc(p)} · ${n}</span>`).join('')}</div></div>` : ''}</div>`;
  wireRows($('#v-skills'), rows);
  const mk = document.getElementById('mkglobal');
  if (mk) mk.onclick = () => void run('make-global', { paths: copies.map((c) => c.entry?.path) });
}

function renderAgents(): void {
  const rows = scoped(agents(model));
  heading('Agents', 'Helpers Claude can hand work to');
  $('#v-agents').innerHTML = rows.length ? `<div class="tk"><div class="tgroup">${rowsHTML(rows)}</div></div>`
    : emptyHTML('No agents', 'Agents are helpers Claude can hand a task to. Each is a Markdown file in ~/.claude/agents, or comes with a plugin.');
  wireRows($('#v-agents'), rows);
}

const EVENTS: [string, string][] = [['SessionStart', 'When a session starts'], ['UserPromptSubmit', 'When you send a message'], ['PreToolUse', 'Before Claude uses a tool'],
  ['PostToolUse', 'After Claude uses a tool'], ['Stop', 'When Claude finishes replying'], ['SubagentStop', 'When an agent finishes'], ['PreCompact', 'Before the conversation is compacted'],
  ['SessionEnd', 'When a session ends'], ['WorktreeCreate', 'When a worktree is created'], ['WorktreeRemove', 'When a worktree is removed']];

/** A hook matcher in plain words: "Edit|Write" → "when Claude edits or writes files". Unknown
 *  patterns stay as written. */
function matcherText(m: string): string {
  const T: Record<string, string> = { Edit: 'edits files', MultiEdit: 'edits files', Write: 'writes files', Read: 'reads files', Bash: 'runs a shell command',
    Glob: 'searches for files', Grep: 'searches files', WebFetch: 'fetches a web page', WebSearch: 'searches the web', Task: 'starts an agent', Agent: 'starts an agent', NotebookEdit: 'edits a notebook' };
  if (m === '*' || m === '') return 'for every tool';
  const parts = m.split('|').map((x) => x.trim());
  const words = parts.map((x) => T[x] ?? (/^mcp__([^_]+(?:_[^_]+)*)__/.test(x) || /^mcp__[\w-]+$/.test(x) ? `uses the ${x.split('__')[1]} MCP server` : ''));
  return words.every(Boolean) ? 'when Claude ' + [...new Set(words)].join(' or ') : m;
}

function renderHooks(): void {
  heading('Hooks', 'Commands that run at points in a session');
  const hooks = model.entries('hook').filter((h) => pass(layerOf(h.scope), h.project));
  const scopeTag = (h: Entry): string => h.scope === 'plugin' ? `<span class="tag">plugin · ${esc(h.meta?.plugin)}</span>` : h.scope === 'user' ? '<span class="tag">Everywhere</span>'
    : `<span class="tag">${h.scope === 'local' ? 'Only you · ' : ''}${esc(model.projectOf(h.project ?? '').label)}</span>`;
  const item = (h: Entry): string => `<div class="hitem"><div style="min-width:0"><code>${esc(String(h.meta?.command ?? '').replaceAll('${CLAUDE_PLUGIN_ROOT}', '‹plugin folder›'))}</code><div class="comps" style="margin-top:6px">${scopeTag(h)}${h.meta?.matcher ? `<span class="tag" title="Matcher: ${esc(h.meta.matcher)}">${esc(matcherText(String(h.meta.matcher)))}</span>` : ''}${(h.issues ?? []).length ? '<span class="tag bad">script not found</span>' : ''}</div></div><div style="display:flex;gap:8px;align-items:center">${h.scope === 'plugin' ? '' : `<button class="btn sm dng hrm" data-path="${esc(h.path)}" data-ev="${esc(h.name)}" data-g="${esc(h.meta?.group)}" data-pos="${esc(h.meta?.pos)}">Remove</button>`}</div></div>`;
  const known = new Set(EVENTS.map(([e]) => e));
  const extra = [...new Set(hooks.map((h) => h.name).filter((n) => !known.has(n)))].map((n) => [n, ''] as [string, string]);
  $('#v-hooks').innerHTML = `<div class="tk"><div class="banner warn"><span class="ic2">!</span><div><b>Hooks run commands on your computer</b>Remove one here (Undo brings it back); to change one, edit the settings file it lives in. Claude can suggest removing hooks but never adds one.</div></div><div>` +
    (hooks.length ? [...EVENTS, ...extra].filter(([ev]) => hooks.some((h) => h.name === ev)).map(([ev, label]) => {
      const xs = hooks.filter((h) => h.name === ev);
      return `<div class="hk"><div class="ev">${esc(label || ev)}${label ? `<small class="mono">${esc(ev)}</small>` : ''}</div><div class="items">${xs.map(item).join('')}</div></div>`;
    }).join('') : emptyHTML('No hooks', 'Hooks run a command at points in a session, like before Claude uses a tool. They live in settings.json.')) + '</div></div>';
  $$<HTMLButtonElement>('.hrm').forEach((b) => (b.onclick = () => void run('hook-remove',
    { path: b.dataset.path, event: b.dataset.ev, group: Number(b.dataset.g), pos: Number(b.dataset.pos) })));
}

const VIEW_RENDER: Partial<Record<View, () => void>> = {
  review: renderReview, ins: renderIns, what: () => void renderWhat(), plugins: renderPlugins, mcp: renderMCP,
  skills: renderSkills, agents: renderAgents, hooks: renderHooks,
  activity: () => void renderActivity(),
  map: () => {
    heading('Map', 'How memories connect across projects');
    const none = !model.mems.length;
    $('#map').hidden = none; // the map keeps its own elements; the empty state sits beside them
    $('#mapempty').innerHTML = none ? emptyHTML('Nothing to map yet', 'The map shows how memories link to each other across projects. It fills in as Claude saves memories.') : '';
    if (!none) { drawMap(model, memShown, openMemory); markMap(ui.open?.id ?? null); }
  },
};

// ---------- drawers ----------

/** The memories m links to and from, drawn around it. max caps each side (the panel shows 7;
 *  the expanded view shows all). Each link is one group, so hovering it highlights the line too. */
function egoSVG(m: Mem, max = 7, W = 420, minH = 140): string {
  const L = m.inn.slice(0, max), R = m.out.slice(0, max), rowH = 26;
  const H = Math.max(minH, Math.max(L.length, R.length) * rowH + 40), cx = W / 2, cy = H / 2;
  const lx = Math.max(162, cx - 210), rx = Math.min(W - 162, cx + 210); // columns stay near the centre when wide
  const ys = (k: number): number[] => Array.from({ length: k }, (_, i) => cy + (i - (k - 1) / 2) * rowH);
  const room = W > 420 ? 40 : 21, cut = (s: string): string => (s.length > room ? s.slice(0, room - 1) + '…' : s);
  const hitW = Math.min(room * 7 + 16, W > 420 ? 320 : 150); // the whole row (dot and label) is the click target
  const font = 'font-family="Geist Variable,system-ui"';
  const side = (arr: Mem[], x: number, anchor: 'start' | 'end', dir: number): string => ys(arr.length).map((y, i) => {
    const t = arr[i];
    return `<g class="eg" data-id="${esc(t.id)}" tabindex="0" role="button" aria-label="${esc(t.title)}"><title>${esc(t.title)} · ${esc(model.projectOf(t.project).label)}</title>
      <path d="M${cx + dir * 34},${cy} C${cx + dir * 80},${cy} ${x - dir * 60},${y} ${x - dir * 6},${y}" fill="none" stroke="${cssVar('var(--line-2)')}" stroke-width="1.3"/>
      <rect x="${anchor === 'end' ? x - hitW : x - 10}" y="${y - rowH / 2}" width="${hitW + 10}" height="${rowH}" fill="transparent"/>
      <circle cx="${x}" cy="${y}" r="4.5" fill="${cssVar(model.projectOf(t.project).color)}"/>
      <text x="${x + (anchor === 'end' ? -10 : 10)}" y="${y + 4}" text-anchor="${anchor}" font-size="12" fill="${cssVar('var(--fg)')}" ${font}>${esc(cut(t.title))}</text></g>`;
  }).join('');
  const top = (k: number): number => (W > 420 ? cy - ((k - 1) / 2) * rowH - 24 : 18); // over its column when wide
  const label = (x: number, a: string, t: string, y = 18): string => `<text x="${x}" y="${y}" text-anchor="${a}" font-size="11" fill="${cssVar('var(--faint)')}" ${font} letter-spacing=".06em">${t}</text>`;
  return `<svg viewBox="0 0 ${W} ${H}" role="img" aria-label="Connections">${L.length ? label(W > 420 ? lx + 4 : 16, W > 420 ? 'end' : 'start', 'MENTIONED BY', top(L.length)) : ''}${R.length ? label(W > 420 ? rx - 4 : W - 16, W > 420 ? 'start' : 'end', 'MENTIONS', top(R.length)) : ''}
    ${side(L, lx, 'end', -1)}${side(R, rx, 'start', 1)}
    <circle cx="${cx}" cy="${cy}" r="30" fill="${cssVar('var(--glass-strong)')}" stroke="${cssVar(model.projectOf(m.project).color)}" stroke-width="2.5"/>
    <text x="${cx}" y="${cy + 4}" text-anchor="middle" font-size="11" font-weight="600" fill="${cssVar('var(--fg)')}" ${font}>This</text>
    ${m.missing.length ? `<text x="${cx}" y="${H - 10}" text-anchor="middle" font-size="11.5" fill="${cssVar('var(--danger)')}" ${font}>${m.missing.length} broken link${m.missing.length > 1 ? 's' : ''}</text>` : ''}
    ${!L.length && !R.length && !m.missing.length ? `<text x="${cx}" y="${cy + 52}" text-anchor="middle" font-size="12" fill="${cssVar('var(--muted)')}" ${font}>Not connected to anything yet</text>` : ''}</svg>`;
}

/** Clicks and Enter on the graph's memories go to pick(). */
function wireEgo(root: HTMLElement, pick: (t: Mem) => void): void {
  $$('.eg', root).forEach((g) => {
    const go = (): void => { const t = model.byId.get(g.dataset.id ?? ''); if (t) pick(t); };
    g.onclick = go;
    g.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); go(); } };
  });
}

/** The connections graph, large: every link, drag to move, pinch or ⌘-scroll (or + and −) to zoom.
 *  Clicking a memory re-centres the graph on it, so you can walk the links; Open edits it. */
function openEgo(start: Mem): void {
  document.querySelector('.egodlg')?.remove();
  const dlg = document.createElement('dialog');
  dlg.className = 'egodlg'; dlg.setAttribute('aria-label', 'Connections');
  $('#app').append(dlg);
  dlg.onclose = () => dlg.remove();
  const trail: Mem[] = [];
  let cur = start;
  const draw = (): void => {
    dlg.innerHTML = `<div class="egohead"><span class="navb"><button id="egback" aria-label="Back" ${trail.length ? '' : 'disabled'}><svg viewBox="0 0 16 16"><path d="M10 3 5 8l5 5"/></svg></button></span>
      <span class="dot" style="background:${model.projectOf(cur.project).color}"></span><b>${esc(cur.title)}</b><span class="hint">${esc(model.projectOf(cur.project).label)} · ${cur.inn.length} mention it · ${cur.out.length} mentioned</span>
      <span class="spacer"></span><span class="hint hide-sm">Drag to move · pinch or ⌘-scroll to zoom</span>
      <span class="maptools" style="position:static;display:flex"><button id="egin" aria-label="Zoom in">+</button><button id="egout" aria-label="Zoom out">−</button></span>
      <button class="btn sm pri" id="egopen">Open</button><button class="x" id="egx" aria-label="Close">×</button></div>
      <div class="egobody conn"></div>`;
    const body = $('.egobody', dlg);
    const bw = Math.max(420, body.clientWidth), bh = Math.max(140, body.clientHeight);
    body.innerHTML = egoSVG(cur, Infinity, bw, bh); // drawn at the dialog's size: 1:1
    const svgEl = body.querySelector('svg')!, vp = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    vp.append(...svgEl.childNodes);
    svgEl.append(vp);
    const svg = select<SVGSVGElement, unknown>(svgEl);
    const z = d3zoom<SVGSVGElement, unknown>().scaleExtent([0.5, 4])
      .filter((e: Event) => e.type !== 'wheel' || (e as WheelEvent).ctrlKey || (e as WheelEvent).metaKey) // plain scrolling isn't zoom
      .on('zoom', (e) => vp.setAttribute('transform', String(e.transform)));
    svg.call(z).call(z.scaleTo, 1.2, [bw / 2, bh / 2]); // a little closer than 1:1 to start, around the centre
    $('#egin', dlg).onclick = () => void svg.transition().duration(200).call(z.scaleBy, 1.3);
    $('#egout', dlg).onclick = () => void svg.transition().duration(200).call(z.scaleBy, 1 / 1.3);
    $<HTMLButtonElement>('#egback', dlg).onclick = () => { cur = trail.pop() ?? cur; draw(); };
    $('#egopen', dlg).onclick = () => { dlg.close(); openMemory(cur); };
    $('#egx', dlg).onclick = () => dlg.close();
    wireEgo(dlg, (t) => { if (t !== cur) { trail.push(cur); cur = t; draw(); } });
  };
  dlg.showModal(); // first, so the graph is drawn at the dialog's real size
  draw();
}

function memoryMenu(m: Mem): MenuItem[] {
  return [
    ['Open', () => openMemory(m)],
    ...(m.project !== 'Global' ? [['Use everywhere', () => void run('memory-promote', { path: m.path })] as MenuItem] : []),
    null,
    ['Copy path', () => void copyText(m.path)],
    [platform.show, () => void reveal(m.path)],
    null,
    ['Move to Trash', () => void run('memory-trash', { path: m.path }).then((ok) => ok && ui.open?.id === m.id && closeDrawer())],
  ];
}

/** Memory text for reading: [[links]] become chips named by the memory they open (a missing one
 *  says so), and **bold** / `code` render. The file itself keeps the [[slug]] Claude reads. */
function richHTML(text: string, dir: string): string {
  return richText(text, (key) => {
    const t = model.resolve(dir, key);
    return t
      ? `<button type="button" class="lchip" data-id="${esc(t.id)}" title="Open “${esc(t.title)}”"><span class="dot" style="background:${model.projectOf(t.project).color}"></span>${esc(t.title)}</button>`
      : `<button type="button" class="lchip bad" data-miss="${esc(key)}" title="This memory doesn't exist. Click to fix the link.">${esc(human(key))}<span>missing</span></button>`;
  });
}

/** The fixes for one broken link in memory m: point it at a close match, write the note, or drop it. */
function fixLink(at: HTMLElement, m: Mem, key: string): void {
  if (dirty) { toast('Save or discard your edits first, then fix the link.'); return; }
  const near = closest(model, dirOf(m.path), key, m).filter((x) => x.s >= 0.25).slice(0, 4);
  menu(at, [
    ...near.map(({ c }): MenuItem => [`Link to “${c.title}”`, () => void run('relink', { from: key, to: c.stem, paths: [m.path] })]),
    [`Create “${human(key)}” as a new note`, () => void run('create-stub', { name: key, dir: dirOf(m.path) })],
    null,
    ['Remove the link', () => void run('unlink', { target: key, paths: [m.path] })],
  ]);
}

/** Typing [[ in memory text lists the memories it can link to (dir's own, then Everywhere);
 *  picking one writes its link. Arrows move, Enter or Tab picks, Escape closes. */
/** Whether the editor's Connections section starts open (it remembers the last choice). */
const connOpen = (): boolean => { try { return localStorage.getItem('cca-conn') !== '0'; } catch { return true; } };

/** A text box that grows with its text, so the drawer is the only thing that scrolls. */
function autogrow(ta: HTMLTextAreaElement): void {
  const fit = (): void => { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 2 + 'px'; };
  ta.addEventListener('input', fit);
  new MutationObserver(fit).observe(ta, { attributes: true, attributeFilter: ['hidden'] });
  requestAnimationFrame(fit);
}

/** Formatting for memory text, in its label row: Link (opens the [[ list, or a menu while the
 *  cursor is in a link), Bold and Code. The text's right-click menu has the same, plus Open / Change /
 *  Remove for a link and Cut / Copy / Paste; Shift+right-click keeps the system menu. */
function editTools(ta: HTMLTextAreaElement, dir: () => string, row: HTMLElement): void {
  const tools = document.createElement('span');
  tools.className = 'ftools'; tools.setAttribute('role', 'toolbar'); tools.setAttribute('aria-label', 'Formatting');
  tools.innerHTML = `<button type="button" data-t="link" title="Link a memory (or type [[)"><svg viewBox="0 0 16 16"><path d="M6.5 9.5a3 3 0 0 0 4.2 0l2-2a3 3 0 0 0-4.2-4.2l-.8.8M9.5 6.5a3 3 0 0 0-4.2 0l-2 2a3 3 0 0 0 4.2 4.2l.8-.8"/></svg><span>Link</span></button><button type="button" data-t="bold" title="Bold" aria-label="Bold"><b>B</b></button><button type="button" data-t="code" title="Code (a block when several lines are selected)" aria-label="Code"><span class="mono">&lt;/&gt;</span></button>`;
  row.querySelector('label')?.after(tools);
  tools.hidden = ta.hidden;
  new MutationObserver(() => { tools.hidden = ta.hidden; }).observe(ta, { attributes: true, attributeFilter: ['hidden'] });
  const edit = (from: number, to: number, text: string, sel?: [number, number]): void => {
    ta.focus(); ta.setRangeText(text, from, to, 'end');
    if (sel) ta.setSelectionRange(sel[0], sel[1]);
    ta.dispatchEvent(new Event('input', { bubbles: true })); // an edit, and it opens the [[ list when that's what was typed
  };
  const wrap = (open: string, close = open): void => {
    const [a, b] = [ta.selectionStart, ta.selectionEnd], t = ta.value.slice(a, b);
    edit(a, b, open + t + close, [a + open.length, a + open.length + t.length]);
  };
  const picked = (): string => ta.value.slice(ta.selectionStart, ta.selectionEnd);
  const link = (): void => edit(ta.selectionStart, ta.selectionEnd, '[[' + picked()); // the list filters by any selected text
  const bold = (): void => wrap('**');
  const code = (): void => (picked().includes('\n') ? wrap('```\n', '\n```') : wrap('`'));
  const here = (): RegExpMatchArray | undefined => [...ta.value.matchAll(LINK)].find((x) => (x.index ?? 0) < ta.selectionStart && ta.selectionStart < (x.index ?? 0) + x[0].length);
  const linkItems = (l: RegExpMatchArray): MenuItem[] => {
    const key = linkKey(l[1]), t = model.resolve(dir(), key), at = l.index ?? 0;
    return [
      ...(t ? [[`Open “${t.title}”`, () => openMemory(t)] as MenuItem] : []),
      [t ? 'Change link' : `“${human(key)}” doesn’t exist: pick a memory`, () => edit(at, at + l[0].length, '[[')],
      ['Remove link', () => edit(at, at + l[0].length, l[0].replace(LINK, (_, k: string) => human(linkKey(k))))],
    ];
  };
  const linkBtn = $<HTMLButtonElement>('[data-t="link"]', tools);
  const sync = (): void => { const l = here(); linkBtn.lastElementChild!.textContent = l ? 'Edit link' : 'Link'; linkBtn.classList.toggle('on', !!l); };
  for (const ev of ['keyup', 'click', 'input', 'focus']) ta.addEventListener(ev, sync);
  tools.onmousedown = (e) => e.preventDefault(); // keep the cursor where it is
  tools.onclick = (e) => {
    const t = (e.target as HTMLElement).closest<HTMLElement>('[data-t]')?.dataset.t, l = here();
    if (t === 'link') { if (l) menu(linkBtn, linkItems(l)); else link(); }
    else if (t === 'bold') bold();
    else if (t === 'code') code();
  };
  ta.addEventListener('contextmenu', (e) => {
    if (e.shiftKey) return; // the system menu (spelling suggestions)
    e.preventDefault();
    const l = here(), sel = picked(), [a, b] = [ta.selectionStart, ta.selectionEnd];
    const clip = (text: string): Promise<void> => navigator.clipboard.writeText(text).catch(() => toast('Press ⌘C to copy.'));
    menu(e, [
      ...(l ? [...linkItems(l), null] : []),
      ['Link a memory', link], ['Bold', bold], ['Code', code], null,
      ...(sel ? [['Cut', () => void clip(sel).then(() => edit(a, b, ''))], ['Copy', () => void clip(sel)]] as MenuItem[] : []),
      ['Paste', () => void navigator.clipboard.readText().then((x) => edit(a, b, x), () => toast('Press ⌘V to paste.'))],
    ]);
  });
}

function linkPicker(ta: HTMLTextAreaElement, dir: () => string, self?: Mem): void {
  const box = document.createElement('div');
  box.className = 'lpick'; box.id = ta.id + '-links'; box.setAttribute('role', 'listbox'); box.hidden = true;
  autogrow(ta);
  const row = ta.closest('.field')?.querySelector<HTMLElement>('.lrow') ?? ta.parentElement!;
  const scroller = row.closest<HTMLElement>('.dbody');
  if (scroller && row.classList.contains('sticky')) scroller.addEventListener('scroll', () => { // pinned: show its edge
    row.classList.toggle('stuck', scroller.scrollTop > 0 && row.getBoundingClientRect().top - scroller.getBoundingClientRect().top < 1);
  }, { passive: true });
  editTools(ta, dir, row);
  row.append(box); // drops down from the label row, which stays in view on long text
  ta.setAttribute('aria-autocomplete', 'list'); ta.setAttribute('aria-controls', box.id);
  let hits: Mem[] = [], sel = 0, from = -1;
  const close = (): void => { box.hidden = true; ta.removeAttribute('aria-activedescendant'); };
  const paint = (): void => {
    box.innerHTML = hits.length ? hits.map((t, i) => `<div role="option" id="${box.id}-${i}" data-i="${i}" aria-selected="${i === sel}"><span class="dot" style="background:${model.projectOf(t.project).color}"></span><span>${esc(t.title)}</span><span class="hint">${esc(model.projectOf(t.project).label)}</span></div>`).join('')
      : '<div class="hint none">No memory by that name. Finish typing ]] to link one you’ll write later.</div>';
    if (hits.length) ta.setAttribute('aria-activedescendant', `${box.id}-${sel}`);
  };
  const pick = (t: Mem): void => {
    const end = ta.selectionStart;
    ta.setRangeText(`[[${t.slug}]]`, from, ta.value.startsWith(']]', end) ? end + 2 : end, 'end');
    close(); ta.focus();
    ta.dispatchEvent(new Event('input', { bubbles: true })); // counts as an edit
  };
  ta.addEventListener('input', () => {
    const q = /\[\[([^\]\n|#]*)$/.exec(ta.value.slice(0, ta.selectionStart));
    if (!q) { close(); return; }
    from = q.index;
    const w = q[1].trim().toLowerCase(), d = dir();
    hits = model.linkable(d).filter((t) => t !== self && (!w || t.title.toLowerCase().includes(w) || t.stem.toLowerCase().includes(w)))
      .sort((a, b) => Number(dirOf(b.path) === d) - Number(dirOf(a.path) === d) || Number(b.title.toLowerCase().startsWith(w)) - Number(a.title.toLowerCase().startsWith(w)) || a.title.localeCompare(b.title))
      .slice(0, 8);
    sel = 0; paint(); box.hidden = false;
  });
  ta.addEventListener('keydown', (e) => {
    if (box.hidden) return;
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && hits.length) { e.preventDefault(); sel = (sel + (e.key === 'ArrowDown' ? 1 : hits.length - 1)) % hits.length; paint(); }
    else if ((e.key === 'Enter' || e.key === 'Tab') && hits.length) { e.preventDefault(); pick(hits[sel]); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopImmediatePropagation(); close(); } // closes only the list
  });
  box.onmousedown = (e) => { // mousedown, so the text box keeps focus
    e.preventDefault();
    const o = (e.target as HTMLElement).closest<HTMLElement>('[data-i]');
    if (o) pick(hits[Number(o.dataset.i)]);
  };
  ta.addEventListener('blur', close);
}

function openMemory(m: Mem, tab: 'edit' | 'history' = 'edit', at = ''): void {
  if (dirty && drawerOpen()) { if (ui.open?.id !== m.id) leave(() => openMemory(m, tab, at)); return; }
  ui.file = null;
  ui.open = m;
  const d = $('#drawer');
  const p = model.projectOf(m.project);
  const notices = [
    m.missing.length ? `<div class="notice"><div><b>Broken link.</b> Mentions ${m.missing.map((x) => '“' + esc(human(x)) + '”').join(', ')}, which ${m.missing.length > 1 ? "don't" : "doesn't"} exist.</div></div>` : '',
    m.badYaml ? '<div class="notice"><div><b>Header problem.</b> Claude may not read this memory’s description.</div></div>' : '',
  ].join('');
  d.innerHTML = `<div class="dhead"><span class="navb"><button id="dback" aria-label="Previous memory" title="Previous memory" disabled><svg viewBox="0 0 16 16"><path d="M10 3 5 8l5 5"/></svg></button><button id="dfwd" aria-label="Next memory" title="Next memory" disabled><svg viewBox="0 0 16 16"><path d="m6 3 5 5-5 5"/></svg></button></span><div class="where"><span class="dot" style="background:${p.color}"></span>${esc(p.label)}</div><button class="x" id="dx" aria-label="Close">×</button></div>
   ${TABS}
   <div class="dbody" id="d-edit">
    <div class="field"><input class="title-in" id="f-title" value="${esc(m.title)}" aria-label="Title"><span class="hint">Updated ${esc(fmtDate(m.modified))} · ${m.uses ? `Claude opened it ${m.uses === 1 ? 'once' : `${m.uses} times`}, most recently ${esc(ago(m.lastUsed))}` : 'Claude hasn’t opened it in 90 days'}</span><span class="hint" id="f-rename" hidden>Saving renames it${m.inn.length ? `, and updates ${m.inn.length === 1 ? 'the memory' : `the ${m.inn.length} memories`} that link to it` : ''}.</span></div>${notices}
    <div class="field"><label for="f-kind">Kind</label><select id="f-kind">${KIND[m.type] ? '' : `<option value="${esc(m.type)}" selected>${m.type ? esc(m.type) + ' · kept as it is' : 'No kind set'}</option>`}${Object.keys(KIND).map((k) => `<option value="${k}" ${k === m.type ? 'selected' : ''}>${KIND[k]} · ${KHELP[k]}</option>`).join('')}</select></div>
    <div class="field"><label for="f-desc">One-line summary</label><input id="f-desc" value="${esc(m.desc)}" placeholder="What Claude sees in its index"><span class="hint">Claude reads this line every session to decide whether to open the full memory.</span></div>
    <details class="field connbox" id="f-conn"${connOpen() ? ' open' : ''}><summary>Connections <span class="hint">${m.inn.length} mention this · ${m.out.length} mentioned here${m.missing.length ? ` · ${m.missing.length} broken` : ''}</span></summary><div class="conn"><button type="button" class="egexp" id="f-egexp" title="Expand: every connection, drag to move, zoom">⤢ Expand${m.inn.length > 7 || m.out.length > 7 ? ` · ${Math.max(0, m.inn.length - 7) + Math.max(0, m.out.length - 7)} more` : ''}</button>${egoSVG(m)}</div></details>
    <div class="field"><div class="lrow sticky"><label for="f-body">Details</label><span class="vtoggle" role="group" aria-label="Details view" id="f-mode" hidden><button type="button" data-m="read" aria-pressed="true">Read</button><button type="button" data-m="edit" aria-pressed="false">Edit</button></span></div>
     <div class="rich" id="f-read"><span class="hint">Loading…</span></div><textarea id="f-body" spellcheck="true" hidden placeholder="Add details. Type [[ to link another memory."></textarea>
</div>
    <div class="hint" style="font-family:var(--f-mono)">${esc(m.path.replace(model.state.home, '~'))}</div>
   </div>
   ${HIST}
   <div class="dfoot" id="f-edit"><button class="btn pri" id="dsave" disabled>Save</button>${m.project !== 'Global' ? '<button class="btn" id="dglobal">Use everywhere</button>' : ''}<span class="spacer"></span><button class="btn dng" id="ddel">Delete</button></div>`;
  d.classList.add('open');
  watchEdits(d);
  wireEgo(d, (t) => openMemory(t));
  $('#f-egexp').onclick = () => openEgo(m);
  $('#f-conn').addEventListener('toggle', () => { try { localStorage.setItem('cca-conn', $<HTMLDetailsElement>('#f-conn').open ? '1' : '0'); } catch { /* only a convenience */ } });
  $('#dx').onclick = () => leave(closeDrawer);
  $('#dback').onclick = () => history.back(); // popstate opens it (asking first if there are unsaved edits)
  $('#dfwd').onclick = () => history.forward();
  wireTabs(d, () => void showHistory(m.path, () => ui.open?.id === m.id, at));
  if (tab === 'history') $<HTMLButtonElement>('.dtabs button[data-tab="history"]', d).click();
  const ta = $<HTMLTextAreaElement>('#f-body'), rd = $('#f-read'), mode = $('#f-mode');
  const showDetails = (edit: boolean): void => {
    ta.hidden = !edit; rd.hidden = edit;
    $$('button', mode).forEach((b) => b.setAttribute('aria-pressed', String((b.dataset.m === 'edit') === edit)));
    if (edit) ta.focus();
    else rd.innerHTML = ta.value.trim() ? richHTML(ta.value, dirOf(m.path)) : '<span class="hint">No details yet. Click to add some.</span>';
  };
  mode.onclick = (e) => { const m = (e.target as HTMLElement).closest<HTMLElement>('[data-m]')?.dataset.m; if (m) showDetails(m === 'edit'); };
  rd.onclick = (e) => {
    const b = (e.target as HTMLElement).closest<HTMLElement>('.lchip');
    if (!b) { if (!getSelection()?.toString()) showDetails(true); return; } // selecting text to copy isn't a click to edit
    const t = b.dataset.id ? model.byId.get(b.dataset.id) : undefined;
    if (t) openMemory(t); else fixLink(b, m, b.dataset.miss ?? '');
  };
  linkPicker(ta, () => dirOf(m.path), m);
  // Escape while editing goes back to the read view (edits kept), not out of the drawer.
  ta.addEventListener('keydown', (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); showDetails(false); $<HTMLButtonElement>('[data-m="edit"]', mode).focus(); } });
  $<HTMLInputElement>('#f-title').addEventListener('input', (e) => { $('#f-rename').hidden = (e.target as HTMLInputElement).value.trim() === m.title; });
  // Save stays off until the text arrives, so it can never write a placeholder or blank the file.
  void loadFile(m.path).then((f) => {
    if (ui.open?.id !== m.id) return;
    ta.value = f.body.replace(/^\n+/, ''); // the server splits the header, exactly as saving puts it back
    mode.hidden = false; $<HTMLButtonElement>('#dsave').disabled = false;
    showDetails(false);
  }).catch(() => { if (ui.open?.id === m.id) rd.innerHTML = '<span class="err">Couldn’t read this memory. Close it and try again.</span>'; });
  $('#dsave').onclick = () => {
    const title = ($('#f-title') as HTMLInputElement).value.trim();
    void run('memory-save', {
      path: m.path, type: ($('#f-kind') as HTMLSelectElement).value,
      // send a name only if the title was edited, so a save never renames by accident
      name: title === m.title ? '' : title,
      description: ($('#f-desc') as HTMLInputElement).value, body: ta.value, seen: m.mtime,
    }).then((ok) => {
      const moved = ok && !ui.open && model.mems.find((x) => x.project === m.project && x.title === title);
      if (moved) openMemory(moved); // renamed: follow it to its new file
    });
  };
  $('#ddel').onclick = () => void run('memory-trash', { path: m.path }).then((ok) => ok && closeDrawer());
  const g = document.getElementById('dglobal');
  if (g) g.onclick = () => void run('memory-promote', { path: m.path }).then((ok) => ok && closeDrawer());
  $$('#v-all .row').forEach((r) => r.setAttribute('aria-selected', String(r.dataset.id === m.id)));
  markMap(m.id);
  syncURL();
  navMem[navPos] = m.id; // also on first load and after Back, which don't sync the address
  navButtons();
}

/** The editor panel's width: drag its left edge, or focus it and use the arrow keys; double-click
 *  resets it. It's kept in prefs, since the browser version's address (and storage) changes every run. */
const DW = 460;
const drawerWidth = (): number => parseInt(getComputedStyle(document.documentElement).getPropertyValue('--dw')) || DW;
function setDrawerWidth(w: number, save = false): number {
  const room = ($('#drawer').parentElement?.clientWidth ?? innerWidth) - 560; // keep the sidebar and some list in view
  const v = Math.round(Math.max(380, Math.min(w, Math.max(380, room))));
  document.documentElement.style.setProperty('--dw', v + 'px');
  const g = $('#dgrip');
  g.setAttribute('aria-valuenow', String(v)); g.setAttribute('aria-valuemax', String(Math.max(380, room)));
  if (save && (prefs.drawerWidth ?? DW) !== v) {
    prefs.drawerWidth = v === DW ? undefined : v;
    void savePrefs(prefs).catch((e: Error) => toast(e.message));
  }
  return v;
}
function wireGrip(): void {
  const g = $('#dgrip');
  g.onpointerdown = (e) => {
    e.preventDefault();
    g.setPointerCapture(e.pointerId);
    const right = ($('#drawer').parentElement ?? document.body).getBoundingClientRect().right;
    document.body.classList.add('resizing');
    g.onpointermove = (ev) => void setDrawerWidth(right - ev.clientX);
    g.onpointerup = g.onpointercancel = () => {
      g.onpointermove = null;
      document.body.classList.remove('resizing');
      setDrawerWidth(drawerWidth(), true);
    };
  };
  g.ondblclick = () => void setDrawerWidth(DW, true);
  g.onkeydown = (e) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    setDrawerWidth(drawerWidth() + (e.key === 'ArrowLeft' ? 40 : -40), true);
  };
}

/** Edit / History tabs in the open drawer. */
function wireTabs(d: HTMLElement, onHistory: () => void): void {
  $$<HTMLButtonElement>('.dtabs button', d).forEach((b) => (b.onclick = () => {
    const hist = b.dataset.tab === 'history';
    $$('.dtabs button', d).forEach((x) => x.setAttribute('aria-selected', String(x === b)));
    $('#d-edit').hidden = hist; $('#f-edit').hidden = hist;
    $('#d-hist').hidden = !hist; $('#f-hist').hidden = !hist;
    if (hist) onHistory();
  }));
}

const TABS = '<div class="dtabs" role="tablist"><button role="tab" data-tab="edit" aria-selected="true">Edit</button><button role="tab" data-tab="history" aria-selected="false">History</button></div>';
const HIST = `<div class="dbody" id="d-hist" hidden><div class="hint">Loading versions…</div></div>
   <div class="dfoot" id="f-hist" hidden><button class="btn pri" id="drestore" disabled>Restore this version</button><span class="spacer"></span><span class="hint">Your current text is kept as a version too</span></div>`;

/** An instructions file (CLAUDE.md, AGENTS.md, a rule or an import) in the drawer: edit it as text,
 *  or see its history. tab and at open History at the version nearest a time (from Activity). */
function openFile(e: Entry, tab: 'edit' | 'history' = 'edit', at = ''): void {
  if (dirty && drawerOpen()) { leave(() => openFile(e, tab, at)); return; }
  const path = e.path ?? '';
  ui.open = null;
  ui.file = path;
  markMap(null);
  const d = $('#drawer');
  const where = e.scope === 'user' ? 'Everywhere' : e.scope === 'import' ? 'Imported' : model.projectOf(e.project ?? '').label;
  const plugin = e.scope === 'plugin';
  d.innerHTML = `<div class="dhead"><div class="where">${esc(where)} · ${esc(e.kind === 'rule' ? 'rule' : 'instructions')} · ~${fmtN(Math.round((e.bytes ?? 0) / 4))} tokens</div><button class="x" id="dx" aria-label="Close">×</button></div>
   ${TABS}
   <div class="dbody" id="d-edit"><div class="field"><div class="title-in" style="padding:2px 0">${esc(e.name)}</div><span class="hint mono">${esc(path.replace(model.state.home, '~'))}</span></div>
    <div class="field" style="flex:1"><label for="i-body">${esc(e.name)}</label><textarea id="i-body" class="mono" style="min-height:360px" spellcheck="false" placeholder="Loading…" readonly></textarea>
    <span class="hint">Claude reads this ${e.scope === 'user' ? 'in every project' : 'in this project'} at the start of a session. Saving keeps the previous version.</span></div></div>
   ${HIST}
   <div class="dfoot" id="f-edit">${plugin ? '<span class="hint">Comes with a plugin; edits would be replaced when it updates.</span>' : '<button class="btn pri" id="isave" disabled>Save</button>'}<span class="spacer"></span><button class="btn" id="ifind">${platform.show}</button></div>`;
  d.classList.add('open');
  watchEdits(d);
  $('#dx').onclick = () => leave(closeDrawer);
  $('#ifind').onclick = () => void reveal(path);
  wireTabs(d, () => void showHistory(path, () => ui.file === path, at));
  const ta = $<HTMLTextAreaElement>('#i-body');
  let seen = e.modified ?? '';
  const save = document.getElementById('isave') as HTMLButtonElement | null;
  void loadFile(path).then((f) => {
    if (ui.file !== path) return;
    ta.value = f.content; ta.placeholder = ''; ta.readOnly = plugin; if (save) save.disabled = false;
  }).catch(() => { if (ui.file === path) ta.placeholder = 'Couldn’t read this file. Close it and try again.'; });
  if (save) save.onclick = () => void run('file-save', { path, content: ta.value, seen }).then((ok) => {
    if (ok) seen = model.state.entries.find((x) => x.path === path)?.modified ?? seen;
  });
  if (tab === 'history') $<HTMLButtonElement>('.dtabs button[data-tab="history"]', d).click();
  syncURL();
}

/** "just now", "12 min ago", "4:50 PM" today, else a date. */
const when = (iso: string): string => {
  const d = new Date(iso), mins = Math.round((Date.now() - d.getTime()) / 6e4);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins} min ago`;
  if (d.toDateString() === new Date().toDateString()) return d.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
};

const WHO: Record<string, [string, string]> = { you: ['You', 'You saved it'], claude: ['C', 'Claude changed it'], original: ['·', 'First saved copy'] };


/** The History tab of the open drawer: versions of path, newest first, with a diff and restore.
 *  still() says whether the drawer still shows this file; at preselects the version nearest a time. */
async function showHistory(path: string, still: () => boolean, at = ''): Promise<void> {
  const box = $('#d-hist');
  let vs: Version[];
  try { vs = await loadVersions(path); } catch { box.innerHTML = '<div class="hint">Versions couldn’t be loaded.</div>'; return; }
  if (!still()) return;
  if (!vs.length) { box.innerHTML = '<div class="empty">No earlier versions yet. This app keeps one every time this file changes, whether you or Claude changed it.</div>'; return; }
  let sel = 0;
  if (at) { // the version saved closest to that moment
    const t = Date.parse(at);
    vs.forEach((v, i) => { if (Math.abs(Date.parse(v.at) - t) < Math.abs(Date.parse(vs[sel].at) - t)) sel = i; });
  }
  const draw = (): void => {
    const v = vs[sel], older = vs[sel + 1];
    const cur = splitDoc(v.content), prev = older ? splitDoc(older.content) : null;
    const lines = prev ? hunks(diffLines(prev.body, cur.body)) : [];
    const was = new Map(prev?.fields ?? []);
    const head = prev ? [...cur.fields.filter(([k, x]) => was.get(k) !== x).map(([k, x]) => `<div class="hchg"><b>${k}</b>${was.get(k) ? `<span class="old">${esc(was.get(k))}</span><span aria-hidden="true">→</span>` : ''}<span>${esc(x)}</span></div>`),
      ...(prev.rest !== cur.rest ? ['<div class="hchg"><b>Header</b><span>Other details in the file’s header changed.</span></div>'] : [])].join('') : '';
    box.innerHTML = `<div style="display:grid;gap:2px">${vs.map((x, i) => { const [badge, label] = WHO[x.who] ?? WHO.original; return `<div class="ver" data-i="${i}" aria-selected="${i === sel}" tabindex="0"><span class="who ${x.who === 'you' ? 'you' : ''}">${badge}</span><b>${label}</b><small>${i === 0 ? 'Current' : ''}</small><span class="when">${esc(when(x.at))}</span></div>`; }).join('')}</div>
      <div class="field"><label>${older ? 'What changed in this version' : 'The first copy this app saved'}</label>${head}${older
        ? `<div class="diff2">${lines.length ? lines.map((l) => `<div class="${l.op === '-' ? 'r' : l.op === '+' ? 'a' : 'c'}">${esc(l.op + ' ' + l.text)}</div>`).join('') : `<div class="c">${head ? 'The text didn’t change.' : 'No changes.'}</div>`}</div>`
        : `${cur.fields.map(([k, x]) => `<div class="hchg"><b>${k}</b><span>${esc(x)}</span></div>`).join('')}<div class="rich">${richHTML(cur.body.slice(0, 4000), dirOf(path))}</div>`}</div>`;
    $$('.ver', box).forEach((x) => (x.onclick = () => { sel = Number(x.dataset.i); draw(); }));
    const restore = $<HTMLButtonElement>('#drestore');
    restore.disabled = sel === 0;
    restore.onclick = () => void run('restore', { path, version: vs[sel].path });
  };
  draw();
}

// ---------- activity ----------

/** What an Activity entry opens: the first changed file that still exists, on its History tab at
 *  that moment. Plugin and MCP changes, and files since deleted, have nothing to open. */
function openable(a: Activity): (() => void) | null {
  for (const c of a.changes ?? []) {
    const m = model.mems.find((x) => x.path === c.path);
    if (m) return () => openMemory(m, 'history', a.at);
    const e = model.state.entries.find((x) => x.path === c.path && (x.kind === 'instructions' || x.kind === 'rule'));
    if (e) return () => openFile(e, 'history', a.at);
  }
  return null;
}

async function renderActivity(): Promise<void> {
  heading('Activity', 'Every change, by you or Claude. Undo where a saved copy exists; History restores the rest.');
  const acts = await loadActivity().catch(() => []);
  if (ui.view !== 'activity') return;
  const day = (iso: string): string => {
    const d = new Date(iso), today = new Date();
    const diff = Math.round((new Date(today.toDateString()).getTime() - new Date(d.toDateString()).getTime()) / 864e5);
    return diff === 0 ? 'Today' : diff === 1 ? 'Yesterday' : d.toLocaleDateString('en-US', { weekday: 'long', month: 'short', day: 'numeric' });
  };
  const time = (iso: string): string => new Date(iso).toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' });
  let lastDay = '';
  const rows = acts.map((a) => {
    const d = day(a.at), head = d !== lastDay ? `<div class="actday">${esc(d)}</div>` : '';
    lastDay = d;
    const target = openable(a);
    return `${head}<div class="arow ${target ? 'go' : ''}" data-act="${esc(a.id)}" ${target ? 'tabindex="0" title="See what changed"' : ''} style="${a.undone ? 'opacity:.5' : ''}"><span class="who ${a.who === 'you' ? 'you' : ''}">${a.who === 'you' ? 'You' : 'C'}</span><div class="what">${esc(a.title)}${a.detail ? `<span>${esc(a.detail)}</span>` : ''}</div><time>${esc(time(a.at))}</time>${a.undone ? '<span class="hint">Undone</span>' : a.canUndo ? `<button class="btn sm" data-undo="${esc(a.id)}">Undo</button>` : '<span class="hint" title="There\'s no saved copy from before this change (Claude Code made it while this app wasn\'t running), or the file changed again since. Open it and use History to restore an earlier version.">Can\'t undo</span>'}</div>`;
  }).join('');
  $('#v-activity').innerHTML = `<div class="rv"><div class="act">${rows || '<div class="done">No changes yet. Edits you make here, and memory changes Claude makes while cca is open, show up in this list.</div>'}</div>
    <p class="hint" style="margin-top:14px">Claude’s own memory edits are recorded while cca is running, so you can always go back.</p></div>`;
  $$<HTMLButtonElement>('[data-undo]').forEach((b) => (b.onclick = (e) => { e.stopPropagation(); void undoChange(b.dataset.undo ?? ''); }));
  $$('#v-activity .arow.go').forEach((r) => {
    const a = acts.find((x) => x.id === r.dataset.act);
    const go = (): void => { const t = a && openable(a); if (t) t(); };
    r.onclick = go;
    r.onkeydown = (e) => { if (e.key === 'Enter') go(); };
  });
}

function openTool(r: Row): void {
  if (dirty && drawerOpen()) { leave(() => openTool(r)); return; }
  ui.file = null;
  ui.open = null;
  const d = $('#drawer');
  const e = r.entry;
  const per = (e?.meta?.perComponent ?? {}) as Record<string, [number, number]>;
  const comps = Object.entries(per).map(([k, [a, o]]) => `<div class="bl" style="grid-template-columns:minmax(0,1fr) auto auto"><span>${esc(k)}</span><span class="tok">~${fmtN(a)}</span><span class="tok sub2">~${fmtN(o)} when used</span></div>`).join('');
  d.innerHTML = `<div class="dhead"><div class="where">${esc(r.source)}</div><button class="x" id="dx" aria-label="Close">×</button></div>
   <div class="dbody"><div class="field"><div class="title-in" style="padding:2px 0">${esc(r.name)}</div>${e?.path && (e.kind === 'skill' || e.kind === 'command' || e.kind === 'agent') ? '' : `<span class="hint">${esc(r.desc)}</span>`}</div>
    <div class="budget" style="padding:14px"><div class="bl" style="border:0;grid-template-columns:minmax(0,1fr) auto"><span>Used in the last 90 days</span><span class="tok">${r.uses ? r.uses.toLocaleString() + ' times' : 'never'}</span></div>
     <div class="bl" style="grid-template-columns:minmax(0,1fr) auto"><span>Last used</span><span class="tok">${r.last ? esc(ago(r.last)) : '–'}</span></div>
     ${r.tokens !== undefined ? `<div class="bl" style="grid-template-columns:minmax(0,1fr) auto"><span>Cost ${esc(r.tokensLabel ?? '')}</span><span class="tok">~${fmtN(r.tokens)} tokens</span></div>` : ''}</div>
    ${comps ? `<div class="field"><label>What's inside</label><div class="blist">${comps}</div></div>` : ''}
    ${r.tags.some((t) => t.includes('••')) ? `<div class="field"><label>Keys</label><div class="comps">${r.tags.filter((t) => t.includes('••')).map((t) => `<span class="tag mono">${esc(t)}</span>`).join('')}</div><span class="hint">Values are never read by this app or sent to Claude.</span></div>` : ''}
    ${e?.kind === 'mcp' ? `<div class="field"><label>${e.meta?.url ? 'Connects to' : 'Runs'}</label><code class="cmdline ro">${esc(String(e.meta?.url ?? e.meta?.command ?? '–'))}</code><span class="hint">${esc(e.meta?.url ? 'Over the web (the address without its query, which can hold keys)' : 'A program on your computer (its arguments aren’t shown: they can hold keys)')}${e.path ? ` · defined in ${esc(e.path.replace(model.state.home, '~'))}` : ''}</span></div>` : ''}
    ${e?.path && (e.kind === 'skill' || e.kind === 'command' || e.kind === 'agent') ? `<div class="field"><label for="t-desc">When Claude uses it</label><input id="t-desc" placeholder="Loading…" readonly><span class="hint" id="t-deschint">Claude reads this to decide when to use it.</span></div>
     <div class="field" id="t-other" hidden><label>Other settings</label><code class="cmdline ro" id="t-otherv"></code><span class="hint">Change these with Edit as file.</span></div>
     <div class="field"><div class="lrow"><label for="t-ins">Instructions</label><button class="btn sm quiet" id="t-raw" hidden>Edit as file</button></div><textarea id="t-ins" placeholder="Loading…" readonly style="min-height:240px"></textarea><textarea id="t-body" class="mono" hidden style="min-height:320px" spellcheck="false" aria-label="The whole file"></textarea></div>` : ''}
    ${r.readonly ? `<div class="notice"><div>Managed by ${esc(r.source.replace('From plugin · ', 'the plugin ').replace('claude.ai connector', 'claude.ai settings'))}. Turn it off there.</div>${r.source.startsWith('From plugin') ? '<button class="btn sm" id="toplugins">Open Plugins</button>' : ''}</div>` : ''}
   </div>
   <div class="dfoot">${r.readonly || !e?.path || e.kind === 'plugin' || e.kind === 'mcp' ? '' : '<button class="btn pri" id="tsave" disabled>Save</button>'}${e?.scope === 'project' && (e.kind === 'skill' || e.kind === 'command') ? '<button class="btn" id="tglobal">Use everywhere</button>' : ''}<span class="spacer"></span>${r.readonly ? '' : `<button class="btn dng" id="tdel">${e?.kind === 'plugin' ? 'Uninstall' : 'Remove'}</button>`}</div>`;
  d.classList.add('open');
  d.setAttribute('aria-label', `${r.name} details`);
  watchEdits(d);
  $('#dx').onclick = () => leave(closeDrawer);
  document.getElementById('toplugins')?.addEventListener('click', () => leave(() => { closeDrawer(); setView('plugins'); }));
  const ta = document.getElementById('t-body') as HTMLTextAreaElement | null; // the whole file (Edit as file)
  const save = document.getElementById('tsave') as HTMLButtonElement | null;
  let doc: ToolDoc | null = null;
  const desc = document.getElementById('t-desc') as HTMLInputElement | null, ins = document.getElementById('t-ins') as HTMLTextAreaElement | null;
  const content = (): string => (!ta || !doc ? '' : !ta.hidden ? ta.value : composeTool(doc, desc?.value ?? '', ins?.value ?? ''));
  if (ta && desc && ins && e?.path) void loadFile(e.path).then((f) => {
    doc = splitTool(f.content);
    ta.value = f.content;
    desc.value = doc.desc ?? ''; desc.placeholder = doc.desc === null ? '' : 'Say when Claude should use it';
    if (doc.desc === null) $('#t-deschint').textContent = doc.di === -2 ? 'This file has no description.' : 'Written in a form this editor can’t change safely: use Edit as file.';
    ins.value = doc.body; ins.placeholder = '';
    if (doc.others.length) { $('#t-other').hidden = false; $('#t-otherv').textContent = doc.others.join('\n'); }
    desc.readOnly = r.readonly || doc.desc === null; ins.readOnly = r.readonly; ta.readOnly = r.readonly;
    const raw = $<HTMLButtonElement>('#t-raw');
    raw.hidden = r.readonly;
    raw.onclick = () => { // one source of truth at a time: carry the form's edits into the file text
      const toFile = ta.hidden;
      if (toFile) ta.value = content(); else { doc = splitTool(ta.value); desc.value = doc.desc ?? ''; ins.value = doc.body; }
      ta.hidden = !toFile; ins.hidden = toFile; desc.closest<HTMLElement>('.field')!.hidden = toFile; $('#t-other').hidden = toFile || !doc?.others.length;
      raw.textContent = toFile ? 'Back to the form' : 'Edit as file';
    };
    if (save) save.disabled = false;
  }).catch(() => { if (ins) ins.placeholder = 'Couldn’t read this file. Close it and try again.'; });
  let seen = e?.modified ?? '';
  if (save) save.onclick = () => void (ta && doc && e?.path ? run('file-save', { path: e.path, content: content(), seen }).then((ok) => {
    if (ok) seen = model.state.entries.find((x) => x.path === e.path)?.modified ?? seen; // so a second save isn't a false conflict
  }) : Promise.resolve(false));
  const mg = document.getElementById('tglobal');
  if (mg && e) mg.onclick = () => void run('make-global', { paths: model.state.entries.filter((x) => x.kind === e.kind && x.name === e.name && x.scope === 'project').map((x) => x.path) }).then((ok) => ok && closeDrawer());
  const del = document.getElementById('tdel');
  if (del && e) del.onclick = () => void run(e.kind === 'plugin' ? 'plugin-uninstall' : e.kind === 'mcp' ? 'mcp-remove' : 'file-trash',
    e.kind === 'plugin' ? { id: e.meta?.id } : e.kind === 'mcp' ? { name: e.name, scope: e.scope, project: e.project ?? '' } : { path: e.path }).then((ok) => ok && closeDrawer());
}

/** Where a project's memories live (or will, for its first one): '' for a folder that's gone. */
function memoryDirFor(project: string): string {
  if (project === GLOBAL) return model.state.globalMemoryDir; // the server owns Claude Code's folder layout
  const p = model.state.projects.find((x) => x.path === project);
  return p && (p.exists || p.global) && !p.worktree ? p.memoryDir : '';
}

function openNewMemory(): void {
  if (dirty && drawerOpen()) { leave(openNewMemory); return; }
  ui.open = null;
  ui.file = null;
  markMap(null);
  // any project Claude has run in, defaulting to the current scope
  const targets = [model.projectOf(GLOBAL), ...model.projects.filter((p) => p.key !== GLOBAL && memoryDirFor(p.key) && (!p.hidden || p.key === ui.proj))];
  const start = targets.some((p) => p.key === ui.proj) ? ui.proj : GLOBAL;
  const d = $('#drawer');
  d.innerHTML = `<div class="dhead"><div class="where">New memory</div><button class="x" id="dx" aria-label="Close">×</button></div>
   <div class="dbody">
    <div class="field"><input class="title-in" id="n-title" placeholder="Title" aria-label="Title" aria-describedby="n-err"><span class="err" id="n-err" hidden>Give the memory a title.</span></div>
    <div class="field"><label for="n-proj">Loads in</label><select id="n-proj">${targets.map((p) => `<option value="${esc(p.key)}" ${p.key === start ? 'selected' : ''}>${esc(p.key === GLOBAL ? 'Everywhere (every project)' : p.label)}</option>`).join('')}</select></div>
    <div class="field"><label for="n-kind">Kind</label><select id="n-kind">${Object.keys(KIND).map((k) => `<option value="${k}" ${k === 'feedback' ? 'selected' : ''}>${KIND[k]} · ${KHELP[k]}</option>`).join('')}</select></div>
    <div class="field"><label for="n-desc">One-line summary</label><input id="n-desc" placeholder="What Claude sees in its index"></div>
    <div class="field"><div class="lrow sticky"><label for="n-body">Details</label></div><textarea id="n-body" placeholder="The rule or fact. Then why it matters, and how to apply it."></textarea></div>
   </div>
   <div class="dfoot"><button class="btn pri" id="ncreate">Create</button><span class="spacer"></span></div>`;
  d.classList.add('open');
  watchEdits(d);
  $('#dx').onclick = () => leave(closeDrawer);
  ($('#n-title') as HTMLInputElement).focus();
  $('#n-title').addEventListener('input', () => { $('#n-err').hidden = true; $('#n-title').removeAttribute('aria-invalid'); });
  linkPicker($<HTMLTextAreaElement>('#n-body'), () => memoryDirFor($<HTMLSelectElement>('#n-proj').value));
  $('#ncreate').onclick = () => {
    const title = ($('#n-title') as HTMLInputElement).value.trim();
    const err = $('#n-err'), ti = $<HTMLInputElement>('#n-title');
    err.hidden = !!title; ti.setAttribute('aria-invalid', String(!title));
    if (!title) { ti.focus(); return; }
    const dir = memoryDirFor($<HTMLSelectElement>('#n-proj').value);
    void run('memory-create', { dir, title, type: ($('#n-kind') as HTMLSelectElement).value,
      description: ($('#n-desc') as HTMLInputElement).value, body: ($('#n-body') as HTMLTextAreaElement).value }).then((ok) => {
      const m = ok && model.mems.find((x) => x.title === title && x.path.startsWith(dir + '/'));
      if (m) openMemory(m); else if (ok) closeDrawer(); // show what was made, where it was made
    });
  };
  syncURL();
}

/** The add panel: paste an MCP snippet, plugin commands or a SKILL.md, or write a skill. */
function openAddDrawer(start: 'mcp' | 'plugin' | 'skill' = 'mcp'): void {
  if (dirty && drawerOpen()) { leave(() => openAddDrawer(start)); return; }
  ui.open = null;
  ui.file = null;
  markMap(null);
  const d = $('#drawer');
  openAdd(d, {
    model: () => model, scope: () => ui.proj, readOnly: () => model.state.readOnly,
    done: async (msg, activity) => {
      setDirty(false);
      closeDrawer();
      await refresh();
      toast(msg, activity ? () => void undoChange(activity) : undefined);
    },
  }, start);
  watchEdits(d);
  $('#dx').onclick = () => leave(closeDrawer);
  syncURL();
}

function closeDrawer(sync = true): void {
  ui.open = null;
  ui.file = null;
  markMap(null);
  $('#drawer').classList.remove('open');
  $$('#v-all .row').forEach((r) => r.setAttribute('aria-selected', 'false'));
  if (sync) syncURL();
}

// ---------- theme ----------

function theme(): void {
  const root = document.documentElement;
  const pop = $('#setpop');
  const store = (k: string, v?: string): string | null => {
    try { if (v === '') localStorage.removeItem(k); else if (v !== undefined) localStorage.setItem(k, v); return localStorage.getItem(k); } catch { return null; }
  };
  const paint = (): void => {
    $$('.theme button', pop).forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.th === (root.dataset.theme ?? ''))));
    $$('.accents button', pop).forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.h === root.style.getPropertyValue('--hue'))));
    if (model) { render(); if (ui.open && !dirty) openMemory(ui.open); } // the map and charts read colors at draw time
  };
  const saved = store('cca-theme');
  if (saved) root.dataset.theme = saved;
  root.style.setProperty('--hue', store('cca-hue') ?? '290');
  $$('.theme button', pop).forEach((b) => (b.onclick = () => {
    if (b.dataset.th) root.dataset.theme = b.dataset.th; else delete root.dataset.theme;
    store('cca-theme', b.dataset.th ?? ''); paint();
  }));
  $$('.accents button', pop).forEach((b) => (b.onclick = () => { root.style.setProperty('--hue', b.dataset.h ?? '290'); store('cca-hue', b.dataset.h); paint(); }));
  matchMedia('(prefers-color-scheme: light)').addEventListener('change', paint);

  const close = (): void => { pop.hidden = true; $$('.gear').forEach((g) => g.setAttribute('aria-expanded', 'false')); };
  const open = (g: HTMLElement): void => {
    pop.hidden = false;
    g.setAttribute('aria-expanded', 'true');
    const r = g.getBoundingClientRect();
    pop.style.left = Math.max(8, Math.min(r.left, innerWidth - pop.offsetWidth - 8)) + 'px';
    pop.style.top = (r.top > innerHeight / 2 ? r.top - pop.offsetHeight - 6 : r.bottom + 6) + 'px';
    pop.querySelector<HTMLButtonElement>('.theme button[aria-pressed="true"]')?.focus();
  };
  document.addEventListener('click', (e) => {
    const g = (e.target as Element).closest<HTMLElement>('.gear');
    if (g) { if (pop.hidden) open(g); else close(); } else if (!pop.hidden && !pop.contains(e.target as Node)) close();
  });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !pop.hidden) { const g = document.querySelector<HTMLElement>('.gear[aria-expanded="true"]'); close(); g?.focus(); } });
  addEventListener('resize', close);
  paint();
}

// ---------- boot ----------

// Anything cut off with an ellipsis shows its full text on hover.
addEventListener('mouseover', (e) => {
  const el = e.target as HTMLElement;
  // Only a single line actually cut off with an ellipsis, and only its own short text: a list or a
  // scrolling panel also overflows, and its whole contents make a useless tooltip.
  if (el.title || el.closest('input,textarea,select') || getComputedStyle(el).textOverflow !== 'ellipsis') return;
  const text = (el.textContent ?? '').trim();
  if (el.scrollWidth > el.clientWidth + 1 && text.length <= 300) el.title = text;
});

function boot(): void {
  wireToast();
  $$('#nav button, #nav2 button').forEach((b) => (b.onclick = () => {
    setView(b.dataset.v as View);
  }));
  $('#mnav').innerHTML = $$('#nav button, #nav2 button').map((b) => `<button data-v="${b.dataset.v}">${esc(b.textContent?.replace(/\d+$/, '').trim())}</button>`).join('');
  $$('#mnav button').forEach((b) => (b.onclick = () => setView(b.dataset.v as View)));
  const mnav = $('#mnav'), fade = (): void => { mnav.classList.toggle('more', mnav.scrollLeft + mnav.clientWidth < mnav.scrollWidth - 4); };
  mnav.addEventListener('scroll', fade, { passive: true }); addEventListener('resize', fade); requestAnimationFrame(fade);
  $('#mnav').insertAdjacentHTML('afterbegin', '<select id="mscope" aria-label="Scope"></select>');
  $('#mnav').insertAdjacentHTML('beforeend', `<button class="gear" aria-label="Settings" aria-haspopup="dialog" aria-expanded="false">${$('#gear').innerHTML}</button>`);
  $('#newmem').onclick = openNewMemory;
  $('#addbtn').onclick = () => openAddDrawer(ui.view === 'plugins' ? 'plugin' : ui.view === 'skills' ? 'skill' : 'mcp');
  ($('#q') as HTMLInputElement).oninput = (e) => { ui.q = (e.target as HTMLInputElement).value.toLowerCase(); render(); };
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !document.querySelector('.palback, .menu') && $('#setpop').hidden) {
      if (selected.size) { clearSelection(); render(); } else if (drawerOpen()) leave(closeDrawer);
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
      e.preventDefault();
      openPalette(model, {
        openMemory: (m) => openMemory(m), // in place, over whatever page is open
        openProject: (key) => { ui.proj = key; setView('all'); },
        newMemory: openNewMemory,
        add: () => openAddDrawer('mcp'),
        openPath: (path) => { // the right editor, in place
          const e = model.state.entries.find((x) => x.path === path);
          const row = [...skills(model), ...agents(model)].find((r) => r.entry?.path === path);
          if (row) openTool(row);
          else if (e && (e.kind === 'instructions' || e.kind === 'rule')) openFile(e);
          else setView('ins');
        },
      });
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 's' && $('#drawer').classList.contains('open')) {
      e.preventDefault();
      document.querySelector<HTMLButtonElement>('#dsave, #tsave, #isave, #ncreate, #a-install, #s-create, #capply, #drawer [data-add]')?.click();
    }
  });
  addEventListener('resize', () => { setDrawerWidth(prefs.drawerWidth ?? DW); if (ui.view === 'map') VIEW_RENDER.map?.(); });
  wireGrip();
  theme();
  void refresh().then(applyURL);
  subscribe(() => void refresh());
}

boot();
