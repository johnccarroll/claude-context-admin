// Splitting skill, agent and memory files into what the forms and History show, and back.
import { human, KIND, LINK, linkKey } from './model';
import { esc } from './ui';

/** A file's YAML header: the same rule as scan.SplitHeader (an empty `---\n---` header counts). */
const HEADER = /^---\r?\n(?:([\s\S]*?)\r?\n)?---[ \t]*(?:\r?\n|$)/;

/** A file's header in plain words (title, summary, kind) and its body, for History. Display only:
 *  any other header change is reported, not hidden. */
export function splitDoc(text: string): { fields: [string, string][]; rest: string; body: string } {
  const m = HEADER.exec(text);
  if (!m) return { fields: [], rest: '', body: text };
  const h = m[1] ?? '';
  const get = (k: string): string => (new RegExp(`^\\s*${k}:[ \\t]*(.*)$`, 'm').exec(h)?.[1] ?? '').trim().replace(/^(["'])(.*)\1$/, '$2');
  const type = get('type');
  const fields: [string, string][] = [['Title', get('name') && human(get('name'))], ['Summary', get('description')], ['Kind', KIND[type] ?? type]];
  const rest = h.split('\n').filter((l) => !/^\s*(name|description|type|metadata):/.test(l)).join('\n');
  return { fields: fields.filter(([, v]) => v), rest, body: text.slice(m[0].length) };
}

/** A skill, command or agent file split for its form: the header lines, the description when it is
 *  one plain line (di = its line; -1 when it spans lines, -2 when there is none), the other header
 *  settings, and the instructions. composeTool puts it back, changing only what was edited. */
export interface ToolDoc { src: string; lines: string[] | null; di: number; desc: string | null; others: string[]; body: string }
export function splitTool(text: string): ToolDoc {
  const m = HEADER.exec(text);
  if (!m) return { src: text, lines: null, di: -2, desc: null, others: [], body: text };
  const lines = (m[1] ?? '').split(/\r?\n/);
  const i = lines.findIndex((l) => /^description:/.test(l));
  const desc = i >= 0 && !/^\s+\S/.test(lines[i + 1] ?? '') ? scalar(lines[i].replace(/^description:\s*/, '').trim()) : null;
  const plain = desc !== null;
  return {
    src: text, lines, di: i < 0 ? -2 : plain ? i : -1,
    desc,
    others: lines.filter((l, j) => j !== i && l.trim() && !/^name:/.test(l) && !(i >= 0 && !plain && j > i && /^\s/.test(l))),
    body: text.slice(m[0].length),
  };
}
/** A one-line YAML scalar's value, or null when it isn't one this form can safely edit. */
function scalar(v: string): string | null {
  if (/^[>|]/.test(v)) return null; // block scalar
  if (v.startsWith('"')) { try { const x: unknown = JSON.parse(v); return typeof x === 'string' ? x : null; } catch { return null; } }
  if (v.startsWith("'")) return v.length > 1 && v.endsWith("'") && !/(^|[^'])'([^']|$)/.test(v.slice(1, -1)) ? v.slice(1, -1).replace(/''/g, "'") : null;
  return /^[[{&*!%@`]/.test(v) || / #/.test(v) ? null : v; // flow, anchors, tags or a comment: leave to the file
}
export function composeTool(d: ToolDoc, desc: string, body: string): string {
  if (!d.lines) return body;
  if (desc === (d.desc ?? '') && body === d.body) return d.src; // untouched: the exact original bytes
  const lines = [...d.lines];
  if (d.di >= 0 && desc !== d.desc) lines[d.di] = 'description: ' + JSON.stringify(desc); // a JSON string is valid YAML
  return `---\n${lines.join('\n')}\n---\n${body}`;
}

/** Memory text for reading, as HTML: ``` fenced code blocks (with their language), headings, > quotes
 *  and lists, and within lines **bold**, `code` and [[links]], each link drawn by link(key). Nothing
 *  inside code is formatted or treated as a link. Anything else stays as written. */
export function richText(text: string, link: (key: string) => string): string {
  const fence = /^(`{3,})[ \t]*([\w+#.-]*)[^\n]*\n([\s\S]*?)^\1`*[ \t]*$\n?/gm;
  let out = '', at = 0;
  for (const f of text.matchAll(fence)) {
    out += blocks(text.slice(at, f.index).replace(/\n+$/, ''), link); // the block's own margin spaces it
    out += `<pre class="code">${f[2] ? `<span class="lang">${esc(f[2])}</span>` : ''}<code>${esc(f[3].replace(/\n$/, ''))}</code></pre>`;
    at = (f.index ?? 0) + f[0].length;
  }
  return out + blocks(at ? text.slice(at).replace(/^\n+/, '') : text, link);
}

/** Line-level Markdown: headings, quotes and lists become elements; other lines stay text. */
function blocks(text: string, link: (key: string) => string): string {
  const lines = text.split('\n'), out: string[] = [];
  let i = 0, prevText = false;
  const take = (re: RegExp): string[] => { const xs: string[] = []; while (i < lines.length && re.test(lines[i])) xs.push(lines[i++]); return xs; };
  const block = (html: string): void => { out.push(html); prevText = false; };
  const BULLET = /^\s*[-*+]\s+/, NUM = /^\s*\d+[.)]\s+/;
  while (i < lines.length) {
    const l = lines[i], h = /^(#{1,6})\s+(.*)$/.exec(l);
    if (h) { i++; block(`<div class="h h${h[1].length}">${inline(h[2], link)}</div>`); }
    else if (/^>/.test(l)) block(`<blockquote>${inline(take(/^>/).map((x) => x.replace(/^>\s?/, '')).join('\n'), link)}</blockquote>`);
    else if (BULLET.test(l)) block(`<ul>${take(BULLET).map((x) => `<li>${inline(x.replace(BULLET, ''), link)}</li>`).join('')}</ul>`);
    else if (NUM.test(l)) block(`<ol>${take(NUM).map((x) => `<li>${inline(x.replace(NUM, ''), link)}</li>`).join('')}</ol>`);
    else { out.push((prevText ? '\n' : '') + inline(l, link)); prevText = true; i++; }
  }
  return out.join('');
}

function inline(text: string, link: (key: string) => string): string {
  return text.split(/(`[^`\n]+`)/).map((part, i) => {
    if (i % 2) return `<code>${esc(part.slice(1, -1))}</code>`;
    let out = '', at = 0;
    const words = (x: string): string => esc(x).replace(/\*\*([^*\n]+)\*\*/g, '<b>$1</b>');
    for (const l of part.matchAll(LINK)) {
      out += words(part.slice(at, l.index)) + link(linkKey(l[1]));
      at = (l.index ?? 0) + l[0].length;
    }
    return out + words(part.slice(at));
  }).join('');
}
