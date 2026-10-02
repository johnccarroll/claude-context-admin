// The memory map: one cluster per project, no node labels (hover for a card), static layout.
import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationNodeDatum } from 'd3-force';
import { select } from 'd3-selection';
import 'd3-transition';
import { zoom as d3zoom, zoomIdentity, zoomTransform, type ZoomTransform } from 'd3-zoom';
import { KIND, type Mem, type Model } from './model';
import { $, cssVar, esc } from './ui';

interface N extends SimulationNodeDatum { m: Mem; r: number }
interface L { source: N | string; target: N | string; cross: boolean }

// Redraws (a save, a live refresh) keep the user's zoom and pan while the same memories are shown.
let view: { sig: string; t: ZoomTransform } | null = null;
let mark: (id: string | null) => void = () => {};
// The force layout is the costly part (about 200 ms for 350 memories): a redraw with the same size,
// memories and links (a live refresh, a save elsewhere) reuses the positions it computed last time.
let layout: { key: string; pos: Map<string, [number, number]> } | null = null;
/** Rings the memory open in the editor and lights its connections, without redrawing. */
export const markMap = (id: string | null): void => mark(id);

/** Places nodes: each project's cluster pulled to its anchor, links holding related memories close. */
function layoutNodes(ns: N[], ls: L[], anchor: Map<string, [number, number]>): void {
  const sim = forceSimulation<N>(ns)
    .force('link', forceLink<N, L & { source: N; target: N }>(ls as never).id((d) => d.m.id)
      .distance((l) => (l.cross ? 120 : 22)).strength((l) => (l.cross ? 0.02 : 0.25)))
    .force('charge', forceManyBody().strength(-14))
    .force('x', forceX<N>((d) => anchor.get(d.m.project)![0]).strength(0.14))
    .force('y', forceY<N>((d) => anchor.get(d.m.project)![1]).strength(0.14))
    .force('collide', forceCollide<N>((d) => d.r + 2.2))
    .stop();
  for (let i = 0; i < 320; i++) sim.tick();
}

