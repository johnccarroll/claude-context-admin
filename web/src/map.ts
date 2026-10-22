// The memory map: a 3D view (default) and a 2D view of the same scene, behind one toolbar.
// The 3D view is its own chunk, loaded only when it opens (see map3d.ts).
import { duplicatePairs, GLOBAL, human, KIND, type Mem, type Model } from './model';
import { layout, layoutKey, type Box, type LayoutLink, type LayoutNode, type Pt } from './maplogic';
import { create as create2d } from './map2d';
import { $, cssVar, esc } from './ui';

// ---------- The scene both views draw ----------

export interface MNode {
  id: string; mem?: Mem; project: string; title: string; kind: string;
  name: string;       // the title as drawn on the map: long ones end in an ellipsis
  ghost: boolean;     // a [[link]] to a memory that doesn't exist
  deg: number; r: number;
}
export interface MLink { s: string; t: string; cross: boolean; ghost?: boolean; twin?: boolean }
export interface Scene {
  key: string;                        // memories and links; same key = same layout
  nodes: MNode[]; links: MLink[]; byId: Map<string, MNode>;
  near: Map<string, Set<string>>;     // each memory with what it links to, from, and its twins
  projects: string[]; pos: Map<string, Pt>; shown: Set<string>;
  label: (p: string) => string; color: (p: string) => string; gone: (p: string) => boolean;
  count: (p: string) => number;
}

export interface Hooks {
  open: (id: string) => void;
  hover: (id: string | null, x?: number, y?: number) => void;
  announce: (text: string) => void;
  boxes: () => Box[];                 // floating controls, in the map's coordinates
  freeWidth: () => number;            // the map's width not covered by the editor
  reduce: () => boolean;
}
export interface MapView {
  update(scene: Scene): void;
  mark(id: string | null): void;
  zoom(factor: number): void;
  reset(): void;
  active(on: boolean): void;          // false: the map isn't on screen, so draw nothing
  theme(): void;
  locate(id: string): [number, number] | null; // a memory's position in the map, for the recorded tour
  destroy(): void;
}

let laid: { key: string; pos: Map<string, Pt> } | null = null;
const short = (t: string): string => (t.length > 34 ? t.slice(0, 32).trimEnd() + '…' : t);

function buildScene(model: Model, visible: (m: Mem) => boolean): Scene {
  const mems = model.mems;
  const nodes: MNode[] = mems.map((m) => ({ id: m.id, mem: m, project: m.project, title: m.title, name: short(m.title), kind: m.type, ghost: false, deg: m.out.length + m.inn.length, r: 0 }));
  const links: MLink[] = [];
  for (const m of mems) for (const t of m.out) links.push({ s: m.id, t: t.id, cross: m.project !== t.project });
  const ghosts = new Set<string>();
  for (const m of mems) for (const k of m.missing) {
    const id = `ghost:${m.project}/${k}`;
    if (!ghosts.has(id)) ghosts.add(id), nodes.push({ id, project: m.project, title: human(k), name: short(human(k)), kind: '', ghost: true, deg: 0, r: 0 });
    links.push({ s: m.id, t: id, cross: false, ghost: true });
  }
  for (const [a, b] of duplicatePairs(mems)) links.push({ s: a.id, t: b.id, cross: true, twin: true });
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const near = new Map(nodes.map((n) => [n.id, new Set([n.id])]));
  for (const l of links) { near.get(l.s)!.add(l.t); near.get(l.t)!.add(l.s); }
  for (const n of nodes) { if (n.ghost) n.deg = 1; n.r = n.ghost ? 3.2 : 3.6 + Math.sqrt(n.deg) * 1.9; }
  const projects = [...new Set(nodes.map((n) => n.project))].sort((a, b) => (a === GLOBAL ? -1 : b === GLOBAL ? 1 : a.localeCompare(b)));
  // Layout covers every memory, not the filtered ones, so a filter never moves anything.
  const ln: LayoutNode[] = nodes.map((n) => ({ id: n.id, project: n.project, r: n.r }));
  const ll: LayoutLink[] = links.filter((l) => !l.twin).map((l) => ({ s: l.s, t: l.t, cross: l.cross, ghost: l.ghost }));
  const key = layoutKey(ln, ll);
  if (laid?.key !== key) laid = { key, pos: layout(ln, ll, GLOBAL, laid?.pos) };
  const shown = new Set<string>();
  for (const m of mems) if (visible(m)) shown.add(m.id);
  for (const l of links) if (l.ghost && shown.has(l.s)) shown.add(l.t);
  const counts = new Map<string, number>();
  for (const n of nodes) if (!n.ghost && shown.has(n.id)) counts.set(n.project, (counts.get(n.project) ?? 0) + 1);
  return {
    key, nodes, links, byId, near, projects, pos: laid.pos, shown,
    label: (p) => model.projectOf(p).label,
    color: (p) => model.projectOf(p).color,
    gone: (p) => p !== GLOBAL && !model.projectOf(p).exists,
    count: (p) => counts.get(p) ?? 0,
  };
}

