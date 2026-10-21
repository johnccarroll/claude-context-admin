// The map's pure logic, shared by the 2D and 3D views and covered by bun test: the stable layout,
// project regions, where names go, and which memory an arrow key moves to.
import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationNodeDatum } from 'd3-force';

export type Pt = [number, number];
/** [left, top, right, bottom] in screen pixels. */
export type Box = [number, number, number, number];

// ---------- Layout ----------

export interface LayoutNode { id: string; project: string; r: number }
export interface LayoutLink { s: string; t: string; cross: boolean; ghost?: boolean }

/** Where each project sits: Everywhere in the middle, the rest on an ellipse. A fixed virtual
 *  canvas, so resizing the window never moves anything; the view fits it to the screen. */
export function anchors(projects: string[], global: string): Map<string, Pt> {
  const ring = projects.filter((p) => p !== global);
  const out = new Map<string, Pt>([[global, [0, 0]]]);
  ring.forEach((p, i) => {
    const t = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(1, ring.length);
    out.set(p, [Math.cos(t) * 360, Math.sin(t) * 230]);
  });
  return out;
}

/** A seeded random stream, so the same memories always land in the same places. */
export function seeded(seed = 0.42): () => number {
  let s = Math.floor(seed * 2 ** 31) || 1;
  return () => ((s = (s * 48271) % 2147483647) - 1) / 2147483646;
}

interface SimNode extends SimulationNodeDatum { n: LayoutNode }

/** Places every memory: each project pulled to its anchor, links holding related memories close.
 *  Memories already placed (prev) start where they were, so a change moves little. */
export function layout(ns: LayoutNode[], ls: LayoutLink[], global: string, prev?: Map<string, Pt>): Map<string, Pt> {
  const A = anchors([...new Set(ns.map((n) => n.project))].sort(), global);
  const rand = seeded();
  const sn: SimNode[] = ns.map((n) => {
    const p = prev?.get(n.id), a = A.get(n.project)!;
    return { n, x: p ? p[0] : a[0] + (rand() - 0.5) * 30, y: p ? p[1] : a[1] + (rand() - 0.5) * 30 };
  });
  const byId = new Map(sn.map((d) => [d.n.id, d]));
  const links = ls.filter((l) => byId.has(l.s) && byId.has(l.t)).map((l) => ({ source: byId.get(l.s)!, target: byId.get(l.t)!, cross: l.cross, ghost: l.ghost }));
  const sim = forceSimulation(sn)
    .force('link', forceLink(links).distance((l) => (l.cross ? 150 : l.ghost ? 18 : 26)).strength((l) => (l.cross ? 0.012 : 0.32)))
    .force('charge', forceManyBody().strength(-42).distanceMax(240))
    .force('x', forceX<SimNode>((d) => A.get(d.n.project)![0]).strength(0.1))
    .force('y', forceY<SimNode>((d) => A.get(d.n.project)![1]).strength(0.1))
    .force('collide', forceCollide<SimNode>((d) => d.n.r + 3))
    .velocityDecay(0.38).stop();
  // A model that only changed a little needs fewer ticks to settle from where it was.
  const ticks = prev && sn.every((d) => prev.has(d.n.id)) ? 120 : 360;
  for (let i = 0; i < ticks; i++) sim.tick();
  return new Map(sn.map((d) => [d.n.id, [d.x!, d.y!]]));
}

/** The cache key for a layout: the memories and links, not the window or the filter. */
export const layoutKey = (ns: LayoutNode[], ls: LayoutLink[]): string =>
  ns.map((n) => n.id).join('|') + '#' + ls.map((l) => `${l.s}>${l.t}`).join('|');

/** Convex hull (Andrew's monotone chain), counter-clockwise; a project's region is drawn around it. */
export function hull(points: Pt[]): Pt[] {
  const p = [...points].sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  if (p.length < 3) return p;
  const cross = (o: Pt, a: Pt, b: Pt): number => (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0]);
  const lo: Pt[] = [], up: Pt[] = [];
  for (const q of p) { while (lo.length >= 2 && cross(lo[lo.length - 2], lo[lo.length - 1], q) <= 0) lo.pop(); lo.push(q); }
  for (const q of p.reverse()) { while (up.length >= 2 && cross(up[up.length - 2], up[up.length - 1], q) <= 0) up.pop(); up.push(q); }
  return [...lo.slice(0, -1), ...up.slice(0, -1)];
}

