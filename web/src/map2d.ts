// The 2D map: memories on a Canvas, each project a soft region, links blending between project
// colours. d3-force runs only while a memory is dragged; otherwise nothing draws unless something
// changed, so an open map costs nothing.
import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type Simulation, type SimulationNodeDatum } from 'd3-force';
import { select } from 'd3-selection';
import 'd3-transition';
import { zoom as d3zoom, zoomIdentity, type ZoomBehavior, type ZoomTransform } from 'd3-zoom';
import { drawChip, rgba, tokens, type Hooks, type MapView, type MLink, type MNode, type Scene, type Tokens } from './map';
import { ARROWS, hull, nextInDirection, placeLabels, type Box, type Dot, type LabelItem, type Pt } from './maplogic';
import { cssVar } from './ui';

interface D extends SimulationNodeDatum {
  n: MNode; x: number; y: number;
  hx: number; hy: number;          // home: where the layout puts it
  from?: Pt; t0?: number; delay?: number; dur?: number; // a move in progress (first bloom, or a changed layout)
  a: number; ta: number;           // emphasis, eased toward its target
  la: number; side?: 1 | -1;       // its name's opacity and side
}
interface L { source: D; target: D; l: MLink; h: number; th: number }

const ease4 = (p: number): number => 1 - Math.pow(1 - p, 4);

