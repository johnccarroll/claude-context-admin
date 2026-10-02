import type { Budget, Prefs, Shadow, State } from './types';

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path, { credentials: 'same-origin' });
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return r.json() as Promise<T>;
}

export const loadState = (): Promise<State> => get<State>('/api/state');
export const loadBudget = (project: string): Promise<{ budget: Budget; shadows: Shadow[] }> =>
  get(`/api/budget?project=${encodeURIComponent(project)}`);
export const loadPrefs = (): Promise<Prefs> => get<Prefs>('/api/prefs');
// Saves go out one at a time, in order, each with the preferences as they were when asked: two
// quick changes (star one project, hide another) can't land out of order and undo each other.
let saving: Promise<unknown> = Promise.resolve();
let saves = 0, pending = 0;
export function savePrefs(p: Prefs): Promise<void> {
  const body = structuredClone(p);
  saves++;
  pending++;
  const next = saving.then(async () => {
    if (!(await post('/api/prefs', body, 'PUT')).ok) throw new Error('Preferences could not be saved');
  }).finally(() => { pending--; });
  saving = next.catch(() => undefined);
  return next;
}
/** A count of saves started; a refresh compares it to know local changes happened meanwhile. */
export const prefsSaves = (): number => saves + (pending ? 1e9 : 0);
export const previewCaps = (path: string): Promise<{ before: string; after: string; changes: number }> =>
  get(`/api/preview/quiet-caps?path=${encodeURIComponent(path)}`);
export const loadFile = (path: string): Promise<{ path: string; content: string; modified: string }> =>
  get(`/api/file?path=${encodeURIComponent(path)}`);

export interface ActResult { ok: boolean; message: string; activity?: string; canUndo?: boolean; confirm?: { sha256: string; command: Record<string, unknown> } }

/** Send JSON; the reply's fields come back with ok and a message, so callers never see a throw. */
async function post<T = object>(path: string, body: unknown, method = 'POST'): Promise<T & ActResult> {
  const r = await fetch(path, {
    method, credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const b = (await r.json().catch(() => ({}))) as T & Partial<ActResult>;
  return { ...b, ok: r.ok, message: b.message ?? (r.ok ? 'Done' : `Failed (${r.status})`) };
}

/** Run a change. The server validates every op and path; nothing here is trusted. */
export const act = (op: string, args: Record<string, unknown>): Promise<ActResult> => post('/api/act', { op, args });
export const undo = (id: string): Promise<ActResult> => post('/api/undo', { id });
export const rescan = (): Promise<ActResult> => post('/api/rescan', {});
export const decide = (id: string, accept: boolean): Promise<ActResult> => post('/api/proposals/decide', { id, accept });

export interface Change { path: string; before?: string; trashed?: string }
export interface Activity { id: string; at: string; who: 'you' | 'claude'; title: string; detail?: string; changes?: Change[]; undone?: boolean; canUndo: boolean }
export interface Version { path: string; at: string; who: string; content: string }
export interface Hit { path: string; kind: string; snippet: string }

export const loadActivity = (): Promise<Activity[]> => get('/api/activity');
export const loadVersions = (path: string): Promise<Version[]> => get(`/api/versions?path=${encodeURIComponent(path)}`);
export const search = (q: string): Promise<Hit[]> => get(`/api/search?q=${encodeURIComponent(q)}`);

/** Live reload: calls onChange whenever the server rescans and something changed. */
export function subscribe(onChange: () => void): void {
  const es = new EventSource('/api/events');
  es.addEventListener('changed', onChange);
  // Dev mode only (scripts/dev.sh): reload after a UI rebuild or a server restart.
  let boot = '';
  es.addEventListener('boot', (e) => { const id = (e as MessageEvent<string>).data; if (boot && id !== boot) location.reload(); boot = id; });
  es.addEventListener('reload', () => location.reload());
}

/** Show a file or folder in Finder or the file manager. Works in read-only mode: it changes nothing. */
export async function reveal(path: string): Promise<void> {
  await post('/api/reveal', { path });
}

export interface InstallServer { name: string; type: string; command?: string; args?: string[]; env?: Record<string, string>; url?: string; headers?: Record<string, string> }
export interface InstallPlan { kind: 'mcp' | 'plugin' | 'skill'; servers?: InstallServer[]; marketplace?: string; plugin?: string; skill?: { name: string; description: string; body: string }; notes?: string[] }

/** Reads a paste on the server. Changes nothing. */
export async function parseInstall(text: string): Promise<{ plan: InstallPlan; literals: Record<string, string[]> } | { error: string }> {
  const r = await post<{ plan: InstallPlan; literals: Record<string, string[]> }>('/api/install/parse', { text });
  return r.ok ? r : { error: r.message };
}
