// The Markdown editor: CodeMirror 6 with live preview. Marks (**, #, `, >, -) show
// only on the line the cursor is on; everywhere else the text reads as formatted. [[links]] draw
// as chips named by the memory they open, and typing [[ lists the memories that can be linked.
// The file keeps exactly what was typed.
import { autocompletion, closeBrackets, closeCompletion, completionStatus, startCompletion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { markdown, markdownKeymap, markdownLanguage } from '@codemirror/lang-markdown';
import { HighlightStyle, syntaxHighlighting, syntaxTree } from '@codemirror/language';
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
import { Annotation, Compartment, EditorSelection, EditorState, Prec, RangeSetBuilder, type Extension } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, keymap, placeholder, ViewPlugin, type ViewUpdate, WidgetType } from '@codemirror/view';
import type { StateCommand } from '@codemirror/state';
import { tags as t } from '@lezer/highlight';
import { LINK, linkKey } from './model';

/** A memory a [[link]] can point at, as the editor shows it. */
export interface LinkTarget { id: string; title: string; slug: string; color: string; where: string; summary: string }
export interface Links {
  /** Every memory the file could link to (its own project's, then Everywhere). */
  candidates: () => LinkTarget[];
  /** The memory a [[key]] opens, or null when it doesn't exist. */
  resolve: (key: string) => LinkTarget | null;
  /** A click on a link: open it, or (when the text is editable) change or remove it. */
  pick: (at: HTMLElement, target: LinkTarget | null, key: string, edit: LinkEdit | null) => void;
}
/** Edits the editor offers for one link: point it elsewhere (the list opens), or make it plain text. */
export interface LinkEdit { change: () => void; remove: () => void }

export interface Editor {
  readonly value: string;
  /** Replace the text without counting as an edit (the loaded file arriving). */
  set: (text: string) => void;
  focus: () => void;
  destroy: () => void;
}

const programmatic = Annotation.define<boolean>();

/** Source mode shows every mark, always; the default formats the text and shows marks only on the
 *  cursor's line. The choice is remembered. */
const SOURCE = 'cca-md-source';
const sourceMode = (): boolean => { try { return localStorage.getItem(SOURCE) === '1'; } catch { return false; } };
const preview = new Compartment();

/** Mounts an editor in host. Typing fires a bubbling `input` event on host, like a text box.
 *  plain shows the text as written (a whole file with its header), in a monospace font. */
export function mountEditor(host: HTMLElement, opts: { value: string; links?: Links; placeholder?: string; readOnly?: boolean; plain?: boolean }): Editor {
  if (!opts.readOnly && !opts.plain) host.append(toolbar(() => view, opts.links));
  host.classList.toggle('plain', !!opts.plain);
  const view = new EditorView({
    parent: host,
    state: EditorState.create({
      doc: opts.value,
      extensions: [
        opts.readOnly ? [EditorState.readOnly.of(true), EditorView.editable.of(false)] : editing(opts.links),
        opts.plain ? [] : [markdown({ base: markdownLanguage, addKeymap: false }), syntaxHighlighting(style), preview.of(sourceMode() && !opts.readOnly ? [] : livePreview(opts.links))], // plain: the file as it is, for its header
        EditorView.lineWrapping,
        opts.placeholder ? placeholder(opts.placeholder) : [],
        EditorView.updateListener.of((u) => {
          if (u.docChanged && !u.transactions.some((tr) => tr.annotation(programmatic))) host.dispatchEvent(new Event('input', { bubbles: true }));
        }),
      ],
    }),
  });
  return {
    get value() { return view.state.doc.toString(); },
    set: (text) => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text }, annotations: programmatic.of(true) }),
    focus: () => view.focus(),
    destroy: () => view.destroy(),
  };
}

/** Everything an editable file gets: history, the Markdown keys (Enter continues a list), search,
 *  bracket closing, ⌘B / ⌘I, and the [[ list. */
function editing(links?: Links): Extension {
  return [
    history(),
    closeBrackets(),
    highlightSelectionMatches(),
    EditorView.contentAttributes.of({ spellcheck: 'true', autocorrect: 'on' }),
    links ? autocompletion({ override: [linkCompletions(links)], icons: false, closeOnBlur: true }) : [],
    // Escape with the list open closes only the list, not the panel around the editor.
    Prec.highest(EditorView.domEventHandlers({ keydown: (e, v) => { if (e.key === 'Escape' && completionStatus(v.state) === 'active') { e.stopPropagation(); return closeCompletion(v); } return false; } })),
    keymap.of([
      { key: 'Mod-b', run: wrapWith('**') }, { key: 'Mod-i', run: wrapWith('_') }, { key: 'Mod-e', run: code },
      ...markdownKeymap, ...historyKeymap, ...searchKeymap, ...defaultKeymap,
    ]),
  ];
}

