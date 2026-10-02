// ⌘K search across memories, instructions, skills, plugins and projects. Names and summaries
// match instantly in the browser; full text comes from the server as you type.
import { search, type Hit } from './api';
import { KIND, type Mem, type Model } from './model';
import { $, $$, esc } from './ui';

interface Hooks {
  openMemory: (m: Mem) => void;
  openPath: (path: string) => void;  // a non-memory file: instructions, skill, agent…
  openProject: (key: string) => void;
  newMemory: () => void;
  add: () => void;
}

const SEARCH_ICON = '<svg width="17" height="17" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" style="color:var(--faint)"><circle cx="9" cy="9" r="6"/><path d="M14 14l4 4"/></svg>';

export function openPalette(model: Model, h: Hooks): void {
  document.querySelector('.palback')?.remove();
  // A modal <dialog>: the page behind is inert, and Escape closes it.
  const back = document.createElement('dialog');
  back.className = 'palback';
  back.setAttribute('aria-label', 'Search');
  back.innerHTML = `<div class="palette"><div class="pin">${SEARCH_ICON}<input id="pq" placeholder="Search memories, instructions, skills, plugins, projects…" aria-label="Search" autocomplete="off"><span style="font:11px var(--f-mono);color:var(--faint)">esc</span></div><div class="res" id="pres" role="listbox"></div>
    <div class="pfoot"><span>↑↓ to move</span><span>↵ to open</span><span style="margin-left:auto">Searches titles, summaries and full text</span></div></div>`;
  $('#app').appendChild(back);
  back.onclose = () => back.remove();
  back.showModal();
  const input = $<HTMLInputElement>('#pq');
  let on = 0, hits: Hit[] = [], seq = 0, timer = 0;
  const close = (): void => back.remove();
  const mark = (s: string, w: string): string => {
    if (!w) return esc(s);
    const i = s.toLowerCase().indexOf(w);
    return i < 0 ? esc(s) : esc(s.slice(0, i)) + '<mark>' + esc(s.slice(i, i + w.length)) + '</mark>' + esc(s.slice(i + w.length));
  };

  const draw = (): void => {
    const w = input.value.trim().toLowerCase();
    const has = (s: string): boolean => !w || s.toLowerCase().includes(w);
    const byPath = new Map(model.mems.map((m) => [m.path, m]));
    const mems = model.mems.filter((m) => has(m.title + ' ' + m.desc));
    // full-text hits on memories not already matched by title or summary
    const textMems = hits.filter((x) => x.kind === 'memory' && byPath.has(x.path) && !mems.includes(byPath.get(x.path)!));
    const files = hits.filter((x) => x.kind !== 'memory');
    const toolkit = [...model.entries('plugin'), ...model.entries('mcp'), ...model.entries('agent').filter((e) => e.scope !== 'plugin')]
      .filter((e) => w && has(e.name + ' ' + (e.description ?? ''))).slice(0, 5);
    const projects = model.projects.filter((p) => w && has(p.label)).slice(0, 4);
    const memRow = (m: Mem, snippet?: string): string => `<div class="it" data-mem="${esc(m.id)}"><span class="dot" style="background:${model.projectOf(m.project).color}"></span><span class="ti">${mark(m.title, w)}</span><span class="su">${mark(snippet ?? m.desc, w)}</span><span class="k">${esc(KIND[m.type] ?? m.type)} · ${esc(model.projectOf(m.project).label)}</span></div>`;
    const actions = [
      ...(!w || 'new memory'.includes(w) || w.startsWith('new') ? ['<div class="it" data-act="new"><span class="tag">+</span><span class="ti">New memory</span><span class="su">Write something Claude should remember</span></div>'] : []),
      ...(!w || /^(add|install|mcp|plugin|skill)/.test(w) ? ['<div class="it" data-act="add"><span class="tag">+</span><span class="ti">Add MCP server, plugin or skill</span><span class="su">Paste what the docs give you</span></div>'] : []),
    ];
    const groups: [string, string[]][] = [
      ['Actions', actions],
      ['Memories', [...mems.slice(0, 6).map((m) => memRow(m)), ...textMems.slice(0, 6).map((x) => memRow(byPath.get(x.path)!, x.snippet))]],
      ['Instructions, skills & agents', files.slice(0, 6).map((x) => { const e = model.state.entries.find((y) => y.path === x.path); return `<div class="it" data-path="${esc(x.path)}"><span class="tag">${esc(x.kind)}</span><span class="ti">${esc(x.path.split('/').slice(-2).join('/'))}</span><span class="su">${mark(x.snippet, w)}</span><span class="k">${esc(e?.project ? model.projectOf(e.project).label : 'Everywhere')}</span></div>`; })],
      ['Plugins, MCP & agents', toolkit.map((e) => `<div class="it" data-view="${e.kind === 'plugin' ? 'plugins' : e.kind === 'mcp' ? 'mcp' : 'agents'}"><span class="tag">${esc(e.kind)}</span><span class="ti">${mark(e.name, w)}</span><span class="su">${mark(e.description ?? '', w)}</span></div>`)],
      ['Projects', projects.map((p) => `<div class="it" data-proj="${esc(p.key)}"><span class="dot" style="background:${p.color}"></span><span class="ti">${mark(p.label, w)}</span><span class="k">Project</span></div>`)],
    ].filter(([, xs]) => xs.length) as [string, string[]][];
    $('#pres').innerHTML = groups.length
      ? groups.map(([g, xs]) => `<div class="grp">${g}</div>${xs.join('')}`).join('')
      : `<div class="empty" style="padding:30px">${w ? 'Nothing matches. Try fewer words.' : 'Type to search everything Claude loads.'}</div>`;
    const items = $$('#pres .it');
    on = Math.max(0, Math.min(on, items.length - 1));
    items.forEach((x, i) => { x.classList.toggle('on', i === on); x.setAttribute('role', 'option'); });
    items.forEach((x) => (x.onclick = () => {
      close();
      if (x.dataset.act === 'new') h.newMemory();
      else if (x.dataset.act === 'add') h.add();
      else if (x.dataset.mem) { const m = model.byId.get(x.dataset.mem); if (m) h.openMemory(m); }
      else if (x.dataset.path) h.openPath(x.dataset.path);
      else if (x.dataset.proj) h.openProject(x.dataset.proj);
      else if (x.dataset.view) document.querySelector<HTMLButtonElement>(`#nav2 button[data-v="${x.dataset.view}"]`)?.click();
    }));
  };

  input.oninput = () => {
    on = 0;
    draw();
    clearTimeout(timer);
    const q = input.value.trim(), mine = ++seq;
    if (q.length < 2) { hits = []; return; }
    timer = window.setTimeout(() => void search(q).then((r) => { if (mine === seq) { hits = r; draw(); } }).catch(() => {}), 150);
  };
  input.onkeydown = (e) => {
    const items = $$('#pres .it');
    if (e.key === 'ArrowDown') { on = Math.min(on + 1, items.length - 1); draw(); e.preventDefault(); items[on]?.scrollIntoView({ block: 'nearest' }); }
    if (e.key === 'ArrowUp') { on = Math.max(on - 1, 0); draw(); e.preventDefault(); }
    if (e.key === 'Enter') items[on]?.click();
  };
  back.onclick = (e) => { if (e.target === back) close(); };
  draw();
  input.focus();
}