export function create(host: HTMLElement, hooks: Hooks): MapView {
  const canvas = document.createElement('canvas');
  canvas.tabIndex = 0;
  canvas.setAttribute('role', 'application');
  canvas.setAttribute('aria-label', 'Memory map. Arrow keys move between memories, Enter opens one, Escape clears.');
  host.append(canvas);
  const ctx = canvas.getContext('2d')!;
  let W = 1, H = 1, dpr = 1, t: ZoomTransform = zoomIdentity, raf = 0, on = true, dirty = true, last = performance.now();
  let S: Scene | null = null, nodes: D[] = [], links: L[] = [], byId = new Map<string, D>();
  let hover: string | null = null, selected: string | null = null, kb: string | null = null, near: Set<string> | null = null;
  let dragging: D | null = null, pulseUntil = 0, first = true, sim: Simulation<D, undefined> | null = null;
  let T: Tokens = tokens();
  const colorOf = (p: string): string => cssVar(S?.color(p) ?? 'var(--faint)');
  let sprites = new Map<string, HTMLCanvasElement>();

  const size = (): void => {
    const b = host.getBoundingClientRect();
    W = Math.max(1, b.width); H = Math.max(1, b.height); dpr = Math.min(devicePixelRatio || 1, 2);
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
    canvas.style.width = W + 'px'; canvas.style.height = H + 'px';
    dirty = true; wake();
  };
  const ro = new ResizeObserver(size);
  ro.observe(host);
  size();

  // Glow: one cached sprite per colour, so it costs a drawImage instead of a blur.
  function theme(): void {
    T = tokens(); sprites = new Map();
    dirty = true; wake();
  }
  function sprite(c: string): HTMLCanvasElement {
    let s = sprites.get(c);
    if (!s) {
      s = document.createElement('canvas'); s.width = s.height = 64;
      const g = s.getContext('2d')!, grd = g.createRadialGradient(32, 32, 0, 32, 32, 32);
      grd.addColorStop(0, rgba(c, 0.55)); grd.addColorStop(0.35, rgba(c, 0.18)); grd.addColorStop(1, rgba(c, 0));
      g.fillStyle = grd; g.fillRect(0, 0, 64, 64); sprites.set(c, s);
    }
    return s;
  }

  // Pan and zoom; a press on a memory drags it instead.
  const zoom: ZoomBehavior<HTMLCanvasElement, unknown> = d3zoom<HTMLCanvasElement, unknown>().scaleExtent([0.3, 6])
    .filter((e: MouseEvent & TouchEvent) => {
      if (e.type === 'wheel') return true;
      if (e.button) return false;
      const p = e.touches?.[0] ?? e;
      const r = canvas.getBoundingClientRect();
      return !hit(p.clientX - r.left, p.clientY - r.top);
    })
    .on('zoom', (e) => { t = e.transform; dirty = true; wake(); });
  const sel = select(canvas).call(zoom).on('dblclick.zoom', null);
  const glide = (ms: number) => (hooks.reduce() || !ms ? sel : sel.transition().duration(ms));

  function hit(px: number, py: number): D | null {
    const [x, y] = t.invert([px, py]);
    let best: D | null = null, bd = Infinity;
    for (const d of nodes) {
      if (!S?.shown.has(d.n.id)) continue;
      const dd = Math.hypot(d.x - x, d.y - y);
      if (dd < d.n.r + 6 / t.k && dd < bd) { bd = dd; best = d; }
    }
    return best;
  }

  // ---------- Pointer: hover, drag, click ----------
  let press: { d: D; x: number; y: number; moved: boolean } | null = null;
  canvas.addEventListener('pointerdown', (e) => {
    const r = canvas.getBoundingClientRect(), d = hit(e.clientX - r.left, e.clientY - r.top);
    if (!d || e.button) return;
    canvas.setPointerCapture(e.pointerId);
    press = { d, x: e.clientX, y: e.clientY, moved: false };
  });
  canvas.addEventListener('pointermove', (e) => {
    const r = canvas.getBoundingClientRect(), px = e.clientX - r.left, py = e.clientY - r.top;
    if (press) {
      if (!press.moved && Math.hypot(e.clientX - press.x, e.clientY - press.y) > 4) {
        press.moved = true; dragging = press.d; hooks.hover(null);
        live().alphaTarget(0.25).restart();
      }
      if (dragging) { const [x, y] = t.invert([px, py]); dragging.fx = x; dragging.fy = y; dirty = true; wake(); }
      return;
    }
    const d = hit(px, py);
    const id = d?.n.id ?? null;
    if (id !== hover) { hover = id; retarget(); }
    canvas.style.cursor = d ? 'pointer' : 'grab';
    hooks.hover(d && d.n.id !== selected ? d.n.id : null, px, py);
  });
  const release = (e: PointerEvent): void => {
    if (!press) return;
    const p = press; press = null;
    if (dragging) { dragging.fx = dragging.fy = null; dragging = null; sim?.alphaTarget(0); }
    else if (!p.moved && !p.d.n.ghost) hooks.open(p.d.n.id);
    canvas.releasePointerCapture(e.pointerId);
  };
  canvas.addEventListener('pointerup', release);
  canvas.addEventListener('pointercancel', release);
  canvas.addEventListener('pointerleave', () => { if (!press) { hover = null; retarget(); hooks.hover(null); } });

  // ---------- Keyboard: arrows move between memories by direction ----------
  canvas.addEventListener('keydown', (e) => {
    const dir = ARROWS[e.key];
    const shown = nodes.filter((d) => S?.shown.has(d.n.id) && !d.n.ghost);
    if (dir) {
      e.preventDefault();
      const from = byId.get(kb ?? selected ?? '') ?? [...shown].sort((a, b) => b.n.deg - a.n.deg)[0];
      if (!from) return;
      const pts = shown.map((d) => { const [x, y] = t.apply([d.x, d.y]); return { key: d.n.id, x, y }; });
      const [fx, fy] = t.apply([from.x, from.y]);
      const next = kb === null && selected === null ? from.n.id : nextInDirection({ key: from.n.id, x: fx, y: fy }, pts, dir);
      if (!next) return;
      kb = next; retarget();
      const d = byId.get(next)!;
      hooks.announce(`${d.n.title}, ${S!.label(d.n.project)}, ${d.n.deg} connection${d.n.deg === 1 ? '' : 's'}`);
      const [sx, sy] = t.apply([d.x, d.y]);
      if (sx < 60 || sy < 60 || sx > hooks.freeWidth() - 60 || sy > H - 60) fly(d, 400);
    } else if (e.key === 'Enter' && kb) hooks.open(kb);
    else if (e.key === 'Escape' && kb) { kb = null; retarget(); }
  });
  canvas.addEventListener('blur', () => { if (kb) { kb = null; retarget(); } });

  // ---------- Physics, only while dragging: memories spring back toward their places ----------
  function live(): Simulation<D, undefined> {
    if (sim) return sim;
    sim = forceSimulation(nodes)
      .force('link', forceLink<D, L>(links.filter((l) => !l.l.twin)).distance((l) => (l.l.cross ? 150 : l.l.ghost ? 18 : 26)).strength((l) => (l.l.cross ? 0.012 : 0.32)))
      .force('charge', forceManyBody().strength(-42).distanceMax(240))
      .force('x', forceX<D>((d) => d.hx).strength(0.08))
      .force('y', forceY<D>((d) => d.hy).strength(0.08))
      .force('collide', forceCollide<D>((d) => d.n.r + 3))
      .velocityDecay(0.38)
      .on('tick', () => { dirty = true; wake(); });
    return sim;
  }

  // ---------- Emphasis ----------
  function retarget(): void {
    const f = hover ?? kb ?? selected;
    near = f ? S?.near.get(f) ?? null : null;
    for (const d of nodes) {
      const shown = S?.shown.has(d.n.id) ?? false;
      d.ta = !shown ? 0 : (near && !near.has(d.n.id) ? 0.12 : 1) * (S?.gone(d.n.project) ? 0.5 : 1);
    }
    for (const l of links) l.th = f && (l.source.n.id === f || l.target.n.id === f) ? 1 : 0;
    pulseUntil = f && !hooks.reduce() ? performance.now() + 2500 : 0;
    dirty = true; wake();
  }

  function fit(ms: number): void {
    const shown = nodes.filter((d) => S?.shown.has(d.n.id));
    if (!shown.length) return;
    const xs = shown.map((d) => d.hx), ys = shown.map((d) => d.hy);
    const x0 = Math.min(...xs) - 60, x1 = Math.max(...xs) + 60, y0 = Math.min(...ys) - 60, y1 = Math.max(...ys) + 30;
    const fw = hooks.freeWidth(), k = Math.min(fw / (x1 - x0), (H - 70) / (y1 - y0), 1.7);
    glide(ms).call(zoom.transform, zoomIdentity.translate(fw / 2 - k * (x0 + x1) / 2, (H - 50) / 2 - k * (y0 + y1) / 2 + 10).scale(k));
  }
  function fly(d: D, ms: number): void {
    const k = Math.max(t.k, 1.9);
    glide(ms).call(zoom.transform, zoomIdentity.translate(hooks.freeWidth() / 2 - d.x * k, H / 2 - d.y * k).scale(k));
  }

  // ---------- Data ----------
  function update(s: Scene): void {
    const prev = S; S = s;
    if (prev?.key !== s.key) {
      const now = performance.now(), old = byId;
      nodes = s.nodes.map((n) => {
        const [hx, hy] = s.pos.get(n.id)!;
        const o = old.get(n.id);
        if (o) { o.n = n; o.from = [o.x, o.y]; o.t0 = now; o.delay = 0; o.dur = 400; o.hx = hx; o.hy = hy; return o; }
        return { n, x: hx, y: hy, hx, hy, a: 0, ta: 1, la: 0 };
      });
      byId = new Map(nodes.map((d) => [d.n.id, d]));
      links = s.links.filter((l) => byId.has(l.s) && byId.has(l.t)).map((l) => ({ source: byId.get(l.s)!, target: byId.get(l.t)!, l, h: 0, th: 0 }));
      sim?.stop(); sim = null;
      // First show: each project blooms out of its centre, busiest memories first.
      if (first && !hooks.reduce()) {
        const c = new Map<string, Pt>();
        for (const p of s.projects) { const m = nodes.filter((d) => d.n.project === p); c.set(p, [m.reduce((a, d) => a + d.hx, 0) / m.length, m.reduce((a, d) => a + d.hy, 0) / m.length]); }
        [...nodes].sort((a, b) => b.n.deg - a.n.deg).forEach((d, i) => { d.from = c.get(d.n.project)!; d.t0 = now; d.delay = Math.min(i * 6, 500); d.dur = 700; [d.x, d.y] = d.from; });
      }
    }
    if (first) { first = false; fit(0); }
    if (hover && !s.shown.has(hover)) hover = null;
    retarget();
  }

  // ---------- Frame loop: runs only while something changes ----------
  function wake(): void { if (on && !raf) raf = requestAnimationFrame(frame); }
  function frame(now: number): void {
    raf = 0;
    const dt = Math.min(64, now - last); last = now;
    const e = 1 - Math.exp(-dt / 90);
    let moving = false;
    for (const d of nodes) {
      if (d.from && d.t0 !== undefined) {
        const p = Math.max(0, Math.min(1, (now - d.t0 - (d.delay ?? 0)) / (d.dur ?? 400)));
        const k = hooks.reduce() ? 1 : ease4(p);
        d.x = d.from[0] + (d.hx - d.from[0]) * k; d.y = d.from[1] + (d.hy - d.from[1]) * k;
        if (p >= 1 || hooks.reduce()) { d.from = undefined; d.x = d.hx; d.y = d.hy; } else moving = true;
      }
      if (Math.abs(d.a - d.ta) > 0.003) { d.a += (d.ta - d.a) * (hooks.reduce() ? 1 : e); moving = true; } else d.a = d.ta;
    }
    for (const l of links) if (Math.abs(l.h - l.th) > 0.005) { l.h += (l.th - l.h) * e; moving = true; } else l.h = l.th;
    const pulsing = now < pulseUntil;
    if (dirty || moving || pulsing) {
      dirty = false;
      moving = draw(now, pulsing, e) || moving;
    }
    if (moving || pulsing || dirty) wake();
  }

  /** Draws one frame; returns true while names are still fading. */
  function draw(now: number, pulsing: boolean, e: number): boolean {
    if (!S) return false;
    const f = hover ?? kb ?? selected;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    ctx.save(); ctx.translate(t.x, t.y); ctx.scale(t.k, t.k);
    const k = t.k;
    // Regions
    for (const p of S.projects) {
      const m = nodes.filter((d) => d.n.project === p && !d.n.ghost && d.a > 0.01);
      if (!m.length) continue;
      const pts: Pt[] = [];
      for (const d of m) for (let i = 0; i < 10; i++) { const a = (i / 10) * Math.PI * 2, r = d.n.r + 16; pts.push([d.x + Math.cos(a) * r, d.y + Math.sin(a) * r]); }
      const h = hull(pts);
      const pa = m.reduce((s, d) => s + d.a, 0) / m.length, c = colorOf(p);
      ctx.beginPath();
      // Smooth the hull by curving through each edge's midpoint.
      for (let i = 0; i < h.length; i++) {
        const p0 = h[i], p1 = h[(i + 1) % h.length], mx = (p0[0] + p1[0]) / 2, my = (p0[1] + p1[1]) / 2;
        if (i === 0) ctx.moveTo(mx, my); else ctx.quadraticCurveTo(p0[0], p0[1], mx, my);
      }
      const p0 = h[0], p1 = h[1 % h.length]; ctx.quadraticCurveTo(p0[0], p0[1], (p0[0] + p1[0]) / 2, (p0[1] + p1[1]) / 2);
      ctx.fillStyle = rgba(c, (T.dark ? 0.055 : 0.07) * pa); ctx.fill();
      ctx.lineWidth = 1 / k; ctx.strokeStyle = rgba(c, (T.dark ? 0.16 : 0.22) * pa); ctx.stroke();
    }
    // Links
    ctx.lineCap = 'round';
    for (const l of links) {
      const s = l.source, g = l.target, a = Math.min(s.a, g.a);
      if (a < 0.01) continue;
      if (l.l.twin) { // the same memory in two projects: dotted, curving the other way
        const mx = (s.x + g.x) / 2, my = (s.y + g.y) / 2, dx = g.x - s.x, dy = g.y - s.y;
        ctx.setLineDash([1.5 / k, 5 / k]); ctx.lineWidth = (1.4 + l.h) / k; ctx.strokeStyle = rgba(T.fg, 0.4 * a + l.h * 0.5);
        ctx.beginPath(); ctx.moveTo(s.x, s.y); ctx.quadraticCurveTo(mx + dy * 0.22, my - dx * 0.22, g.x, g.y); ctx.stroke(); ctx.setLineDash([]);
        continue;
      }
      ctx.lineWidth = ((l.l.cross ? 1 : 0.9) + l.h * 1.1) / k;
      ctx.setLineDash(l.l.ghost ? [3 / k, 3 / k] : []);
      if (l.l.cross) {
        const mx = (s.x + g.x) / 2, my = (s.y + g.y) / 2, dx = g.x - s.x, dy = g.y - s.y;
        const grd = ctx.createLinearGradient(s.x, s.y, g.x, g.y), base = (T.dark ? 0.32 : 0.4) * a + l.h * 0.6;
        grd.addColorStop(0, rgba(colorOf(s.n.project), base)); grd.addColorStop(1, rgba(colorOf(g.n.project), base));
        ctx.strokeStyle = grd; ctx.beginPath(); ctx.moveTo(s.x, s.y); ctx.quadraticCurveTo(mx - dy * 0.18, my + dx * 0.18, g.x, g.y); ctx.stroke();
      } else {
        ctx.strokeStyle = l.l.ghost ? rgba(T.faint, 0.6 * a) : rgba(colorOf(s.n.project), (T.dark ? 0.42 : 0.5) * a + l.h * 0.5);
        ctx.beginPath(); ctx.moveTo(s.x, s.y); ctx.lineTo(g.x, g.y); ctx.stroke();
      }
    }
    ctx.setLineDash([]);
    // Signals travelling out from the focused memory, for a moment after focus
    if (f && pulsing) {
      for (const l of links) {
        if (l.h < 0.05 || l.l.ghost || l.l.twin) continue;
        const out = l.source.n.id === f, s = out ? l.source : l.target, g = out ? l.target : l.source;
        const cx = l.l.cross ? (s.x + g.x) / 2 - (g.y - s.y) * 0.18 * (out ? 1 : -1) : 0, cy = l.l.cross ? (s.y + g.y) / 2 + (g.x - s.x) * 0.18 * (out ? 1 : -1) : 0;
        for (let i = 0; i < 2; i++) {
          const p = (now / 1100 + i / 2) % 1, u = 1 - p;
          const x = l.l.cross ? u * u * s.x + 2 * u * p * cx + p * p * g.x : s.x + (g.x - s.x) * p;
          const y = l.l.cross ? u * u * s.y + 2 * u * p * cy + p * p * g.y : s.y + (g.y - s.y) * p;
          ctx.fillStyle = rgba(T.dark ? '#ffffff' : colorOf(g.n.project), l.h * Math.sin(p * Math.PI) * 0.9);
          ctx.beginPath(); ctx.arc(x, y, 1.8 / k, 0, Math.PI * 2); ctx.fill();
        }
      }
    }
    // Glow, additive on dark
    ctx.globalCompositeOperation = T.dark ? 'lighter' : 'source-over';
    for (const d of nodes) {
      if (d.n.ghost || d.a < 0.02) continue;
      const r = d.n.r * (d.n.id === f ? 5.7 : 4.2);
      ctx.globalAlpha = d.a * (T.dark ? 0.9 : 0.35);
      ctx.drawImage(sprite(colorOf(d.n.project)), d.x - r, d.y - r, r * 2, r * 2);
    }
    ctx.globalAlpha = 1; ctx.globalCompositeOperation = 'source-over';
    // Memories
    for (const d of nodes) {
      if (d.a < 0.01) continue;
      ctx.beginPath(); ctx.arc(d.x, d.y, d.n.r, 0, Math.PI * 2);
      if (d.n.ghost) { ctx.setLineDash([2 / k, 2 / k]); ctx.lineWidth = 1.2 / k; ctx.strokeStyle = rgba(T.muted, 0.8 * d.a); ctx.stroke(); ctx.setLineDash([]); continue; }
      ctx.fillStyle = rgba(colorOf(d.n.project), d.a); ctx.fill();
      ctx.lineWidth = 1.3 / k; ctx.strokeStyle = rgba(T.ground, 0.9 * d.a); ctx.stroke();
      if (d.n.id === selected || d.n.id === kb) {
        ctx.beginPath(); ctx.arc(d.x, d.y, d.n.r + 4 / k, 0, Math.PI * 2);
        ctx.lineWidth = 2 / k; ctx.strokeStyle = T.accent; ctx.stroke();
      }
    }
    ctx.restore();
    // Project capsules first, so memory names steer around them
    const fixed: Box[] = [...hooks.boxes()], caps: [string, number, number, number][] = [];
    for (const p of S.projects) {
      const m = nodes.filter((d) => d.n.project === p && !d.n.ghost && S!.shown.has(d.n.id));
      if (!m.length) continue;
      const [sx, sy] = t.apply([m.reduce((a, d) => a + d.x, 0) / m.length, Math.min(...m.map((d) => d.y - d.n.r))]);
      const y = sy - 16 * Math.min(1.4, k) - 6, pa = Math.max(0.4, m.reduce((a, d) => a + d.a, 0) / m.length) * (S.gone(p) ? 0.6 : 1);
      caps.push([p, sx, y, pa]);
    }
    for (const [p, x, y, a] of caps) fixed.push(drawChip(ctx, T, S.label(p), S.count(p), x, y, colorOf(p), a, T.dark));
    // Memory names in screen space, crisp at any zoom; none overlap
    const items: LabelItem[] = [], dots: Dot[] = [];
    const want = (d: D): boolean => {
      if (!S!.shown.has(d.n.id)) return false;
      if (near) return near.has(d.n.id);
      if (d.n.ghost) return k > 2.2;
      return d.n.r * k > 6.8 || k > 2;
    };
    const mid = new Map<string, [number, number]>();
    for (const d of nodes) { if (d.a < 0.05) continue; const [x, y] = t.apply([d.x, d.y]); dots.push({ key: d.n.id, x, y, r: d.n.r * k + 1 }); const c = mid.get(d.n.project) ?? [0, 0]; c[0] += x; c[1]++; mid.set(d.n.project, c); }
    ctx.textBaseline = 'middle';
    const fontOf = (big: boolean): string => `${big ? 600 : 500} ${big ? 13.5 : 12}px ${T.ui}`;
    const order = nodes.filter(want).sort((a, b) => (+(b.n.id === f) - +(a.n.id === f)) || b.n.deg - a.n.deg);
    for (const d of order) {
      const [x, y] = t.apply([d.x, d.y]), big = d.n.id === f;
      ctx.font = fontOf(big);
      const c = mid.get(d.n.project)!;
      items.push({ key: d.n.id, x, y, r: d.n.r * k, w: ctx.measureText(d.n.title).width, h: 18, prefer: x < c[0] / c[1] - 4 ? -1 : 1, last: d.side, big });
    }
    const placed = placeLabels(items, fixed, dots, { ignoreDots: !!near });
    let fading = false;
    for (const d of nodes) {
      const target = placed.has(d.n.id) ? 1 : 0;
      if (placed.has(d.n.id)) d.side = placed.get(d.n.id);
      if (Math.abs(d.la - target) > 0.02) { d.la += (target - d.la) * (hooks.reduce() ? 1 : e); fading = true; } else d.la = target;
      if (d.la < 0.03 || !d.side) continue;
      const [x, y] = t.apply([d.x, d.y]), big = d.n.id === f;
      ctx.font = fontOf(big); ctx.textAlign = d.side > 0 ? 'left' : 'right';
      const tx = x + d.side * (d.n.r * k + 6);
      ctx.lineWidth = 4; ctx.lineJoin = 'round'; ctx.strokeStyle = rgba(T.ground, 0.85 * d.la); ctx.strokeText(d.n.title, tx, y);
      ctx.fillStyle = rgba(d.n.ghost ? T.faint : T.fg, (big ? 1 : 0.88) * d.la * Math.max(d.a, 0.35)); ctx.fillText(d.n.title, tx, y);
    }
    ctx.textAlign = 'start';
    return fading;
  }

  return {
    update, theme,
    mark(id) {
      if (id === selected) return;
      selected = id; retarget();
      const d = id ? byId.get(id) : undefined;
      if (d) fly(d, 600);
    },
    zoom: (f) => glide(220).call(zoom.scaleBy, f),
    reset() { kb = null; hover = null; retarget(); fit(480); },
    active(v) { on = v; if (v) { dirty = true; wake(); } else if (raf) { cancelAnimationFrame(raf); raf = 0; } },
    destroy() { on = false; cancelAnimationFrame(raf); sim?.stop(); ro.disconnect(); sel.on('.zoom', null); canvas.remove(); },
  };
}
