// Mirrors internal/entry, internal/scan, internal/usage, internal/audit and internal/loads.

export type Kind =
  | 'memory' | 'memory-index' | 'instructions' | 'rule' | 'skill'
  | 'command' | 'agent' | 'plugin' | 'mcp' | 'hook';
export type Scope = 'user' | 'project' | 'local' | 'plugin' | 'import';

export interface Entry {
  kind: Kind;
  scope: Scope;
  project?: string;
  path?: string;
  name: string;
  description?: string;
  type?: string;
  enabled: boolean;
  bytes?: number;
  lines?: number;
  modified?: string;
  links?: string[];
  issues?: string[];
  meta?: Record<string, unknown>;
}

export interface Project {
  dir: string;
  path: string;
  exists: boolean;
  worktree: boolean;
  global: boolean;
  repo: boolean;
}

export interface Stat { count: number; last: string }

export interface Evidence { line: number; text: string }
export interface Finding { code: string; path: string; project?: string; detail?: string; confidence?: string; evidence?: Evidence[]; candidates?: string[] }

export interface State {
  home: string;
  projects: Project[];
  entries: Entry[];
  usage: Record<string, Stat> | null;
  report: { counts: Record<string, number>; findings: Finding[]; warnings?: string[] };
  proposals: Proposal[] | null;
  health: { name: string; status: 'warn' | 'fail'; detail: string; fix?: string }[] | null;
  readOnly: boolean;
  os: string;
}

export interface Proposal { id: string; at: string; op: string; args: Record<string, unknown>; reason: string; status: string }

export interface Source { label: string; detail: string; tokens: number; paths?: string[]; view?: string }
export interface Budget { project: string; sources: Source[]; total: number; indexLines: number }
export interface Shadow { kind: Kind; name: string; winner: Entry; hidden: Entry[] }

export interface ProjectPrefs { alias?: string; favorite?: boolean; hidden?: boolean }
export interface Prefs { projects: Record<string, ProjectPrefs>; order: string[]; dismissed?: string[] }
