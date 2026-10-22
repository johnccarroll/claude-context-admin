// The 3D map: 3d-force-graph's own spheres, glow, physics and orbit, plus a flat layer of names
// and project capsules drawn over it. Loaded as its own chunk, only when 3D opens.
//
// It sleeps whenever nothing moves (no turn, no camera move, no physics, no pointer): the library's
// render loop is paused and the names layer stops, so an open map costs nothing.
import ForceGraph3D, { type LinkObject, type NodeObject } from '3d-force-graph';
import { Vector2, Vector3 } from 'three';
import { UnrealBloomPass } from 'three/examples/jsm/postprocessing/UnrealBloomPass.js';
import { chipSize, drawChip, tokens, type Hooks, type MapView, type MNode, type Scene, type Tokens } from './map';
import { ARROWS, nextInDirection, placeLabels, seeded, type Box, type Dot, type LabelItem } from './maplogic';
import { cssVar } from './ui';

interface N { id: string; n: MNode; x?: number; y?: number; z?: number; vx?: number; vy?: number; vz?: number }
interface Lk { source: string | N; target: string | N; cross: boolean; ghost?: boolean; twin?: boolean }

// A shade deeper than the 2D palette: glow lifts these to the intended colour instead of white.
const DEEP: Record<string, string> = { 1: '#d98c56', 2: '#45bba9', 3: '#d9729c', 4: '#6d95e3', 5: '#9b7fe6', 6: '#9fbd52', 7: '#c6a26c' };
const SC = 0.62, REL = 2.4, TURN_FOR = 60000, RESUME_AFTER = 5000, PARTICLE_SPEED = 0.0045;
const val = (n: MNode): number => (n.ghost ? 0.6 : 1 + n.deg * 0.9);
const radius = (n: MNode): number => Math.cbrt(val(n)) * REL; // the library's own sphere size
// The library types its callbacks with its own node and link shapes; ours carry more.
const NA = <R>(f: (d: N) => R) => (o: NodeObject): R => f(o as N);
const LA = <R>(f: (l: Lk) => R) => (o: LinkObject): R => f(o as unknown as Lk);
const ends = (l: Lk): [string, string] => [typeof l.source === 'string' ? l.source : l.source.id, typeof l.target === 'string' ? l.target : l.target.id];