// ---------- Drawing helpers both views use ----------

/** Theme colours for canvas code, re-read when the theme or accent changes. */
export interface Tokens { fg: string; muted: string; faint: string; ground: string; accent: string; dark: boolean; ui: string; mono: string }
export function tokens(): Tokens {
  const ground = cssVar('var(--surface)');
  return {
    fg: cssVar('var(--fg)'), muted: cssVar('var(--muted)'), faint: cssVar('var(--faint)'), ground, accent: cssVar('var(--accent)'),
    dark: luminance(ground) < 0.3, ui: cssVar('var(--f-ui)') || 'system-ui', mono: cssVar('var(--f-mono)') || 'ui-monospace',
  };
}
function rgbOf(c: string): [number, number, number] {
  if (c.startsWith('#')) { const h = c.length === 4 ? c.slice(1).split('').map((x) => x + x).join('') : c.slice(1, 7); const i = parseInt(h, 16); return [(i >> 16) & 255, (i >> 8) & 255, i & 255]; }
  const m = c.match(/[\d.]+/g); return m ? [+m[0], +m[1], +m[2]] : [128, 128, 128];
}
const luminance = (c: string): number => { const [r, g, b] = rgbOf(c); return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255; };
export const rgba = (c: string, a: number): string => { const [r, g, b] = rgbOf(c); return `rgba(${r},${g},${b},${Math.max(0, Math.min(1, a))})`; };
export const mix = (c: string, to: string, t: number, a = 1): string => {
  const [r1, g1, b1] = rgbOf(c), [r2, g2, b2] = rgbOf(to);
  return `rgba(${Math.round(r1 + (r2 - r1) * t)},${Math.round(g1 + (g2 - g1) * t)},${Math.round(b1 + (b2 - b1) * t)},${a})`;
};

/** A project's name: a tinted capsule in tracked capitals with its count, so it never reads as
 *  one of its memories (those are plain text). */
