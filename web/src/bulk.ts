// Multi-select on the memory list. While anything is selected, the page header becomes the
// selection bar (people missed a bar at the bottom of the screen).
import { GLOBAL, KHELP, KIND, type Model } from './model';
import { $, $$, esc, menu, type MenuItem } from './ui';

export const selected = new Set<string>();
let last = '';

const CHECK = '<svg viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="2"><path d="M2.5 6.2l2.3 2.3 4.7-5"/></svg>';

export const checkbox = (id: string, title: string): string =>
  `<input type="checkbox" class="cbox" ${selected.has(id) ? 'checked' : ''} aria-label="Select ${esc(title)}">`;

interface Hooks {
  rerender: () => void;
  bulk: (action: string, extra?: Record<string, unknown>) => void;
  memoryDir: (project: string) => string;
}

/** Wire checkboxes in the list; shift-click selects a range. */
export function wireCheckboxes(root: HTMLElement, h: Hooks): void {
  const ids = $$('.row', root).map((r) => r.dataset.id ?? '');
  $$<HTMLInputElement>('.cbox', root).forEach((c) => (c.onclick = (e) => {
    e.stopPropagation();
    const id = (c.closest('.row') as HTMLElement).dataset.id ?? '';
    if (e.shiftKey && last && ids.includes(last)) {
      const [a, b] = [ids.indexOf(last), ids.indexOf(id)].sort((x, y) => x - y);
      ids.slice(a, b + 1).forEach((x) => selected.add(x));
    } else if (selected.has(id)) selected.delete(id);
    else selected.add(id);
    last = id;
    h.rerender();
  }));
  root.classList.toggle('selecting', selected.size > 0);
}

export function clearSelection(): void {
  selected.clear();
  last = '';
}

/** The selection bar, drawn over the page header. */
export function renderSelectionBar(model: Model, visibleIds: string[], h: Hooks): void {
  document.querySelector('.selhead')?.remove();
  document.querySelector('.main')?.classList.toggle('selecting', selected.size > 0);
  if (!selected.size) return;
  const bar = document.createElement('div');
  bar.className = 'selhead';
  bar.setAttribute('role', 'toolbar');
  bar.setAttribute('aria-label', 'Selected memories');
  const n = selected.size;
  const anyProject = [...selected].some((id) => model.byId.get(id)?.project !== GLOBAL);
  bar.innerHTML = `<button class="all" aria-label="Clear selection" title="Clear selection">${CHECK}</button><span class="cnt2">${n} selected</span>
    ${n < visibleIds.length ? `<button class="link" data-b="all">Select all ${visibleIds.length}</button>` : ''}<span class="sep"></span>
    <button class="btn sm" data-b="type">Change kind</button><button class="btn sm" data-b="move">Move to project</button>
    ${anyProject ? '<button class="btn sm" data-b="global">Use everywhere</button>' : ''}<button class="btn sm dng" data-b="trash">Move to Trash</button>
    <span class="grow"></span><button class="btn sm" data-b="done">Done</button>`;
  $('.top').appendChild(bar);
  ($('.all', bar) as HTMLButtonElement).onclick = () => { clearSelection(); h.rerender(); };
  $$<HTMLButtonElement>('[data-b]', bar).forEach((b) => (b.onclick = () => {
    switch (b.dataset.b) {
      case 'done': clearSelection(); h.rerender(); break;
      case 'all': visibleIds.forEach((id) => selected.add(id)); h.rerender(); break;
      case 'type':
        menu(b, Object.keys(KIND).map((k) => [`${KIND[k]} · ${KHELP[k]}`, () => h.bulk('type', { type: k })] as MenuItem));
        break;
      case 'move':
        menu(b, model.projects.filter((p) => p.key !== GLOBAL && p.exists && h.memoryDir(p.key))
          .map((p) => [p.label, () => h.bulk('move', { dir: h.memoryDir(p.key) })] as MenuItem));
        break;
      default: h.bulk(b.dataset.b ?? '');
    }
  }));
}
