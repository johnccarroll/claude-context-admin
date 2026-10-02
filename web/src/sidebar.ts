// Sidebar project list: Everywhere on its own, then favorites, projects and hidden ones.
// Favorite, rename, hide and drag-to-reorder are display preferences saved by the server.
import { reveal, savePrefs } from './api';
import { GLOBAL, type Model, type ProjectView } from './model';
import type { Prefs } from './types';
import { $, $$, copyText, esc, menu, platform, toast, type MenuItem } from './ui';

interface Hooks {
  current: () => string;        // selected project key ('' = none)
  select: (key: string) => void;
  changed: () => void;          // prefs changed; re-render with them
  relocate: (at: HTMLElement | MouseEvent, from: string) => void; // a gone folder's memories
}

let showHidden = false;

export function renderProjects(model: Model, prefs: Prefs, counts: Map<string, number>, h: Hooks): void {
  const ps = model.projects.filter((p) => counts.has(p.key));
  const global = ps.find((p) => p.key === GLOBAL);
  const rest = ps.filter((p) => p.key !== GLOBAL);
  const favs = rest.filter((p) => p.favorite && !p.hidden);
  const normal = rest.filter((p) => !p.favorite && !p.hidden);
  const hidden = rest.filter((p) => p.hidden);

  const row = (p: ProjectView, draggable: boolean): string =>
    `<div class="prow" data-p="${esc(p.key)}" aria-current="${h.current() === p.key}" ${draggable ? 'draggable="true"' : ''}>
      <button class="pmain" title="${esc(p.path)}"><span class="dot" style="background:${p.color}"></span><span class="lbl">${esc(p.label)}</span><span class="n">${counts.get(p.key) || ''}</span></button>
      ${p.key === GLOBAL ? '' : `<button class="pact star" aria-pressed="${p.favorite}" aria-label="${p.favorite ? 'Remove from favorites' : 'Add to favorites'}" title="Favorite">★</button>`}
      ${p.key === GLOBAL ? '' : `<button class="pact more" aria-label="More for ${esc(p.label)}" title="Rename, hide">⋯</button>`}
    </div>`;
  const section = (title: string, xs: ProjectView[], drag: boolean): string =>
    xs.length ? `<div class="sect">${title}</div><div class="projs">${xs.map((p) => row(p, drag)).join('')}</div>` : '';

  const total = [...counts.values()].reduce((a, b) => a + b, 0);
  $('#projs').innerHTML = `<div class="sect">Scope</div><div class="projs"><div class="prow" data-p="" aria-current="${h.current() === ''}"><button class="pmain"><span class="dot" style="background:var(--faint)"></span><span class="lbl">All projects</span><span class="n">${total}</span></button></div>${global ? row(global, false) : ''}</div>` +
    section('Favorites', favs, true) +
    section('Projects', normal, true) +
    (hidden.length ? `<button class="sect toggle" id="htoggle" aria-expanded="${showHidden}">${showHidden ? '▾' : '▸'} Hidden <span class="hint2">${hidden.length}</span></button>${showHidden ? `<div class="projs">${hidden.map((p) => row(p, false)).join('')}</div>` : ''}` : '');

  const save = async (): Promise<void> => {
    try { await savePrefs(prefs); h.changed(); } catch (e) { toast((e as Error).message); }
  };
  const pp = (key: string) => (prefs.projects[key] ??= {});

  $$('.prow', $('#projs')).forEach((r) => {
    const key = r.dataset.p ?? '';
    $('.pmain', r).onclick = () => h.select(h.current() === key ? '' : key);
    if (!key) return; // "All projects" has no menu
    const p = model.projectOf(key);
    const star = r.querySelector<HTMLButtonElement>('.star');
    if (star) star.onclick = () => { pp(key).favorite = !p.favorite; void save(); };
    const items = (): MenuItem[] => [
      ...(key === GLOBAL ? [] : [
        ['Rename…', () => rename(r, p, (alias) => { pp(key).alias = alias; void save(); })] as MenuItem,
        ...(p.label !== p.defaultLabel ? [['Use original name', () => { pp(key).alias = ''; void save(); }] as MenuItem] : []),
        [p.favorite ? 'Remove from favorites' : 'Add to favorites', () => { pp(key).favorite = !p.favorite; void save(); }] as MenuItem,
        [p.hidden ? 'Show' : 'Hide', () => {
          pp(key).hidden = !p.hidden;
          void save();
          if (!p.hidden) toast(`Hid ${p.label}. Find it under Hidden at the bottom.`, () => { pp(key).hidden = false; void save(); });
        }] as MenuItem,
        null,
      ]),
      ['Copy path', () => void copyText(p.path)],
      ...(p.exists ? [[platform.show, () => void reveal(p.path)] as MenuItem] : []),
      ...model.state.report.findings.filter((f) => f.code === 'folder-moved' && f.project === p.key)
        .map((f) => ['Folder moved…', () => h.relocate(r, f.path)] as MenuItem),
    ];
    const more = r.querySelector<HTMLButtonElement>('.more');
    if (more) more.onclick = (e) => { e.stopPropagation(); menu(more, items()); };
    r.oncontextmenu = (e) => { e.preventDefault(); menu(e, items()); };
    if (r.draggable) wireDrag(r, prefs, model, save);
  });
  const ht = document.getElementById('htoggle');
  if (ht) ht.onclick = () => { showHidden = !showHidden; h.changed(); };
}

function rename(row: HTMLElement, p: ProjectView, done: (alias: string) => void): void {
  const main = $('.pmain', row);
  const input = document.createElement('input');
  input.className = 'rename';
  input.value = p.label;
  input.maxLength = 80;
  input.setAttribute('aria-label', 'Project name');
  main.replaceWith(input);
  input.focus();
  input.select();
  let finished = false;
  const finish = (ok: boolean): void => {
    if (finished) return;
    finished = true;
    const v = input.value.trim();
    if (ok && v && v !== p.label) done(v === p.defaultLabel ? '' : v);
    else input.replaceWith(main);
  };
  input.onkeydown = (e) => { if (e.key === 'Enter') finish(true); if (e.key === 'Escape') finish(false); };
  input.onblur = () => finish(true);
}

// Drag a row onto another to place it before that row. Order is saved as a list of keys.
let dragKey = '';
function wireDrag(r: HTMLElement, prefs: Prefs, model: Model, save: () => Promise<void>): void {
  const key = r.dataset.p ?? '';
  r.ondragstart = (e) => { dragKey = key; r.classList.add('dragging'); e.dataTransfer?.setData('text/plain', key); };
  r.ondragend = () => { r.classList.remove('dragging'); $$('.prow.drag-over').forEach((x) => x.classList.remove('drag-over')); };
  r.ondragover = (e) => { if (dragKey && dragKey !== key) { e.preventDefault(); r.classList.add('drag-over'); } };
  r.ondragleave = () => r.classList.remove('drag-over');
  r.ondrop = (e) => {
    e.preventDefault();
    r.classList.remove('drag-over');
    if (!dragKey || dragKey === key) return;
    const order = model.projects.map((p) => p.key).filter((k) => k !== dragKey);
    order.splice(order.indexOf(key), 0, dragKey);
    prefs.order = order;
    dragKey = '';
    void save();
  };
}
