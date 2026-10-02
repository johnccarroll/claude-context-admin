// Splitting skill, agent and memory files into what the forms and History show, and back.
import { human, KIND } from './model';

/** A file's header in plain words (title, summary, kind) and its body, for History. Display only:
 *  any other header change is reported, not hidden. */
export function splitDoc(text: string): { fields: [string, string][]; rest: string; body: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
  if (!m) return { fields: [], rest: '', body: text };
  const get = (k: string): string => (new RegExp(`^\\s*${k}:[ \\t]*(.*)$`, 'm').exec(m[1])?.[1] ?? '').trim().replace(/^(["'])(.*)\1$/, '$2');
  const type = get('type');
  const fields: [string, string][] = [['Title', get('name') && human(get('name'))], ['Summary', get('description')], ['Kind', KIND[type] ?? type]];
  const rest = m[1].split('\n').filter((l) => !/^\s*(name|description|type|metadata):/.test(l)).join('\n');
  return { fields: fields.filter(([, v]) => v), rest, body: text.slice(m[0].length) };
}

/** A skill, command or agent file split for its form: the header lines, the description when it is
 *  one plain line (di = its line; -1 when it spans lines, -2 when there is none), the other header
 *  settings, and the instructions. composeTool puts it back, changing only what was edited. */
export interface ToolDoc { src: string; lines: string[] | null; di: number; desc: string | null; others: string[]; body: string }
export function splitTool(text: string): ToolDoc {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
  if (!m) return { src: text, lines: null, di: -2, desc: null, others: [], body: text };
  const lines = m[1].split(/\r?\n/);
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