// Below 500 px wide the capsules shrink and drop the count, so they don't crowd a phone screen.
const compact = (c: CanvasRenderingContext2D): boolean => c.canvas.clientWidth > 0 && c.canvas.clientWidth < 500;
export function chipSize(c: CanvasRenderingContext2D, T: Tokens, label: string, count: number): [number, number, number] {
  const small = compact(c);
  c.font = `600 ${small ? 9.5 : 10.5}px ${T.ui}`; c.letterSpacing = '0.08em';
  const w1 = c.measureText(label.toUpperCase()).width;
  c.font = `500 10.5px ${T.mono}`; c.letterSpacing = '0px';
  const w2 = small ? 0 : c.measureText(String(count)).width + 7;
  return [w1 + w2 + (small ? 14 : 18), small ? 18 : 20, w1];
}
export function drawChip(c: CanvasRenderingContext2D, T: Tokens, label: string, count: number, x: number, y: number, color: string, a: number, dark: boolean): Box {
  const [w, h, w1] = chipSize(c, T, label, count), small = compact(c), pad = small ? 7 : 9;
  // Kept inside the map's frame, so a project near the edge is never cut off.
  const cw = c.canvas.clientWidth || Infinity;
  x = Math.max(w / 2 + 4, Math.min(cw - w / 2 - 4, x));
  const x0 = x - w / 2, y0 = y - h / 2;
  c.beginPath(); c.roundRect(x0, y0, w, h, h / 2);
  c.fillStyle = dark ? `rgba(10,10,14,${0.82 * a})` : `rgba(255,255,255,${0.92 * a})`; c.fill();
  c.fillStyle = rgba(color, (dark ? 0.2 : 0.12) * a); c.fill();
  c.lineWidth = 1; c.strokeStyle = rgba(color, (dark ? 0.55 : 0.5) * a); c.stroke();
  c.textAlign = 'left'; c.textBaseline = 'middle';
  // The name keeps full contrast even when its capsule is dimmed: only the capsule recedes.
  const ta = Math.max(a, 0.85);
  c.font = `600 ${small ? 9.5 : 10.5}px ${T.ui}`; c.letterSpacing = '0.08em';
  c.fillStyle = dark ? mix(color, '#ffffff', 0.55, ta) : mix(color, '#000000', 0.35, ta); c.fillText(label.toUpperCase(), x0 + pad, y + 0.5);
  c.letterSpacing = '0px';
  if (!small) {
    c.font = `500 10.5px ${T.mono}`;
    c.fillStyle = dark ? `rgba(255,255,255,${0.62 * ta})` : `rgba(20,20,30,${0.62 * ta})`; c.fillText(String(count), x0 + pad + w1 + 7, y + 0.5);
  }
  return [x0, y0, x0 + w, y0 + h];
}

/** The hover card: what the memory is, where, how connected. Everything escaped. */
export function tipHTML(s: Scene, n: MNode): string {
  const twins = [...(s.near.get(n.id) ?? [])].filter((id) => s.links.some((l) => l.twin && ((l.s === n.id && l.t === id) || (l.t === n.id && l.s === id))));
  const also = twins.map((id) => s.label(s.byId.get(id)!.project));
  return `<b>${esc(n.title)}</b><span>${esc(n.ghost ? 'Broken link' : KIND[n.kind] ?? n.kind)} · ${esc(s.label(n.project))} · ${n.ghost ? 'no memory has this name' : `${n.deg} connection${n.deg === 1 ? '' : 's'}`}</span>` +
    (n.mem?.desc ? `<div style="margin-top:4px">${esc(n.mem.desc)}</div>` : '') + (also.length ? `<div style="margin-top:4px">Also written in ${esc(also.join(', '))}</div>` : '');
}

// ---------- The view switch, toolbar and public API ----------

type Kind = '2d' | '3d';
const KEY = 'cca-map-view';
const reduce = matchMedia('(prefers-reduced-motion: reduce)');
let gl: boolean | null = null;
/** WebGL2 without a major performance caveat: software rendering on managed or virtual machines
 *  is too slow for 3D, so those get 2D. */
function webgl(): boolean {
  if (gl === null) { try { gl = !!document.createElement('canvas').getContext('webgl2', { failIfMajorPerformanceCaveat: true }); } catch { gl = false; } }
  return gl;
}
const saved = (): Kind | null => { try { const v = localStorage.getItem(KEY); return v === '2d' || v === '3d' ? v : null; } catch { return null; } };
const save = (k: Kind): void => { try { localStorage.setItem(KEY, k); } catch { /* private window: not remembered */ } };
function wanted(): Kind {
  if (!webgl()) return '2d';
  return saved() ?? (reduce.matches ? '2d' : '3d');
}

let kind: Kind | null = null, view: MapView | null = null, scene: Scene | null = null, marked: string | null = null;
let openFn: (m: Mem) => void = () => {}, closeFn: () => void = () => {};
let loading: Promise<void> | null = null, wired = false, onScreen = true, boxes: Box[] = [];