export function drawMap(model: Model, visible: (m: Mem) => boolean, open: (m: Mem) => void): void {
  const host = $('#map');
  const svg = select<SVGSVGElement, unknown>('#msvg');
  const box = host.getBoundingClientRect();
  const W = box.width, H = Math.max(box.height, 240); // fit the window; zoom handles the rest
  $('#maphow').textContent = matchMedia('(hover: none)').matches ? 'Tap a dot to open it. Pinch to zoom.' : 'Hover to preview, click to open. Scroll to zoom.';
  svg.attr('viewBox', `0 0 ${W} ${H}`).selectAll('*').remove();

  const mems = model.mems.filter(visible);
  const deg = (m: Mem): number => m.out.length + m.inn.length;
  const ns: N[] = mems.map((m) => ({ m, r: 3.2 + Math.sqrt(deg(m)) * 1.6 }));
  const byId = new Map(ns.map((n) => [n.m.id, n]));
  const ls: L[] = [];
  for (const n of ns) for (const t of n.m.out) if (byId.has(t.id)) ls.push({ source: n.m.id, target: t.id, cross: n.m.project !== t.project });

  // Anchors: Global in the middle, other projects around an ellipse.
  const keys = [...new Set(mems.map((m) => m.project))];
  const ring = keys.filter((k) => k !== 'Global');
  const anchor = new Map<string, [number, number]>([['Global', [W / 2, H / 2]]]);
  ring.forEach((k, i) => {
    const a = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(1, ring.length);
    anchor.set(k, [W / 2 + Math.cos(a) * W * 0.32, H / 2 + Math.sin(a) * H * 0.33]);
  });
  const key = `${W}x${H}|${ns.map((n) => n.m.id).join(',')}|${ls.map((l) => `${l.source}>${l.target}`).join(',')}`;
  if (layout?.key === key) {
    for (const n of ns) [n.x, n.y] = layout.pos.get(n.m.id)!;
    for (const l of ls) { l.source = byId.get(l.source as string)!; l.target = byId.get(l.target as string)!; }
  } else {
    layoutNodes(ns, ls, anchor);
    layout = { key, pos: new Map(ns.map((n) => [n.m.id, [n.x!, n.y!]])) };
  }

  const g = svg.append('g');
  const edge = (l: L): [N, N] => [l.source as N, l.target as N];
  const line = cssVar('var(--line-2)'), accent = cssVar('var(--accent)'), fg = cssVar('var(--fg)');
  const link = g.append('g').selectAll('path').data(ls).join('path')
    .attr('fill', 'none').attr('stroke', (l) => (l.cross ? accent : line)).attr('stroke-width', (l) => (l.cross ? 1 : 0.8))
    .attr('stroke-opacity', (l) => (l.cross ? 0.5 : 0.9))
    .attr('d', (l) => {
      const [s, t] = edge(l);
      if (!l.cross) return `M${s.x},${s.y}L${t.x},${t.y}`;
      const dr = Math.hypot(t.x! - s.x!, t.y! - s.y!) * 1.6;
      return `M${s.x},${s.y}A${dr},${dr} 0 0,1 ${t.x},${t.y}`;
    });
  const node = g.append('g').selectAll('circle').data(ns).join('circle')
    .attr('cx', (d) => d.x!).attr('cy', (d) => d.y!).attr('r', (d) => d.r)
    .attr('fill', (d) => cssVar(model.projectOf(d.m.project).color))
    .attr('stroke', cssVar('var(--glass-strong)')).attr('stroke-width', 1.4).style('cursor', 'pointer');

  const labels = g.append('g');
  for (const k of keys) {
    const members = ns.filter((n) => n.m.project === k);
    const top = Math.min(...members.map((n) => n.y! - n.r));
    const cx = members.reduce((a, n) => a + n.x!, 0) / members.length;
    labels.append('text').attr('class', 'plabel').attr('x', cx).attr('y', top - 10).attr('text-anchor', 'middle')
      .html(`${esc(model.projectOf(k).label)} <tspan>${members.length}</tspan>`);
  }

  const tip = $('#tip');
  const near = (d: N): Set<string> => new Set([d.m.id, ...d.m.out.map((x) => x.id), ...d.m.inn.map((x) => x.id)]);
  const touches = (l: L, d: N): boolean => (l.source as N).m.id === d.m.id || (l.target as N).m.id === d.m.id;
  const glass = cssVar('var(--glass-strong)');
  let picked: N | undefined;
  const rest = (): void => { // resting styles, with the open memory picked out
    node.attr('opacity', 1).attr('stroke', (x) => (x === picked ? accent : glass)).attr('stroke-width', (x) => (x === picked ? 3 : 1.4));
    link.attr('stroke-opacity', (l) => (picked && touches(l, picked) ? 1 : l.cross ? 0.5 : 0.9))
      .attr('stroke', (l) => (picked && touches(l, picked) ? accent : l.cross ? accent : line));
  };
  mark = (id) => { picked = id ? byId.get(id) : undefined; rest(); };
  node
    .on('mouseenter', (_e, d) => {
      const s = near(d);
      node.attr('opacity', (x) => (s.has(x.m.id) ? 1 : 0.15));
      link.attr('stroke-opacity', (l) => (touches(l, d) ? 1 : 0.05)).attr('stroke', (l) => (touches(l, d) ? fg : l.cross ? accent : line));
      tip.hidden = false;
      tip.innerHTML = `<b>${esc(d.m.title)}</b><span>${esc(KIND[d.m.type] ?? d.m.type)} · ${esc(model.projectOf(d.m.project).label)} · ${d.m.out.length + d.m.inn.length} connections</span>${d.m.desc ? `<div style="margin-top:4px">${esc(d.m.desc)}</div>` : ''}`;
    })
    .on('mousemove', (e: MouseEvent) => {
      const b = host.getBoundingClientRect();
      let x = e.clientX - b.left + 14, y = e.clientY - b.top + 14;
      if (x + 290 > b.width) x -= 300;
      if (y + 120 > b.height) y -= 130;
      tip.style.left = x + 'px';
      tip.style.top = y + 'px';
    })
    .on('mouseleave', () => { rest(); tip.hidden = true; })
    .on('click', (_e, d) => {
      tip.hidden = true;
      open(d.m);
      // The editor slides over the right of the map: if it would cover this memory, pan it clear.
      const dw = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--dw')) || 460; // the editor's width (resizable)
      const free = W - dw - 20, t = zoomTransform(svg.node()!), [sx] = t.apply([d.x!, d.y!]);
      if (W > 760 && sx > free - 40) svg.transition().duration(300).call(z.translateBy, (free / 2 - sx) / t.k, 0);
    });

  if (!ns.length) return;
  // Fit everything drawn, project labels included, into the space above the legend.
  const bb = (g.node() as SVGGElement).getBBox();
  const x0 = bb.x - 20, x1 = bb.x + bb.width + 20, y0 = bb.y - 16, y1 = bb.y + bb.height + 16;
  const legend = ($('.maplegend') as HTMLElement).offsetHeight + 24, Hf = Math.max(160, H - legend);
  const k = Math.min(W / (x1 - x0), Hf / (y1 - y0), 1.6);
  const fit = zoomIdentity.translate(W / 2 - (k * (x0 + x1)) / 2, Hf / 2 - (k * (y0 + y1)) / 2).scale(k);
  const sig = `${W}x${H}:${ns.map((n) => n.m.id).join('|')}`;
  const z = d3zoom<SVGSVGElement, unknown>().scaleExtent([0.3, 5])
    .on('zoom', (e) => { g.attr('transform', e.transform); view = { sig, t: e.transform }; });
  svg.call(z).call(z.transform, view?.sig === sig ? view.t : fit);
  $('#mreset').onclick = () => svg.transition().duration(300).call(z.transform, fit);
  $('#mzin').onclick = () => svg.transition().duration(200).call(z.scaleBy, 1.4);
  $('#mzout').onclick = () => svg.transition().duration(200).call(z.scaleBy, 1 / 1.4);
}
