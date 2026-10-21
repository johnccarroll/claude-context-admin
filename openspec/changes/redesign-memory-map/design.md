# Design

## Context

See proposal.md for why. The map today is `web/src/map.ts`: an SVG drawn with d3, laid out once
per redraw, called from `main.ts` as `drawMap(model, memShown, openMemory)` on every render (a
filter change, a save, a live refresh) and `markMap(id)` when the editor opens or closes a memory.
The filter box and chips already decide which memories are shown, and the list and the map agree.
The editor is the app's drawer; the map never shows its own card.

Constraints that shape the approach:

- **cca ships to colleagues' managed work machines.** The UI is embedded in the Go binary; the
  server sends `Content-Security-Policy: default-src 'self'`; CI runs the binary with loopback only.
  Nothing may load from a CDN or font host, and every GPU feature needs a path that works without
  one.
- The UI is vanilla TypeScript bundled by Bun (`web/build.ts`), 66 KB gzipped today.
- The prototype (artifact "Memory Map Redesign", seven rounds of review with John) settled the look.
  The last round's lesson: stay close to 3d-force-graph's own rendering and physics; custom
  meshes, rims, rings and extra forces made it worse. The names layer and project capsules stay.

## Goals / Non-Goals

**Goals:** a 3D map as the default and a 2D map one tap away, with identical data, filters, names,
capsules, selection and keyboard model; idle at about 0% CPU; 500 memories smooth on a slow laptop;
120 Hz where the browser allows; no network; a 2D fallback wherever WebGL is missing or slow.

**Non-Goals:** sigma.js or any other renderer; a light-theme 3D stage (the 3D stage stays dark in
both themes, like a media viewer); editing from the map (the drawer does that); lifting WebKit's
60 fps cap in the Mac app through private API.

## Decisions

### D1. Two views behind one module

`map.ts` keeps `drawMap`/`markMap` as its public API and owns the shared chrome: the toolbar
(2D | 3D, zoom out, zoom in, Reset view, Key), tooltips, the key popover and the live region.
It hosts one view at a time through an interface:

```ts
interface MapView {
  update(model: Model, shown: (m: Mem) => boolean): void; // same ids keep their places
  mark(id: string | null): void;
  zoom(factor: number): void;
  reset(): void;
  destroy(): void;
}
```

`map2d.ts` is the Canvas view; `map3d.ts` the WebGL view, loaded with `import()` so Bun emits it
as its own chunk (build gains `splitting: true`). Pure logic (label placement, capsule sizing,
spatial keyboard navigation, layout seeding) lives in `maplogic.ts`, which `bun test` covers.

*Alternative considered:* one renderer for both (3d-force-graph has a 2D sibling, force-graph).
Rejected: it adds a second 56 KB library, and the 2D view's regions and gradient links are ours.

### D2. 3D renders with 3d-force-graph, natively

Spheres, glow (UnrealBloomPass), links, particles, orbit and fly-in are the library's. Ours are a
flat names layer (a 2D canvas over the WebGL one, projected each frame) and one extra force that
pulls each project toward its place on the 2D map. Nodes start at their 2D x and y with a random
z spread of ±70, so every project is a cloud.

Node drag is off: with 3d-force-graph 1.80.1 and three r186 a click on a node throws in
DragControls. A drag orbits.

*Alternatives considered:* custom meshes and materials (tried in the prototype; rejected by John),
cosmos.gl (no labels), sigma 4 (two weeks old, 2D only).

### D3. Positions are stable

Layout runs over every memory, not the filtered set, and is cached by the set of ids and links.
A filter only hides memories (2D skips them; 3D uses `nodeVisibility`), so nothing moves when you
type. When memories or links change, the new layout is seeded from the old positions and the 2D
view tweens from old to new over 400 ms; the 3D view reuses its node objects so the library keeps
their positions.

### D4. Idle means idle

- 2D: the simulation is marked settled after layout (its `alpha` set to 0), and the canvas draws
  only when something changed. The prototype redrew every frame because a stopped simulation
  still reports `alpha` 1.
- 3D: `warmupTicks` settles the layout before the first frame; `cooldownTicks` stops the physics.
  Particles run only on the focused memory's links. When nothing moves (no turn, no focus, no
  camera move, no pointer) the view calls `pauseAnimation()` and the names layer stops; any input
  resumes both. The turn stops on its own after 30 s; Reset view starts it again.
- Both pause when the map isn't the current view, the page is hidden, or the window is not
  visible.
- The renderer's pixel ratio is capped at 2, and bloom renders at half resolution.

Measured in the prototype at 500 memories (M5 Pro, Chromium): physics 14 points of main-thread
time, particles 12, drawing 13, names 3; idle total 70%. Target: under 1% idle; 50 fps or more while
turning with the CPU slowed 4×.

### D5. Frame-rate independence

Everything is timed by elapsed time, so it runs at the display's rate: 120 Hz in Chrome and Edge on ProMotion
(Firefox to be confirmed on hardware); 60 in Safari and in the Mac app's WKWebView, which caps pages near 60 by
default. The one per-frame quantity in the library, particle speed, is scaled each second by the
measured frame interval.

### D6. Labels and capsules

Names are drawn as text on a canvas (never HTML), in the app's own font. Project names are tinted
capsules in tracked capitals with their count; memory names are plain. Placement is greedy in
priority order (focused, then wanted, then nearest and busiest): a name must not cross another
name, a capsule, a floating control or a dot in front of its own; it tries the side away from its
cluster's centre first and keeps its side while it fits; losers fade out over about 140 ms.
Overlap checks use a uniform grid so cost stays linear at 500 memories. Control positions are
read on resize, not every frame.

### D7. Fallbacks

The 3D option is offered only when `getContext('webgl2', { failIfMajorPerformanceCaveat: true })`
succeeds, so software-rendered GPUs on managed and virtual machines get 2D. Reduce Motion opens
2D (3D stays one tap away, without the turn or particles). The choice is remembered per browser
(`localStorage`, wrapped in try/catch).

### D8. Security and packaging

- `three@0.186.1` and `3d-force-graph@1.80.1` are added with exact versions; the bundle must
  contain one copy of three. The tree is 35 packages: MIT, ISC and BSD-3, no install scripts,
  no known vulnerabilities (`bun audit` already runs in CI).
- `scripts/notices.sh` lists every npm package in the bundle, not only `d3-*` and fonts.
- The CSP stays `default-src 'self'`: three needs neither `eval` nor workers, and no file is
  fetched at runtime. The existing loopback-only CI job proves it.
- `nodeLabel`/`linkLabel` render HTML, so they are always empty; memory text reaches the screen
  only through `fillText` or `esc()`.

## Risks / Trade-offs

- [3D adds about 364 KB gzipped to the binary] → its own chunk, loaded only when 3D opens; 2D
  users never parse it.
- [Single-maintainer library] → exact pins, the bundle is ours once built, and 2D is a complete
  fallback.
- [Names drawn over the scene can sit over a dot further back] → accepted; the dot-in-front check
  removes the worst cases.
- [Mac app stays at 60 Hz] → documented; no private API.
- [Bun code splitting is newer than the rest of the build] → the build checks that the chunk
  exists, and the smoke test opens 3D.

## Migration Plan

UI only; no data or API change. The old SVG map is deleted in the same change. Rollback is a
revert. The README's map screenshot, `docs/tour.gif` and a new launch clip are retaken from the
demo home once the build passes.
