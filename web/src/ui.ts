// Small DOM helpers shared by every view.

export const $ = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document): T => {
  const el = root.querySelector<T>(sel);
  if (!el) throw new Error('missing element ' + sel);
  return el;
};
export const $$ = <T extends HTMLElement = HTMLElement>(sel: string, root: ParentNode = document): T[] =>
  [...root.querySelectorAll<T>(sel)];

/** Escape text for HTML. Everything shown comes from files on disk, so escape it all. */
export const esc = (s: unknown): string =>
  String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);

export const fmtN = (n: number): string =>
  n >= 1000 ? (n / 1000).toFixed(n >= 10000 ? 0 : 1).replace(/\.0$/, '') + 'k' : String(n);

export const fmtDate = (d: string): string =>
  d ? new Date(d + 'T12:00').toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) : '';

const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' }); // today, yesterday, 5 days ago
export const ago = (d: string): string =>
  d ? rtf.format(-Math.max(0, Math.round((Date.now() - new Date(d + 'T12:00').getTime()) / 864e5)), 'day') : '';

/** Read a CSS custom property, for canvas/SVG code that needs a concrete color. */
export const cssVar = (v: string): string =>
  v.startsWith('var(') ? getComputedStyle(document.documentElement).getPropertyValue(v.slice(4, -1)).trim() : v;

let toastTimer = 0;
let undoFn: (() => void) | undefined;
export function toast(msg: string, undo?: () => void): void {
  $('#tmsg').textContent = msg;
  undoFn = undo;
  $('#tundo').hidden = !undo;
  $('#toast').classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => $('#toast').classList.remove('show'), 4000);
}
export function wireToast(): void {
  $('#tundo').onclick = () => { undoFn?.(); $('#toast').classList.remove('show'); };
}

/** Set when cca doctor finds the transcript format changed: usage numbers can't be trusted. */
export const usageState = { broken: false };

export const usageCell = (uses: number, last: string, hideSmall = false): string =>
  usageState.broken
    ? `<div class="stat2 ${hideSmall ? 'hide-sm' : ''}" title="Usage can't be read on this Claude Code version"><b>—</b>usage unavailable</div>`
    : uses
    ? `<div class="stat2 ${hideSmall ? 'hide-sm' : ''}"><b>${uses.toLocaleString()} ${uses === 1 ? 'use' : 'uses'}</b>${esc(ago(last))}</div>`
    : `<div class="stat2 cold ${hideSmall ? 'hide-sm' : ''}"><b>Unused</b>last 90 days</div>`;

export const toggle = (on: boolean, id: string): string =>
  `<button class="switch" role="switch" aria-checked="${on}" data-sw="${esc(id)}" aria-label="Enabled"></button>`;

/** Words that differ by OS; set from the server's state. */
export const platform = { show: 'Show in Finder' };

export type MenuItem = [string, () => void] | null; // null draws a divider

/** Small popup menu at an element or at the pointer (right-click). */
export function menu(at: HTMLElement | MouseEvent, items: MenuItem[]): void {
  document.querySelector('.menu')?.remove();
  const m = document.createElement('div');
  m.className = 'menu';
  m.setAttribute('role', 'menu');
  m.innerHTML = items.map((it, i) => (it ? `<button role="menuitem" data-i="${i}">${esc(it[0])}</button>` : '<hr>')).join('');
  document.body.appendChild(m);
  const [x0, y0] = at instanceof MouseEvent ? [at.clientX, at.clientY] : [at.getBoundingClientRect().left, at.getBoundingClientRect().bottom + 4];
  m.style.left = Math.min(x0, innerWidth - m.offsetWidth - 8) + 'px';
  m.style.top = Math.min(y0, innerHeight - m.offsetHeight - 8) + 'px';
  $$('button', m).forEach((x) => (x.onclick = () => { m.remove(); items[Number(x.dataset.i)]?.[1](); }));
  ($('button', m) as HTMLButtonElement).focus();
  const close = (e: Event): void => {
    if (e instanceof KeyboardEvent && e.key !== 'Escape') return;
    if (e.type === 'mousedown' && m.contains(e.target as Node)) return;
    m.remove();
    removeEventListener('mousedown', close);
    removeEventListener('keydown', close);
  };
  setTimeout(() => { addEventListener('mousedown', close); addEventListener('keydown', close); });
}

export async function copyText(text: string, done = 'Copied.'): Promise<void> {
  try { await navigator.clipboard.writeText(text); toast(done); } catch { toast(text); }
}