function hooks(): Hooks {
  const tip = $('#tip'), host = $('#map');
  return {
    open: (id) => { const m = scene?.byId.get(id)?.mem; if (m) openFn(m); },
    hover: (id, x = 0, y = 0) => {
      const n = id ? scene?.byId.get(id) : undefined;
      if (!n || !scene) { tip.hidden = true; return; }
      tip.innerHTML = tipHTML(scene, n); tip.hidden = false;
      const b = host.getBoundingClientRect();
      tip.style.left = Math.min(x + 14, b.width - 300) + 'px'; tip.style.top = Math.min(y + 18, b.height - 120) + 'px';
    },
    announce: (t) => { $('#msay').textContent = t; },
    boxes: () => boxes,
    freeWidth: () => { const w = host.clientWidth; return document.getElementById('drawer')?.classList.contains('open') && w > 760 ? w - (parseInt(getComputedStyle(document.documentElement).getPropertyValue('--dw')) || 460) : w; },
    reduce: () => reduce.matches,
  };
}

/** The floating controls' boxes, so names steer around them; read on resize, not every frame. */
function measureBoxes(): void {
  const host = $('#map'), hb = host.getBoundingClientRect();
  boxes = [...host.querySelectorAll<HTMLElement>('.mapbar, .mapseg, .mapkey.open')].map((el) => { const r = el.getBoundingClientRect(); return [r.left - hb.left - 6, r.top - hb.top - 6, r.right - hb.left + 6, r.bottom - hb.top + 6] as Box; });
}

async function show(k: Kind): Promise<void> {
  if (kind === k && view) return;
  kind = k;
  const host = $('#mhost');
  view?.destroy(); view = null; host.replaceChildren();
  $('#map').classList.toggle('is3d', k === '3d');
  const seg = $('#mdim'); seg.dataset.v = k;
  for (const b of seg.querySelectorAll('button')) b.setAttribute('aria-checked', String(b.dataset.v === k));
  setKey(false);
  const make = k === '3d' ? (await import('./map3d')).create : create2d;
  if (kind !== k) return; // switched again while loading
  view = make(host, hooks());
  view.active(onScreen);
  if (scene) view.update(scene);
  view.mark(marked);
  host.classList.remove('fade'); void host.offsetWidth; host.classList.add('fade');
}

function setKey(open: boolean): void {
  $('#mkey').classList.toggle('open', open);
  $('#mkeyb').setAttribute('aria-expanded', String(open));
  measureBoxes();
}