// ---------- Names ----------

/** Boxes and dots bucketed by area, so overlap checks stay cheap with hundreds of memories. */
export class Grid<T extends { box: Box }> {
  private cells = new Map<number, T[]>();
  constructor(private size = 64) {}
  private keys(b: Box): number[] {
    const out: number[] = [], s = this.size;
    for (let x = Math.floor(b[0] / s); x <= Math.floor(b[2] / s); x++) for (let y = Math.floor(b[1] / s); y <= Math.floor(b[3] / s); y++) out.push(x * 100003 + y);
    return out;
  }
  add(item: T): void { for (const k of this.keys(item.box)) (this.cells.get(k) ?? this.cells.set(k, []).get(k)!).push(item); }
  /** The first stored item overlapping b that passes the test, if any. */
  hit(b: Box, test: (item: T) => boolean = () => true): T | undefined {
    for (const k of this.keys(b)) for (const it of this.cells.get(k) ?? []) if (overlaps(it.box, b) && test(it)) return it;
    return undefined;
  }
}
export const overlaps = (a: Box, b: Box): boolean => a[0] < b[2] && a[2] > b[0] && a[1] < b[3] && a[3] > b[1];

export interface LabelItem {
  key: string;
  x: number; y: number; r: number; // the dot, on screen
  w: number; h: number;            // the name's size
  prefer: 1 | -1;                  // the side away from its cluster's centre
  last?: 1 | -1;                   // the side it had last frame: kept while it fits
  big?: boolean;                   // the focused memory: always shown
  dist?: number;                   // depth (3D): only nearer dots block it
}
export interface Dot { key: string; x: number; y: number; r: number; dist?: number }

/** Places names in the order given (most important first). A name may not cross another name,
 *  a fixed box (capsules, controls) or a dot in front of its own; it tries its preferred side,
 *  then the other. Returns the side each placed name took. */
export function placeLabels(items: LabelItem[], fixed: Box[], dots: Dot[], opts: { ignoreDots?: boolean; gap?: number; bounds?: Box } = {}): Map<string, 1 | -1> {
  const gap = opts.gap ?? 6;
  const names = new Grid<{ box: Box }>();
  for (const b of fixed) names.add({ box: b });
  const dotGrid = new Grid<{ box: Box; d: Dot }>();
  if (!opts.ignoreDots) for (const d of dots) dotGrid.add({ box: [d.x - d.r, d.y - d.r, d.x + d.r, d.y + d.r], d });
  const out = new Map<string, 1 | -1>();
  for (const it of items) {
    const first = it.last ?? it.prefer;
    for (const side of [first, -first as 1 | -1]) {
      const x0 = side > 0 ? it.x + it.r + gap : it.x - it.r - gap - it.w;
      const box: Box = [x0 - 3, it.y - it.h / 2, x0 + it.w + 3, it.y + it.h / 2];
      const b = opts.bounds;
      if (b && (box[0] < b[0] || box[2] > b[2] || box[1] < b[1] || box[3] > b[3])) continue; // never off the map's edge
      if (!it.big) {
        if (names.hit(box)) continue;
        if (dotGrid.hit(box, ({ d }) => d.key !== it.key && (it.dist === undefined || d.dist === undefined || d.dist < it.dist + 20))) continue;
      }
      names.add({ box });
      out.set(it.key, side);
      break;
    }
  }
  return out;
}

// ---------- Keyboard ----------

export interface NavPoint { key: string; x: number; y: number }

/** The nearest memory in an arrow's direction, favouring ones straight ahead. */
export function nextInDirection(from: NavPoint, all: NavPoint[], dir: Pt): string | undefined {
  let best: string | undefined, score = Infinity;
  for (const p of all) {
    if (p.key === from.key) continue;
    const dx = p.x - from.x, dy = p.y - from.y, dist = Math.hypot(dx, dy);
    if (!dist) continue;
    const cos = (dx * dir[0] + dy * dir[1]) / dist;
    if (cos < 0.45) continue; // more than ~63° off the arrow's direction
    const s = dist * (2 - cos);
    if (s < score) { score = s; best = p.key; }
  }
  return best;
}

export const ARROWS: Record<string, Pt> = { ArrowRight: [1, 0], ArrowLeft: [-1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