/** Wraps the selection in a mark (or inserts a pair to type into), as ⌘B does in a word processor;
 *  wrapped already, unwraps it. */
export const wrapWith = (open: string, close = open): StateCommand => ({ state, dispatch }) => {
  dispatch(state.update(state.changeByRange((r) => {
    const n = open.length, m = close.length;
    if (state.sliceDoc(r.from - n, r.from) === open && state.sliceDoc(r.to, r.to + m) === close) {
      return { changes: [{ from: r.from - n, to: r.from }, { from: r.to, to: r.to + m }], range: EditorSelection.range(r.from - n, r.to - n) };
    }
    return { changes: [{ from: r.from, insert: open }, { from: r.to, insert: close }], range: EditorSelection.range(r.from + n, r.to + n) };
  })));
  return true;
};
/** `code`, or a ``` block when the selection spans lines. */
export const code: StateCommand = (t) => (t.state.sliceDoc(t.state.selection.main.from, t.state.selection.main.to).includes('\n') ? wrapWith('```\n', '\n```') : wrapWith('`'))(t);

/** Adds, cycles or removes a line prefix on every selected line: `#` → `##` → `###` → none; `- ` on or off. */
export const prefixLines = (cycle: string[]): StateCommand => ({ state, dispatch }) => {
  const { doc } = state, r = state.selection.main;
  const changes = [];
  for (let n = doc.lineAt(r.from).number; n <= doc.lineAt(r.to).number; n++) {
    const l = doc.line(n), cur = cycle.find((p) => p && l.text.startsWith(p)) ?? '';
    const next = cycle[(cycle.indexOf(cur) + 1) % cycle.length];
    changes.push({ from: l.from, to: l.from + cur.length, insert: next });
  }
  dispatch(state.update({ changes }));
  return true;
};

/** Starts a link: [[ around the selection, with the list of memories open. */
const link = (v: EditorView): boolean => {
  const r = v.state.selection.main, t = v.state.sliceDoc(r.from, r.to);
  v.dispatch({ changes: { from: r.from, to: r.to, insert: `[[${t}` }, selection: { anchor: r.from + 2 + t.length } });
  startCompletion(v);
  return true;
};

const I = {
  link: '<svg viewBox="0 0 16 16"><path d="M6.5 9.5a3 3 0 0 0 4.2 0l2-2a3 3 0 0 0-4.2-4.2l-.8.8M9.5 6.5a3 3 0 0 0-4.2 0l-2 2a3 3 0 0 0 4.2 4.2l.8-.8"/></svg>',
  list: '<svg viewBox="0 0 16 16"><path d="M6 4h7M6 8h7M6 12h7M3 4h.01M3 8h.01M3 12h.01"/></svg>',
};
/** The formatting bar above the text: each button keeps the cursor where it is. */
function toolbar(view: () => EditorView, links?: Links): HTMLElement {
  const bar = document.createElement('div');
  bar.className = 'mdbar'; bar.setAttribute('role', 'toolbar'); bar.setAttribute('aria-label', 'Formatting');
  const tools: [string, string, string, (v: EditorView) => boolean][] = [
    ...(links ? [['link', I.link + '<span>Link</span>', 'Link a memory (or type [[)', link] as [string, string, string, (v: EditorView) => boolean]] : []),
    ['bold', '<b>B</b>', 'Bold (⌘B)', wrapWith('**')], ['italic', '<i>I</i>', 'Italic (⌘I)', wrapWith('_')],
    ['code', '<span class="mono">&lt;/&gt;</span>', 'Code (⌘E); a block when lines are selected', code],
    ['heading', '<span class="mono">H</span>', 'Heading: tap again for the next size', prefixLines(['', '# ', '## ', '### '])],
    ['list', I.list, 'List', prefixLines(['', '- '])],
  ];
  bar.innerHTML = tools.map(([k, html, title]) => `<button type="button" data-t="${k}" title="${title}" aria-label="${title.split(' (')[0].split(':')[0]}">${html}</button>`).join('') +
    `<span class="vmode" role="radiogroup" aria-label="View"><button type="button" role="radio" data-t="preview" aria-checked="${!sourceMode()}" title="Preview: the text reads as formatted; marks show on the line you're on">Preview</button><button type="button" role="radio" data-t="source" aria-checked="${sourceMode()}" title="Source: every Markdown mark, always">Source</button></span>`;
  bar.onmousedown = (e) => e.preventDefault(); // the text keeps focus and its selection
  bar.onclick = (e) => {
    const k = (e.target as HTMLElement).closest<HTMLElement>('[data-t]')?.dataset.t, v = view();
    if (k === 'source' || k === 'preview') {
      const on = k === 'source';
      try { localStorage.setItem(SOURCE, on ? '1' : '0'); } catch { /* only a convenience */ }
      bar.querySelectorAll('[role=radio]').forEach((r) => r.setAttribute('aria-checked', String((r as HTMLElement).dataset.t === k)));
      v.dispatch({ effects: preview.reconfigure(on ? [] : livePreview(links)) });
      v.focus();
      return;
    }
    const t = tools.find((x) => x[0] === k); if (t) { t[3](v); v.focus(); }
  };
  return bar;
}