function wire(): void {
  wired = true;
  // scripts/tour.sh records the README tour: with ?tour it can ask where a memory is on screen.
  if (new URLSearchParams(location.search).has('tour')) {
    (window as unknown as { __mapLocate: (stem: string) => [number, number] | null }).__mapLocate = (stem) => {
      const n = scene?.nodes.find((x) => x.id.endsWith('/' + stem + '.md'));
      const p = n && view?.locate(n.id), b = $('#map').getBoundingClientRect();
      return p ? [b.left + p[0], b.top + p[1]] : null;
    };
  }
  const map = $('#map'), btip = $('#mtip');
  // 3D needs a usable GPU: otherwise the option is there but disabled, with the reason.
  const b3 = $<HTMLButtonElement>('#mdim [data-v="3d"]');
  if (!webgl()) { b3.disabled = true; b3.dataset.tip = '3D needs graphics acceleration, which this browser has turned off'; }
  $('#mdim').addEventListener('click', (e) => {
    const b = (e.target as HTMLElement).closest<HTMLButtonElement>('button');
    if (!b || b.disabled || b.dataset.v === kind) return;
    save(b.dataset.v as Kind); void show(b.dataset.v as Kind);
  });
  $('#mzin').onclick = () => view?.zoom(1.4);
  $('#mzout').onclick = () => view?.zoom(1 / 1.4);
  $('#mreset').onclick = () => { if (marked) closeFn(); view?.reset(); };
  const keyb = $('#mkeyb');
  keyb.onclick = (e) => { e.stopPropagation(); hideTip(); setKey(!$('#mkey').classList.contains('open')); };
  addEventListener('pointerdown', (e) => { if (!(e.target as HTMLElement).closest('#mkey, #mkeyb')) setKey(false); });
  addEventListener('keydown', (e) => { if (e.key === 'Escape' && $('#mkey').classList.contains('open')) setKey(false); });

  // Control tooltips: the first waits a moment, the next ones show at once while moving along.
  let timer = 0, warm = 0, on: HTMLElement | null = null;
  const showTip = (b: HTMLElement, instant: boolean): void => {
    // Left of the zoom stack, below the 2D | 3D toggle: always toward the map, never off its edge.
    const r = b.getBoundingClientRect(), stack = !!b.closest('.mapbar');
    btip.textContent = b.dataset.tip ?? '';
    btip.classList.toggle('instant', instant);
    const w = btip.offsetWidth, h = btip.offsetHeight;
    btip.style.setProperty('--o', stack ? 'right center' : 'top center');
    btip.style.left = (stack ? r.left - w - 8 : Math.min(innerWidth - w - 8, r.left + r.width / 2 - w / 2)) + 'px';
    btip.style.top = (stack ? r.top + r.height / 2 - h / 2 : r.bottom + 8) + 'px';
    btip.classList.add('on'); on = b;
  };
  function hideTip(): void { clearTimeout(timer); if (on) warm = performance.now(); on = null; btip.classList.remove('on'); }
  for (const b of document.querySelectorAll<HTMLElement>('#map .mapbar [data-tip], #mdim [data-tip]')) {
    b.addEventListener('pointerenter', (e) => {
      if (e.pointerType !== 'mouse') return;
      clearTimeout(timer);
      if (on || performance.now() - warm < 500) showTip(b, true); else timer = window.setTimeout(() => showTip(b, false), 450);
    });
    b.addEventListener('pointerleave', hideTip);
    b.addEventListener('pointerdown', hideTip);
    b.addEventListener('focus', () => { if (b.matches(':focus-visible')) showTip(b, false); });
    b.addEventListener('blur', hideTip);
  }

  // Draw nothing while the map is off screen (another page, a hidden tab, a closed window).
  const vis = (): void => {
    const v = !document.hidden && !$('#v-map').hidden && map.offsetParent !== null;
    if (v !== onScreen) { onScreen = v; view?.active(v); }
  };
  new IntersectionObserver(vis).observe(map);
  document.addEventListener('visibilitychange', vis);
  new MutationObserver(vis).observe($('#v-map'), { attributes: true, attributeFilter: ['hidden'] });
  new MutationObserver(vis).observe(map, { attributes: true, attributeFilter: ['hidden'] });
  vis();
  new ResizeObserver(measureBoxes).observe(map);
  // The drawer moves the toolbar; re-measure once it has.
  const drawer = document.getElementById('drawer');
  if (drawer) new MutationObserver(() => setTimeout(measureBoxes, 260)).observe(drawer, { attributes: true, attributeFilter: ['class'] });
  // Theme or accent changes: re-read colours.
  const retheme = (): void => view?.theme();
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', retheme);
  new MutationObserver(retheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style', 'class'] });
}

/** Draws the map for the memories that pass `visible`. Called on every render; keeps places. */
export function drawMap(model: Model, visible: (m: Mem) => boolean, open: (m: Mem) => void, close: () => void = () => {}): void {
  openFn = open; closeFn = close;
  if (!wired) wire();
  scene = buildScene(model, visible);
  const k = kind ?? wanted();
  if (view && kind === k) { view.update(scene); return; }
  loading ??= show(k).finally(() => { loading = null; });
}

/** Marks the memory open in the editor and moves the view to it; null clears it. */
export function markMap(id: string | null): void {
  marked = id;
  view?.mark(id);
}
