import { expect, test } from 'bun:test';
import { CompletionContext } from '@codemirror/autocomplete';
import { markdown } from '@codemirror/lang-markdown';
import { EditorSelection, EditorState, type Transaction } from '@codemirror/state';
import { code, linkCompletions, prefixLines, wrapWith, type Links } from '../src/editor';

/** Runs a command on a document with a selection; returns the text and selection after. */
function run(cmd: (t: { state: EditorState; dispatch: (tr: Transaction) => void }) => boolean, doc: string, from: number, to = from): { doc: string; from: number; to: number } {
  let state = EditorState.create({ doc, selection: EditorSelection.single(from, to), extensions: [markdown()] });
  cmd({ state, dispatch: (tr) => { state = tr.state; } });
  const r = state.selection.main;
  return { doc: state.doc.toString(), from: r.from, to: r.to };
}

test('wrapWith: wraps a selection, keeps it selected; empty selection opens a pair to type into', () => {
  expect(run(wrapWith('**'), 'say hi', 4, 6)).toEqual({ doc: 'say **hi**', from: 6, to: 8 });
  expect(run(wrapWith('**'), 'say ', 4)).toEqual({ doc: 'say ****', from: 6, to: 6 });
});

test('wrapWith: wrapping again unwraps', () => {
  expect(run(wrapWith('**'), 'say **hi**', 6, 8)).toEqual({ doc: 'say hi', from: 4, to: 6 });
});

test('code: inline for one line, a fenced block for several', () => {
  expect(run(code, 'x = 1', 0, 5).doc).toBe('`x = 1`');
  expect(run(code, 'a\nb', 0, 3).doc).toBe('```\na\nb\n```');
});

test('prefixLines: cycles heading sizes and toggles list marks on every selected line', () => {
  const h = prefixLines(['', '# ', '## ', '### ']);
  expect(run(h, 'Title', 0).doc).toBe('# Title');
  expect(run(h, '# Title', 0).doc).toBe('## Title');
  expect(run(h, '### Title', 0).doc).toBe('Title');
  expect(run(prefixLines(['', '- ']), 'one\ntwo', 0, 7).doc).toBe('- one\n- two');
  expect(run(prefixLines(['', '- ']), '- one\n- two', 0, 11).doc).toBe('one\ntwo');
});

const links: Links = {
  candidates: () => [{ id: '1', title: 'Deploy checklist', slug: 'feedback_deploy_checklist', color: '#000', where: 'Everywhere', summary: '' }],
  resolve: () => null, pick: () => {},
};

test('[[ completion: offers memories after [[, writes the slug and steps over a closing ]]', () => {
  const at = (doc: string, pos: number) => linkCompletions(links)(new CompletionContext(EditorState.create({ doc, extensions: [markdown()] }), pos, false));
  expect(at('See [[dep', 9)?.options.map((o) => o.label)).toEqual(['Deploy checklist']);
  expect(at('See dep', 7)).toBeNull();
  expect(at('`[[dep`', 6)).toBeNull(); // inside code, nothing is a link
  let state = EditorState.create({ doc: 'See [[dep]]', selection: EditorSelection.single(9), extensions: [markdown()] });
  const r = at(state.doc.toString(), 9)!;
  const view = { state, dispatch: (tr: { changes?: unknown; selection?: unknown }) => { state = state.update(tr as never).state; } };
  (r.options[0].apply as (v: unknown, c: unknown, from: number, to: number) => void)(view, r.options[0], r.from, 9);
  expect(state.doc.toString()).toBe('See [[feedback_deploy_checklist]]');
  expect(state.selection.main.from).toBe(state.doc.length);
});