export function linkCompletions(links: Links) {
  return (ctx: CompletionContext): CompletionResult | null => {
    const m = ctx.matchBefore(/\[\[([^\]\n|#]*)$/);
    if (!m || inCode(ctx.state, ctx.pos)) return null;
    const from = m.from + 2;
    return {
      from,
      filter: true,
      options: links.candidates().map((c) => ({
        label: c.title, detail: c.where, type: 'link',
        apply: (v, _c, _from, to) => { // write the slug, and step over a ]] closeBrackets already put there
          const end = v.state.sliceDoc(to, to + 2) === ']]' ? to + 2 : to;
          v.dispatch({ changes: { from: m.from, to: end, insert: `[[${c.slug}]]` }, selection: { anchor: m.from + c.slug.length + 4 } });
        },
      })),
    };
  };
}

// ---------- live preview ----------

const style = HighlightStyle.define([
  { tag: t.heading1, class: 'md-h md-h1' }, { tag: t.heading2, class: 'md-h md-h2' }, { tag: t.heading3, class: 'md-h md-h3' },
  { tag: [t.heading4, t.heading5, t.heading6], class: 'md-h' },
  { tag: t.strong, class: 'md-strong' }, { tag: t.emphasis, class: 'md-em' }, { tag: t.strikethrough, class: 'md-strike' },
  { tag: t.monospace, class: 'md-code' }, { tag: [t.url, t.link], class: 'md-url' },
  { tag: t.processingInstruction, class: 'md-mark' }, { tag: t.labelName, class: 'md-mark' }, { tag: t.contentSeparator, class: 'md-mark' },
]);

class LinkChip extends WidgetType {
  constructor(private readonly key: string, private readonly from: number, private readonly to: number, private readonly links: Links) { super(); }
  eq(o: LinkChip): boolean { return o.key === this.key && o.from === this.from && o.to === this.to; }
  toDOM(view: EditorView): HTMLElement {
    const t = this.links.resolve(this.key), b = document.createElement('button');
    b.type = 'button';
    b.className = 'lchip' + (t ? '' : ' bad');
    b.dataset.id = t?.id ?? '';
    if (t) {
      b.title = t.summary || `Open “${t.title}”`;
      b.innerHTML = `<span class="dot" style="background:${t.color}"></span>`;
      b.append(t.title);
    } else {
      b.title = 'This memory doesn’t exist';
      b.append(human(this.key));
      const s = document.createElement('span'); s.textContent = 'missing'; b.append(s);
    }
    const { from, to } = this;
    const edit: LinkEdit | null = view.state.readOnly ? null : {
      change: () => { view.dispatch({ changes: { from, to, insert: '[[' }, selection: { anchor: from + 2 } }); view.focus(); startCompletion(view); },
      remove: () => { view.dispatch({ changes: { from, to, insert: t?.title ?? human(this.key) } }); view.focus(); },
    };
    b.onclick = () => this.links.pick(b, t, this.key, edit);
    return b;
  }
}
const human = (s: string): string => s.replace(/^(feedback|project|reference|user)[_-]/, '').replace(/[_-]+/g, ' ');

class Bullet extends WidgetType {
  toDOM(): HTMLElement { const s = document.createElement('span'); s.className = 'md-bullet'; s.textContent = '•'; return s; }
  eq(): boolean { return true; }
}
const bullet = Decoration.replace({ widget: new Bullet() });
const hide = Decoration.replace({});
const quoteLine = Decoration.line({ class: 'md-quote' });
const fenceLine = Decoration.line({ class: 'md-fence' });

/** Marks on the cursor's line show; elsewhere they're hidden and [[links]] become chips. */
function livePreview(links?: Links): Extension {
  return ViewPlugin.fromClass(class {
    decorations: DecorationSet;
    constructor(v: EditorView) { this.decorations = build(v, links); }
    update(u: ViewUpdate): void { if (u.docChanged || u.selectionSet || u.viewportChanged || u.focusChanged) this.decorations = build(u.view, links); }
  }, { decorations: (p) => p.decorations });
}

function build(view: EditorView, links?: Links): DecorationSet {
  const { state } = view, doc = state.doc;
  const active = new Set<number>();
  if (view.hasFocus) for (const r of state.selection.ranges) for (let n = doc.lineAt(r.from).number; n <= doc.lineAt(r.to).number; n++) active.add(n);
  const onActive = (pos: number): boolean => active.has(doc.lineAt(pos).number);
  const out: { from: number; to: number; d: Decoration }[] = [];
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(state).iterate({
      from, to,
      enter: (n) => {
        switch (n.name) {
          case 'HeaderMark': case 'EmphasisMark': case 'CodeMark': case 'QuoteMark': {
            if (n.name === 'CodeMark' && n.node.parent?.name === 'FencedCode') return; // ``` lines stay, dimmed
            if (onActive(n.from)) return;
            let end = n.to;
            if (n.name === 'HeaderMark' || n.name === 'QuoteMark') while (end < doc.length && doc.sliceString(end, end + 1) === ' ') end++;
            out.push({ from: n.from, to: end, d: hide });
            return;
          }
          case 'ListMark':
            if (!onActive(n.from) && /^[-*+]$/.test(doc.sliceString(n.from, n.to))) out.push({ from: n.from, to: n.to, d: bullet });
            return;
          case 'Blockquote': case 'FencedCode': {
            const cls = n.name === 'Blockquote' ? quoteLine : fenceLine;
            for (let l = doc.lineAt(n.from).number, last = doc.lineAt(n.to).number; l <= last; l++) { const line = doc.line(l); out.push({ from: line.from, to: line.from, d: cls }); }
            return;
          }
          case 'InlineCode': case 'CodeText': case 'URL': case 'Link':
            return false; // nothing inside code is a link
          case 'Paragraph': case 'ATXHeading1': case 'ATXHeading2': case 'ATXHeading3': case 'ATXHeading4': case 'ATXHeading5': case 'ATXHeading6': case 'ListItem': case 'TableCell':
            return;
        }
      },
    });
    // [[links]]: the Markdown parser doesn't know them, so find them in the text itself.
    if (links) for (let l = doc.lineAt(from).number, last = doc.lineAt(to).number; l <= last; l++) {
      const line = doc.line(l);
      for (const m of line.text.matchAll(LINK)) {
        const a = line.from + (m.index ?? 0), b = a + m[0].length;
        if (state.selection.ranges.some((r) => view.hasFocus && r.from <= b && r.to >= a)) continue; // being edited: raw
        if (inCode(state, a)) continue;
        out.push({ from: a, to: b, d: Decoration.replace({ widget: new LinkChip(linkKey(m[1]), a, b, links) }) });
      }
    }
  }
  out.sort((x, y) => x.from - y.from || x.to - y.to || (x.d.spec.widget || x.d.spec.class === undefined ? 1 : 0) - (y.d.spec.widget || y.d.spec.class === undefined ? 1 : 0));
  const b = new RangeSetBuilder<Decoration>();
  let last = -1;
  for (const x of out) { if (x.from < last) continue; b.add(x.from, x.to, x.d); last = x.to; } // the first of two overlapping decorations wins
  return b.finish();
}

function inCode(state: EditorState, pos: number): boolean {
  let n: { name: string; parent: unknown } | null = syntaxTree(state).resolveInner(pos, 1);
  for (; n; n = n.parent as typeof n) if (n.name === 'InlineCode' || n.name === 'FencedCode' || n.name === 'CodeBlock' || n.name === 'CodeText') return true;
  return false;
}

/** Text shown read-only with the same formatting: review previews and old versions. */
export function renderMarkdown(host: HTMLElement, text: string, links: Links): void {
  host.replaceChildren();
  mountEditor(host, { value: text, links, readOnly: true });
}