export function create(host: HTMLElement, hooks: Hooks): MapView {
  const el = document.createElement('div');
  el.className = 'm3d';
  el.tabIndex = 0;
  el.setAttribute('role', 'application');
  el.setAttribute('aria-label', 'Memory map in 3D. Drag to turn it. Arrow keys move between memories, Enter opens one, Escape clears.');
  host.append(el);
  const lc = document.createElement('canvas');
  lc.className = 'm3dnames';
  host.append(lc);
  const lx = lc.getContext('2d')!;

  let S: Scene | null = null, T: Tokens = tokens(), on = true;
  let nodes: N[] = [], byId = new Map<string, N>();
  let hover: string | null = null, selected: string | null = null, kb: string | null = null, near: Set<string> | null = null;
  let raf = 0, paused = false, awakeUntil = 0, rotateUntil = 0, engine = false, touched = false, idle = 0, pulseUntil = 0;
  // Ambient motion (the turn and signals on every link) runs for a minute after any input, then rests.
  let liveUntil = performance.now() + TURN_FOR, ambient = true, settle = 0;
  let px = 0, py = 0, last = performance.now(), dtAvg = 16.7, dtN = 0;
  const rand = seeded(0.17);
  const colors = new Map<string, string>();
  const col = (p: string): string => colors.get(p) ?? colors.set(p, (() => { const v = S?.color(p) ?? ''; const m = v.match(/--c(\d)/); return m ? DEEP[m[1]] : cssVar(v) || '#66666f'; })()).get(p)!;
  let W = Math.max(1, host.clientWidth), H = Math.max(1, host.clientHeight); // updated on resize, never read per frame
  const focus = (): string | null => hover ?? kb ?? selected;
  const touchesFocus = (l: Lk): boolean => { const f = focus(); if (!f) return false; const [s, t] = ends(l); return s === f || t === f; };

  const g = new ForceGraph3D(el, { controlType: 'orbit' })
    .width(host.clientWidth).height(host.clientHeight)
    .backgroundColor('rgba(0,0,0,0)').showNavInfo(false)
    .nodeId('id')
    // The library's labels render HTML: never pass memory text to them. Names are drawn below.
    .nodeLabel(() => '').linkLabel(() => '')
    .enableNodeDrag(false) // 3d-force-graph 1.80.1's drag throws on click with three r186; a drag turns the map
    .nodeVisibility(NA((d) => !!S?.shown.has(d.id)))
    .linkVisibility(LA((l) => { const [s, t] = ends(l); return !!S?.shown.has(s) && !!S?.shown.has(t); }))
    .nodeVal(NA((d) => val(d.n))).nodeRelSize(REL).nodeResolution(16).nodeOpacity(0.92)
    .nodeColor(NA((d) => ((near && !near.has(d.id)) ? '#26262e' : col(d.n.project))))
    .linkColor(LA((l) => (touchesFocus(l) ? '#ffffff' : l.twin ? '#d8d8e2' : l.cross ? '#8a78d8' : col(byId.get(ends(l)[0])?.n.project ?? ''))))
    .linkOpacity(0.3)
    .linkWidth(LA((l) => (touchesFocus(l) ? 0.5 : 0)))
    // Signals only on the focused memory's links, for a few seconds.
    .linkDirectionalParticles(LA((l) => (hooks.reduce() || l.twin || l.ghost ? 0 : touchesFocus(l) ? 3 : !ambient ? 0 : l.cross ? 2 : 1)))
    .linkDirectionalParticleWidth(1.2).linkDirectionalParticleSpeed(PARTICLE_SPEED).linkDirectionalParticleColor(() => '#ffffff')
    .cooldownTime(15000) // the map lifts and settles on screen, as in the mockup
    .onEngineStop(() => { engine = false; })
    .onNodeHover((o: NodeObject | null) => {
      const d = o as N | null;
      hover = d?.id ?? null; el.style.cursor = d ? 'pointer' : '';
      refresh();
      hooks.hover(d && d.id !== selected ? d.id : null, px, py);
      clearTimeout(idle);
      if (d) ctl.autoRotate = false; else if (!selected) idle = window.setTimeout(spin, 2500);
    })
    .onNodeClick(NA((d) => { if (!d.n.ghost) hooks.open(d.id); }));
  (g.d3Force('charge') as unknown as { strength: (v: number) => void } | undefined)?.strength(-45);
  (g.d3Force('link') as unknown as { distance: (f: (l: Lk) => number) => { strength: (f: (l: Lk) => number) => void } } | undefined)?.distance((l) => (l.cross ? 80 : 24)).strength((l) => (l.twin ? 0 : l.cross ? 0.02 : 0.45));
  // One addition to the library's forces: a light pull toward each project's place on the 2D map,
  // so projects sit where people know them from 2D.
  let pts: N[] = [];
  const anchor = new Map<string, [number, number]>();
  const cluster = Object.assign((alpha: number) => {
    for (const d of pts) { const a = anchor.get(d.n.project); if (a) { d.vx! += (a[0] - d.x!) * 0.04 * alpha; d.vy! += (a[1] - d.y!) * 0.04 * alpha; } }
  }, { initialize: (ns: N[]) => { pts = ns; } });
  g.d3Force('cluster', cluster as never);

  // Glow at half resolution; the renderer at most 2× the screen's pixels.
  const renderer = g.renderer();
  renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 2));
  const bloom = new UnrealBloomPass(new Vector2(host.clientWidth, host.clientHeight), 1.0, 0.4, 0.12);
  g.postProcessingComposer().addPass(bloom);

  const ctl = g.controls() as unknown as { autoRotate: boolean; autoRotateSpeed: number; enableDamping: boolean; target: Vector3; addEventListener: (t: string, f: () => void) => void };
  ctl.autoRotateSpeed = 0.7; ctl.enableDamping = true;
  const spin = (): void => {
    if (!hooks.reduce() && !hover && !selected && !kb && performance.now() < rotateUntil) { ctl.autoRotate = true; wake(); }
  };
  ctl.addEventListener('start', () => { touched = true; live(); ctl.autoRotate = false; clearTimeout(idle); rotateUntil = performance.now() + TURN_FOR; wake(); });
  ctl.addEventListener('change', () => wake(300));
  ctl.addEventListener('end', () => { clearTimeout(idle); idle = window.setTimeout(spin, RESUME_AFTER); });

  // Input keeps it awake; the library's hover test runs in its render loop.
  el.addEventListener('pointermove', (e) => { const r = host.getBoundingClientRect(); px = e.clientX - r.left; py = e.clientY - r.top; live(); wake(600); });
  el.addEventListener('pointerleave', () => { hooks.hover(null); });
  el.addEventListener('wheel', () => { live(); wake(600); }, { passive: true });
  function live(): void { liveUntil = performance.now() + TURN_FOR; }

  // ---------- Sleep and wake ----------
  function wake(ms = 1200): void {
    if (!on) return;
    awakeUntil = Math.max(awakeUntil, performance.now() + ms);
    if (paused) { paused = false; g.resumeAnimation(); }
    if (!raf) raf = requestAnimationFrame(tick);
  }
  function tick(now: number): void {
    raf = 0;
    const dt = Math.min(100, now - last); last = now;
    // Particle speed is per frame in the library: scale it so signals move at the same speed at
    // 60, 120 or any other refresh rate.
    dtAvg += (dt - dtAvg) * 0.05;
    if (++dtN % 60 === 0) g.linkDirectionalParticleSpeed(PARTICLE_SPEED * dtAvg / 16.67);
    if (ctl.autoRotate && now > rotateUntil) ctl.autoRotate = false;
    const amb = !hooks.reduce() && now < liveUntil;
    if (amb !== ambient) { ambient = amb; g.linkDirectionalParticles(g.linkDirectionalParticles()); }
    const fading = names(dt);
    if (on && (ctl.autoRotate || engine || fading || ambient || now < awakeUntil || now < pulseUntil)) raf = requestAnimationFrame(tick);
    else if (on) { paused = true; g.pauseAnimation(); }
  }

  function refresh(): void {
    const f = focus();
    near = f ? S?.near.get(f) ?? null : null;
    pulseUntil = f && !hooks.reduce() ? performance.now() + 2500 : 0;
    g.nodeColor(g.nodeColor()).linkColor(g.linkColor()).linkWidth(g.linkWidth()).linkDirectionalParticles(g.linkDirectionalParticles());
    wake(pulseUntil ? 2600 : 600);
  }

  // ---------- Camera ----------
  /** The opening view: face-on, framing every shown memory, then the slow turn. */
  function home(ms: number): void {
    const shown = nodes.filter((d) => S?.shown.has(d.id));
    if (!shown.length) return;
    const xs = shown.map((d) => d.x ?? 0), ys = shown.map((d) => d.y ?? 0);
    const cx = (Math.min(...xs) + Math.max(...xs)) / 2, cy = (Math.min(...ys) + Math.max(...ys)) / 2;
    const hx = (Math.max(...xs) - Math.min(...xs)) / 2 + 40, hy = (Math.max(...ys) - Math.min(...ys)) / 2 + 60;
    const tf = Math.tan((g.camera() as unknown as { fov: number }).fov * Math.PI / 360), aspect = hooks.freeWidth() / Math.max(1, host.clientHeight);
    const z = Math.max(hy / tf, hx / (tf * aspect)) * 1.08;
    ctl.autoRotate = false; clearTimeout(idle);
    g.cameraPosition({ x: cx, y: cy, z }, { x: cx, y: cy, z: 0 }, ms);
    rotateUntil = performance.now() + ms + TURN_FOR; live();
    idle = window.setTimeout(spin, ms + 50);
    wake(ms + 300);
  }
  /** Flies to a memory and keeps it clear of the editor, which covers the map's right side. */
  function fly(d: N, ms: number): void {
    ctl.autoRotate = false; clearTimeout(idle);
    const p = new Vector3(d.x ?? 0, d.y ?? 0, d.z ?? 0), dist = 200;
    const dir = p.clone().normalize(); if (!dir.lengthSq()) dir.set(0, 0, 1);
    const cam = p.clone().add(dir.multiplyScalar(dist));
    // Shift both camera and target right, so the memory sits in the middle of the free area.
    const free = hooks.freeWidth();
    const tf = Math.tan((g.camera() as unknown as { fov: number }).fov * Math.PI / 360);
    const shiftPx = W / 2 - free / 2, perPx = (2 * dist * tf) / H;
    const right = new Vector3().subVectors(p, cam).cross(new Vector3(0, 1, 0)).normalize().multiplyScalar(shiftPx * perPx);
    g.cameraPosition({ x: cam.x + right.x, y: cam.y + right.y, z: cam.z + right.z }, { x: p.x + right.x, y: p.y + right.y, z: p.z + right.z }, hooks.reduce() ? 0 : ms);
    wake(ms + 300);
  }

  // ---------- Keyboard ----------
  el.addEventListener('keydown', (e) => {
    const dir = ARROWS[e.key];
    if (dir) {
      e.preventDefault();
      const pts2 = projected().filter((p) => !p.d.n.ghost);
      const from = pts2.find((p) => p.d.id === (kb ?? selected)) ?? [...pts2].sort((a, b) => b.d.n.deg - a.d.n.deg)[0];
      if (!from) return;
      const next = kb === null && selected === null ? from.d.id : nextInDirection({ key: from.d.id, x: from.x, y: from.y }, pts2.map((p) => ({ key: p.d.id, x: p.x, y: p.y })), dir);
      if (!next) return;
      kb = next; ctl.autoRotate = false; clearTimeout(idle); refresh();
      const p = pts2.find((q) => q.d.id === next)!;
      if (p.x < 60 || p.y < 60 || p.x > hooks.freeWidth() - 60 || p.y > H - 60) fly(p.d, 500);
      const n = byId.get(next)!.n;
      hooks.announce(`${n.title}, ${S!.label(n.project)}, ${n.deg} connection${n.deg === 1 ? '' : 's'}`);
    } else if (e.key === 'Enter' && kb) hooks.open(kb);
    else if (e.key === 'Escape' && kb) { kb = null; refresh(); idle = window.setTimeout(spin, RESUME_AFTER); }
  });
  el.addEventListener('blur', () => { if (kb) { kb = null; refresh(); } });

  // ---------- Names layer ----------
  const v = new Vector3();
  const la = new Map<string, number>(), side = new Map<string, 1 | -1>(), pa = new Map<string, number>();
  interface P { d: N; x: number; y: number; dist: number; px: number }
  function projected(): P[] {
    const cam = g.camera() as unknown as { position: Vector3; fov: number };
    const focal = H / (2 * Math.tan((cam.fov * Math.PI) / 360));
    const out: P[] = [];
    for (const d of nodes) {
      if (!S?.shown.has(d.id) || d.x === undefined) continue;
      v.set(d.x, d.y!, d.z!); const dist = cam.position.distanceTo(v); v.project(cam as never);
      if (v.z > 1) continue; // behind the camera
      out.push({ d, x: ((v.x + 1) / 2) * W, y: ((1 - v.y) / 2) * H, dist, px: (radius(d.n) * focal) / dist });
    }
    return out;
  }
  /** Draws names and capsules; returns true while any is still fading. */
  function names(dt: number): boolean {
    const dpr = Math.min(devicePixelRatio || 1, 2);
    if (lc.width !== Math.round(W * dpr) || lc.height !== Math.round(H * dpr)) { lc.width = Math.round(W * dpr); lc.height = Math.round(H * dpr); lc.style.width = W + 'px'; lc.style.height = H + 'px'; }
    lx.setTransform(dpr, 0, 0, dpr, 0, 0); lx.clearRect(0, 0, W, H);
    if (!S) return false;
    const e = hooks.reduce() ? 1 : 1 - Math.exp(-dt / 140), f = focus(), cam = g.camera() as unknown as { position: Vector3 };
    const scr = projected();
    let fading = false;
    // Project capsules first, nearest first; one that would sit on another fades until they part.
    const fixed: Box[] = [...hooks.boxes()], caps: { p: string; x: number; y: number; a: number }[] = [];
    const byP = new Map<string, N[]>();
    for (const d of nodes) if (!d.n.ghost && S.shown.has(d.id) && d.x !== undefined) (byP.get(d.n.project) ?? byP.set(d.n.project, []).get(d.n.project)!).push(d);
    const plist = [...byP].map(([p, m]) => {
      v.set(m.reduce((s, d) => s + d.x!, 0) / m.length, Math.max(...m.map((d) => d.y!)) + 16, m.reduce((s, d) => s + d.z!, 0) / m.length);
      const dist = cam.position.distanceTo(v); v.project(cam as never);
      return { p, x: ((v.x + 1) / 2) * W, y: ((1 - v.y) / 2) * H, dist, behind: v.z > 1 };
    }).filter((q) => !q.behind).sort((a, b) => a.dist - b.dist);
    const placedCaps: Box[] = [], hud = hooks.boxes();
    for (const q of plist) {
      const label = S.label(q.p);
      const [w] = chipSize(lx, T, label, S.count(q.p));
      q.x = Math.max(w / 2 + 4, Math.min(W - w / 2 - 4, q.x)); // where drawChip will put it
      const box: Box = [q.x - w / 2 - 3, q.y - 12, q.x + w / 2 + 3, q.y + 12];
      const ok = ![...placedCaps, ...hud].some((b) => b[0] < box[2] && b[2] > box[0] && b[1] < box[3] && b[3] > box[1]); // never under a control
      if (ok) placedCaps.push(box);
      const cur = pa.get(q.p) ?? 0, target = ok ? 1 : 0;
      pa.set(q.p, Math.abs(cur - target) < 0.02 ? target : cur + (target - cur) * e);
      if (Math.abs(cur - target) >= 0.02) fading = true;
      caps.push({ p: q.p, x: q.x, y: q.y, a: pa.get(q.p)! });
    }
    fixed.push(...placedCaps);
    // Memory names: nearest and busiest first; the focused one always; nearer ones brighter.
    let near0 = Infinity, far0 = 0;
    for (const s of scr) { near0 = Math.min(near0, s.dist); far0 = Math.max(far0, s.dist); }
    const depth = (s: P): number => (far0 > near0 ? 1 - (s.dist - near0) / (far0 - near0) : 1);
    const mid = new Map<string, [number, number]>();
    for (const s of scr) { const c = mid.get(s.d.n.project) ?? [0, 0]; c[0] += s.x; c[1]++; mid.set(s.d.n.project, c); }
    const want = (s: P): boolean => (near ? near.has(s.d.id) : !s.d.n.ghost && s.px > 3.2);
    const fontOf = (big: boolean): string => `${big ? 600 : 500} ${big ? 14 : 12.5}px ${T.ui}`;
    lx.letterSpacing = '0.01em'; // a touch of tracking: light text over glow reads better
    const order = scr.filter(want).sort((a, b) => (+(b.d.id === f) - +(a.d.id === f)) || depth(b) * (1 + b.d.n.deg / 6) - depth(a) * (1 + a.d.n.deg / 6));
    const items: LabelItem[] = order.map((s) => {
      const big = s.d.id === f; lx.font = fontOf(big);
      const c = mid.get(s.d.n.project)!;
      return { key: s.d.id, x: s.x, y: s.y, r: s.px, w: lx.measureText(s.d.n.name).width, h: 18, prefer: s.x < c[0] / c[1] - 4 ? -1 : 1, last: side.get(s.d.id), big, dist: s.dist };
    });
    const dots: Dot[] = scr.map((s) => ({ key: s.d.id, x: s.x, y: s.y, r: s.px, dist: s.dist }));
    const placed = placeLabels(items, fixed, dots, { ignoreDots: !!near, bounds: [4, 4, W - 4, H - 4] });
    lx.textBaseline = 'middle';
    for (const s of scr) {
      const id = s.d.id, cur = la.get(id) ?? 0, target = placed.has(id) ? 1 : 0;
      if (placed.has(id)) side.set(id, placed.get(id)!);
      const a = Math.abs(cur - target) < 0.02 ? target : cur + (target - cur) * e;
      if (a !== target) fading = true;
      la.set(id, a);
      const sd = side.get(id);
      if (a < 0.03 || !sd) continue;
      const big = id === f;
      lx.font = fontOf(big); lx.textAlign = sd > 0 ? 'left' : 'right';
      const x = s.x + sd * (s.px + 6), alpha = a * (big ? 1 : 0.5 + 0.5 * depth(s));
      lx.lineWidth = 5; lx.lineJoin = 'round'; lx.strokeStyle = `rgba(3,3,6,${0.88 * alpha})`; lx.strokeText(s.d.n.name, x, s.y);
      lx.fillStyle = `rgba(242,242,245,${alpha})`; lx.fillText(s.d.n.name, x, s.y);
    }
    lx.letterSpacing = '0px';
    for (const c of caps) if (c.a > 0.03) drawChip(lx, T, S.label(c.p), S.count(c.p), c.x, c.y, col(c.p), c.a * (f ? 0.5 : S.gone(c.p) ? 0.6 : 1), true);
    lx.textAlign = 'start';
    return fading;
  }

  // ---------- Data ----------
  function update(s: Scene): void {
    const prev = S; S = s; colors.clear();
    if (prev?.key !== s.key) {
      // Reuse each memory's object so the library keeps its place; new ones start at their 2D
      // spot with a spread in depth, so every project is a cloud rather than a sheet.
      const old = byId;
      nodes = s.nodes.map((n) => {
        const o = old.get(n.id);
        if (o) { o.n = n; return o; }
        const [x, y] = s.pos.get(n.id)!;
        return { id: n.id, n, x: x * SC, y: -y * SC, z: (rand() - 0.5) * 140 };
      });
      byId = new Map(nodes.map((d) => [d.id, d]));
      anchor.clear();
      for (const p of s.projects) { const m = s.nodes.filter((n) => n.project === p).map((n) => s.pos.get(n.id)!); anchor.set(p, [m.reduce((a, q) => a + q[0], 0) / m.length * SC, -m.reduce((a, q) => a + q[1], 0) / m.length * SC]); }
      const links: Lk[] = s.links.map((l) => ({ source: l.s, target: l.t, cross: l.cross, ghost: l.ghost, twin: l.twin }));
      engine = true;
      if (!prev) {
        // First frame: face-on, framed on the 2D layout, which the 3D one closely follows.
        const xs = nodes.map((d) => d.x!), ys = nodes.map((d) => d.y!);
        const tf = Math.tan((g.camera() as unknown as { fov: number }).fov * Math.PI / 360), aspect = hooks.freeWidth() / Math.max(1, host.clientHeight);
        const hx = (Math.max(...xs) - Math.min(...xs)) / 2 + 40, hy = (Math.max(...ys) - Math.min(...ys)) / 2 + 60;
        g.cameraPosition({ x: 0, y: 0, z: Math.max(hy / tf, hx / (tf * aspect)) * 1.15 }, { x: 0, y: 0, z: 0 });
        rotateUntil = performance.now() + TURN_FOR;
        settle = window.setTimeout(() => { if (!touched) home(hooks.reduce() ? 0 : 900); }, 1200);
      }
      g.graphData({ nodes, links });
    } else {
      g.nodeVisibility(g.nodeVisibility()).linkVisibility(g.linkVisibility());
    }
    if (hover && !s.shown.has(hover)) hover = null;
    refresh();
  }

  const ro = new ResizeObserver(() => { W = Math.max(1, host.clientWidth); H = Math.max(1, host.clientHeight); g.width(W).height(H); wake(300); });
  ro.observe(host);
  const T0 = (): void => { T = tokens(); wake(300); };

  return {
    update, theme: T0,
    mark(id) {
      if (id === selected) return;
      selected = id; refresh();
      const d = id ? byId.get(id) : undefined;
      if (d) fly(d, 900);
      else { clearTimeout(idle); idle = window.setTimeout(spin, RESUME_AFTER); }
    },
    zoom(f) { // toward the point being looked at, not the scene's origin
      const c = (g.camera() as unknown as { position: Vector3 }).position, t = ctl.target;
      g.cameraPosition({ x: t.x + (c.x - t.x) / f, y: t.y + (c.y - t.y) / f, z: t.z + (c.z - t.z) / f }, { x: t.x, y: t.y, z: t.z }, hooks.reduce() ? 0 : 300);
      wake(500);
    },
    locate(id) { const p = projected().find((q) => q.d.id === id); return p ? [p.x, p.y] : null; },
    reset() { kb = null; hover = null; touched = true; hooks.hover(null); el.style.cursor = ''; refresh(); home(hooks.reduce() ? 0 : 900); },
    active(v2) {
      on = v2;
      if (v2) wake(600);
      else { cancelAnimationFrame(raf); raf = 0; paused = true; g.pauseAnimation(); }
    },
    destroy() { on = false; cancelAnimationFrame(raf); clearTimeout(idle); clearTimeout(settle); ro.disconnect(); g._destructor(); el.remove(); lc.remove(); },
  };
}
