// "Add to Claude": paste what install docs give you (an MCP JSON snippet, `claude mcp add`,
// `/plugin` commands, a SKILL.md) or write a skill. The server parses and validates; MCP servers
// and plugins are added by Claude Code's own CLI, skills through cca's versioned writer.
// Nothing here talks to the network.
import { act, parseInstall, type InstallPlan, type InstallServer } from './api';
import type { Model } from './model';
import { $, $$, esc, toast } from './ui';

export interface AddHooks {
  model: () => Model;
  scope: () => string;              // the current sidebar scope (project key, GLOBAL or '')
  done: (msg: string, activity?: string) => Promise<void>; // refresh, toast with Undo
  readOnly: () => boolean;
}

const EXAMPLE = '{\n  "mcpServers": {\n    "playwright": { "command": "npx", "args": ["@playwright/mcp@latest"] }\n  }\n}';
/** The environment variable a key reads from, as the server names it (API key → API_KEY). */
const envName = (k: string): string => k.toUpperCase().replace(/[^A-Z0-9]+/g, '_');
const SCOPES: [string, string][] = [
  ['user', 'Everywhere: every project, just you'],
  ['project', 'This project, shared: saved in the repo for everyone'],
  ['local', 'This project, only you'],
];

/** Fills the drawer with the add form. start picks what the empty form suggests. */
export function openAdd(d: HTMLElement, h: AddHooks, start: 'mcp' | 'plugin' | 'skill' = 'mcp'): void {
  const projects = h.model().state.projects.filter((p) => p.exists && !p.worktree && !p.global);
  const cur = projects.find((p) => p.path === h.scope());
  d.innerHTML = `<div class="dhead"><div class="where">Add to Claude</div><button class="x" id="dx" aria-label="Close">×</button></div>
   <div class="dbody">
    <div class="field"><label for="a-text">${start === 'skill' ? 'Paste a SKILL.md, or write one below' : 'Paste what the docs give you'}</label>
     <textarea id="a-text" class="mono" spellcheck="false" placeholder="${esc(start === 'plugin' ? '/plugin marketplace add owner/repo\n/plugin install name@marketplace' : start === 'skill' ? '---\nname: my-skill\ndescription: When Claude should use it\n---\nInstructions…' : EXAMPLE)}"></textarea>
     <span class="hint">An MCP server's JSON, a <span class="mono">claude mcp add</span> command, <span class="mono">/plugin</span> commands or a SKILL.md. Nothing is added until you confirm.</span></div>
    ${start === 'skill' ? '' : '<button class="link2" id="a-skill" style="justify-self:start">Or write a new skill</button>'}
    <div id="a-preview"></div>
   </div>`;
  d.classList.add('open');
  const text = $<HTMLTextAreaElement>('#a-text', d);
  const preview = $('#a-preview', d);
  const scopeField = (id: string, allowed: string[]): string => `<div class="field"><label for="${id}">Where it applies</label>
    <select id="${id}">${SCOPES.filter(([k]) => allowed.includes(k)).map(([k, l]) => `<option value="${k}" ${k === (cur ? 'local' : 'user') ? 'selected' : ''}>${esc(l)}</option>`).join('')}</select></div>
    <div class="field" id="${id}-pf" ${cur ? '' : 'hidden'}><label for="${id}-p">Project</label><select id="${id}-p">${projects.map((p) => `<option value="${esc(p.path)}" ${p.path === cur?.path ? 'selected' : ''}>${esc(h.model().projectOf(p.path).label)}</option>`).join('')}</select></div>`;
  const wireScope = (id: string): (() => { scope: string; project: string }) => {
    const sel = $<HTMLSelectElement>('#' + id, d);
    const pf = $('#' + id + '-pf', d);
    sel.onchange = () => { pf.hidden = sel.value === 'user'; };
    pf.hidden = sel.value === 'user';
    return () => ({ scope: sel.value, project: sel.value === 'user' ? '' : $<HTMLSelectElement>('#' + id + '-p', d).value });
  };
  const apply = async (op: string, args: Record<string, unknown>): Promise<void> => {
    if (h.readOnly()) { toast('Read-only mode: start cca without --read-only to make changes.'); return; }
    const r = await act(op, args);
    if (r.confirm) { confirmCommand(r.message, r.confirm, () => void apply(op, { ...args, accept: r.confirm?.sha256 })); return; }
    if (!r.ok) { toast(r.message); return; }
    await h.done(r.message, r.canUndo ? r.activity : undefined);
  };

  // A plugin that installs by running a command: show exactly that command; only approving it
  // re-runs the install with its sha256.
  const confirmCommand = (msg: string, c: { sha256: string; command: Record<string, unknown> }, ok: () => void): void => {
    preview.insertAdjacentHTML('beforeend', `<div class="banner warn" id="a-confirm"><span class="ic2">!</span><div style="min-width:0"><b>This plugin installs by running a command</b>${esc(msg)}
      <pre class="diff2" style="margin-top:8px;padding:8px 12px;white-space:pre-wrap">${esc(JSON.stringify(c.command, null, 2))}</pre>
      <div style="display:flex;gap:8px;margin-top:8px"><button class="btn dng" id="a-run">Run it and install</button><button class="btn" id="a-no">Don't install</button></div></div></div>`);
    $('#a-run', d).onclick = () => { $('#a-confirm', d).remove(); ok(); };
    $('#a-no', d).onclick = () => $('#a-confirm', d).remove();
  };

  const serverCard = (s: InstallServer, i: number, literal: string[]): string => {
    const kv = (title: string, m: Record<string, string> | undefined, kind: 'env' | 'hdr'): string => !m || !Object.keys(m).length ? '' :
      `<div class="field"><label>${title}</label><div class="kvs">${Object.entries(m).map(([k, v]) => {
        const ref = v.includes('${');
        const secret = literal.includes(k);
        return `<div class="kv"><span class="mono">${esc(k)}</span><input data-${kind}="${esc(k)}" value="${esc(v)}" type="${secret ? 'password' : 'text'}" spellcheck="false" autocomplete="off">
          ${ref ? '<span class="hint">from your environment</span>' : secret ? `<button class="link2" data-ref="${esc(k)}" data-kind="${kind}" title="Use \${${esc(envName(k))}}">Use env var</button>` : '<span></span>'}</div>`;
      }).join('')}</div></div>`;
    return `<div class="addcard" data-i="${i}">
      <div class="field"><label>Name</label><input data-name value="${esc(s.name)}" placeholder="letters, numbers, - and _" spellcheck="false"></div>
      <div class="field"><label>${s.type === 'stdio' ? 'Runs on your computer' : `Connects to (${esc(s.type)})`}</label><code class="cmdline">${esc(s.type === 'stdio' ? [s.command, ...(s.args ?? [])].join(' ') : s.url)}</code></div>
      ${kv('Environment', s.env, 'env')}${kv('Headers', s.headers, 'hdr')}
      ${literal.length ? `<div class="banner warn"><span class="ic2">⚿</span><div><b>${literal.length === 1 ? 'A key is' : 'Keys are'} written out in full</b>Choose “Use env var” for ${esc(literal.join(', '))}, then add <code>export NAME=your-key</code> to your shell profile and restart Claude Code. A key written out would sit in plain text in Claude Code's config, and be visible to other programs while it's saved.</div></div>` : ''}
      ${scopeField('a-scope-' + i, ['user', 'project', 'local'])}
      <button class="btn pri" data-add>Add MCP server</button></div>`;
  };

  const render = (plan: InstallPlan, literals: Record<string, string[]>): void => {
    const notes = plan.notes?.length ? `<ul class="hint notes">${plan.notes.map((n) => `<li>${esc(n)}</li>`).join('')}</ul>` : '';
    if (plan.kind === 'mcp') {
      preview.innerHTML = `<div class="banner warn"><span class="ic2">!</span><div><b>MCP servers run with your permissions</b>Add servers only from sources you trust. Below is exactly what Claude Code will run or connect to.</div></div>${notes}` +
        (plan.servers ?? []).map((s, i) => serverCard(s, i, literals[s.name] ?? [])).join('');
      (plan.servers ?? []).forEach((s, i) => {
        const card = $(`.addcard[data-i="${i}"]`, preview);
        const where = wireScope('a-scope-' + i);
        $$<HTMLButtonElement>('[data-ref]', card).forEach((b) => (b.onclick = () => {
          const k = b.dataset.ref ?? '';
          const input = $<HTMLInputElement>(`[data-${b.dataset.kind}="${CSS.escape(k)}"]`, card);
          const env = envName(k);
          input.value = b.dataset.kind === 'hdr' && /^bearer /i.test(input.value) ? `Bearer \${${env}}` : `\${${env}}`;
          input.type = 'text';
          b.replaceWith(Object.assign(document.createElement('span'), { className: 'hint', textContent: 'from your environment' }));
          if (!card.querySelector('[data-ref]')) card.querySelector('.banner.warn')?.remove(); // no key left written out
        }));
        $('[data-add]', card).onclick = () => {
          const read = (kind: string): Record<string, string> | undefined => {
            const xs = $$<HTMLInputElement>(`[data-${kind}]`, card);
            return xs.length ? Object.fromEntries(xs.map((x) => [x.getAttribute(`data-${kind}`) ?? '', x.value])) : undefined;
          };
          const server = { ...s, name: $<HTMLInputElement>('[data-name]', card).value.trim(), env: read('env'), headers: read('hdr') };
          void apply('mcp-add', { server, ...where() });
        };
      });
    } else if (plan.kind === 'plugin') {
      preview.innerHTML = `${notes}<div class="addcard">
        ${plan.marketplace ? `<div class="field"><label>Marketplace</label><code class="cmdline">${esc(plan.marketplace)}</code><span class="hint">Claude Code downloads the catalog from here, then the plugin.</span></div>` : ''}
        ${plan.plugin ? `<div class="field"><label>Plugin</label><code class="cmdline">${esc(plan.plugin)}</code></div>` : '<div class="hint">No plugin named yet: this adds the marketplace only.</div>'}
        <div class="banner warn"><span class="ic2">!</span><div><b>Plugins can add skills, hooks and MCP servers</b>Install plugins only from sources you trust. If one needs to run a command to install, you'll see it first.</div></div>
        ${scopeField('a-scope-p', ['user', 'project', 'local'])}
        <button class="btn pri" id="a-install">${plan.plugin ? 'Install plugin' : 'Add marketplace'}</button></div>`;
      const where = wireScope('a-scope-p');
      $('#a-install', d).onclick = () => void apply('plugin-add', { marketplace: plan.marketplace ?? '', plugin: plan.plugin ?? '', ...where() });
    } else {
      skillForm(plan.skill ?? { name: '', description: '', body: '' });
    }
  };

  const skillForm = (sk: { name: string; description: string; body: string }): void => {
    preview.innerHTML = `<div class="addcard">
      <div class="field"><label for="s-name">Name</label><input id="s-name" value="${esc(sk.name)}" placeholder="release-notes" spellcheck="false"><span class="hint">Lowercase letters, numbers and hyphens. You run it as /${esc(sk.name || 'name')}.</span></div>
      <div class="field"><label for="s-desc">When Claude should use it</label><input id="s-desc" value="${esc(sk.description)}" placeholder="Draft release notes from merged pull requests"></div>
      <div class="field"><label for="s-body">Instructions</label><textarea id="s-body" style="min-height:180px">${esc(sk.body)}</textarea></div>
      ${scopeField('a-scope-s', ['user', 'project'])}
      <button class="btn pri" id="s-create">Create skill</button></div>`;
    const where = wireScope('a-scope-s');
    $('#s-create', d).onclick = () => void apply('skill-create', {
      name: $<HTMLInputElement>('#s-name', d).value.trim(), description: $<HTMLInputElement>('#s-desc', d).value,
      body: $<HTMLTextAreaElement>('#s-body', d).value, ...where(),
    });
  };

  let timer = 0, seq = 0;
  text.oninput = () => {
    clearTimeout(timer);
    timer = window.setTimeout(async () => {
      const mine = ++seq;
      if (!text.value.trim()) { preview.innerHTML = ''; return; }
      const r = await parseInstall(text.value);
      if (mine !== seq) return; // a newer paste is already being read
      if ('error' in r) { preview.innerHTML = `<div class="notice"><div>${esc(r.error)}</div></div>`; return; }
      render(r.plan, r.literals);
    }, 250);
  };
  const sk = document.getElementById('a-skill');
  if (sk) sk.onclick = () => skillForm({ name: '', description: '', body: '' });
  if (start === 'skill') skillForm({ name: '', description: '', body: '' });
  text.focus();
}
