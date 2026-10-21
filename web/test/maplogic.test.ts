import { expect, test } from 'bun:test';
import { hull, layout, layoutKey, nextInDirection, overlaps, placeLabels, type Box, type LabelItem, type LayoutLink, type LayoutNode } from '../src/maplogic';

const item = (key: string, x: number, y: number, o: Partial<LabelItem> = {}): LabelItem => ({ key, x, y, r: 4, w: 60, h: 18, prefer: 1, ...o });
const boxOf = (it: LabelItem, side: 1 | -1): Box => {
  const x0 = side > 0 ? it.x + it.r + 6 : it.x - it.r - 6 - it.w;
  return [x0 - 3, it.y - it.h / 2, x0 + it.w + 3, it.y + it.h / 2];
};

test('names never overlap each other', () => {
  const items = Array.from({ length: 60 }, (_, i) => item('m' + i, (i % 10) * 40, Math.floor(i / 10) * 12));
  const placed = placeLabels(items, [], []);
  const boxes = [...placed].map(([k, s]) => boxOf(items.find((i) => i.key === k)!, s));
  expect(boxes.length).toBeGreaterThan(0);
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) expect(overlaps(boxes[i], boxes[j])).toBe(false);
});

test('a name takes its preferred side, else the other, else none', () => {
  const a = item('a', 100, 100, { prefer: -1 });
  expect(placeLabels([a], [], []).get('a')).toBe(-1);
  // block the left: it goes right
  expect(placeLabels([a], [boxOf(a, -1)], []).get('a')).toBe(1);
  // block both: no name
  expect(placeLabels([a], [boxOf(a, -1), boxOf(a, 1)], []).has('a')).toBe(false);
});

test('a name keeps last frame\'s side while it fits', () => {
  const a = item('a', 100, 100, { prefer: 1, last: -1 });
  expect(placeLabels([a], [], []).get('a')).toBe(-1);
});

test('fixed boxes (capsules, controls) block names', () => {
  const a = item('a', 100, 100);
  expect(placeLabels([a], [[0, 0, 400, 400]], []).has('a')).toBe(false);
});

test('a dot in front blocks a name; one behind does not; the focused memory ignores both', () => {
  const a = item('a', 100, 100, { dist: 500, prefer: 1 });
  const right = boxOf(a, 1), dotAt = { x: (right[0] + right[2]) / 2, y: 100, r: 3 };
  const left = boxOf(a, -1), dotLeft = { x: (left[0] + left[2]) / 2, y: 100, r: 3 };
  const front = [{ key: 'f', ...dotAt, dist: 300 }, { key: 'g', ...dotLeft, dist: 300 }];
  const behind = [{ key: 'f', ...dotAt, dist: 900 }];
  expect(placeLabels([a], [], front).has('a')).toBe(false);
  expect(placeLabels([a], [], behind).get('a')).toBe(1);
  expect(placeLabels([{ ...a, big: true }], [], front).get('a')).toBe(1);
  expect(placeLabels([a], [], front, { ignoreDots: true }).get('a')).toBe(1);
});

test('a memory\'s own dot never blocks its name', () => {
  const a = item('a', 100, 100, { r: 40 });
  expect(placeLabels([a], [], [{ key: 'a', x: 100, y: 100, r: 40 }]).get('a')).toBe(1);
});

test('arrow keys pick the nearest memory in that direction', () => {
  const pts = [{ key: 'o', x: 0, y: 0 }, { key: 'r', x: 50, y: 5 }, { key: 'far', x: 200, y: 0 }, { key: 'u', x: 0, y: -40 }, { key: 'diag', x: 30, y: 60 }];
  const o = pts[0];
  expect(nextInDirection(o, pts, [1, 0])).toBe('r');
  expect(nextInDirection(o, pts, [0, -1])).toBe('u');
  expect(nextInDirection(o, pts, [0, 1])).toBe('diag');
  expect(nextInDirection(o, pts, [-1, 0])).toBeUndefined();
});

test('hull wraps every point', () => {
  const pts: [number, number][] = [[0, 0], [10, 0], [10, 10], [0, 10], [5, 5], [2, 8]];
  const h = hull(pts);
  expect(h.length).toBe(4);
  expect(new Set(h.map((p) => p.join()))).toEqual(new Set(['0,0', '10,0', '10,10', '0,10']));
});

const ns: LayoutNode[] = ['a/1', 'a/2', 'a/3', 'b/1', 'b/2', 'G/1'].map((id) => ({ id, project: id.split('/')[0], r: 5 }));
const ls: LayoutLink[] = [{ s: 'a/1', t: 'a/2', cross: false }, { s: 'a/2', t: 'a/3', cross: false }, { s: 'b/1', t: 'G/1', cross: true }];

test('the layout is the same on every run', () => {
  expect([...layout(ns, ls, 'G')]).toEqual([...layout(ns, ls, 'G')]);
});

test('the layout keys on memories and links only', () => {
  expect(layoutKey(ns, ls)).toBe(layoutKey([...ns], [...ls]));
  expect(layoutKey(ns, ls)).not.toBe(layoutKey(ns, ls.slice(1)));
});

test('a small change starts from the old places and moves little', () => {
  const before = layout(ns, ls, 'G');
  const after = layout(ns, [...ls, { s: 'a/3', t: 'a/1', cross: false }], 'G', before);
  for (const [id, [x, y]] of before) {
    const [x2, y2] = after.get(id)!;
    expect(Math.hypot(x2 - x, y2 - y)).toBeLessThan(40);
  }
});

test('projects sit apart', () => {
  const p = layout(ns, ls, 'G');
  const c = (k: string): [number, number] => { const m = [...p].filter(([id]) => id.startsWith(k + '/')).map(([, v]) => v); return [m.reduce((s, v) => s + v[0], 0) / m.length, m.reduce((s, v) => s + v[1], 0) / m.length]; };
  const [a, b] = [c('a'), c('b')];
  expect(Math.hypot(a[0] - b[0], a[1] - b[1])).toBeGreaterThan(150);
});

test('names stay inside the map, trying the other side at an edge', () => {
  const a = item('a', 380, 100); // near the right edge of a 400 px map
  expect(placeLabels([a], [], [], { bounds: [0, 0, 400, 300] }).get('a')).toBe(-1);
  const tight = item('b', 200, 100, { w: 500 });
  expect(placeLabels([tight], [], [], { bounds: [0, 0, 400, 300] }).has('b')).toBe(false);
});
