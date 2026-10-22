# Tasks

## 1. Dependencies and build

- [x] 1.1 Add `three@0.186.1` and `3d-force-graph@1.80.1` with exact versions; `bun audit` clean; verify with `bun pm ls` that one copy of three is installed
- [x] 1.2 Turn on code splitting in `web/build.ts` and fail the build unless a separate 3D chunk is emitted; verify with `bun run build` output
- [x] 1.3 Extend `scripts/notices.sh` to every npm package in the production tree; verify the output names three, 3d-force-graph and its dependencies

## 2. Shared logic (`maplogic.ts`), test first

- [x] 2.1 Label placement: greedy, side preference, keeps side, avoids names, capsules, controls and nearer dots, grid-based; `bun test` covers overlaps, side choice and the obstacle rules
- [x] 2.2 Spatial keyboard navigation: nearest memory in an arrow's direction; tests for each direction and for no candidate
- [x] 2.3 Stable 2D layout cached by ids and links, seeded from previous positions; tests that a filter does not move anything and an unchanged model reuses the cache

## 3. 2D view (`map2d.ts`)

- [x] 3.1 Canvas view: regions, links, twins, ghost nodes for broken links, glow, names, capsules; draws only when something changed (simulation marked settled); verify idle main thread under 1% in Chromium
- [x] 3.2 Hover focus, drag, fly to the open memory clear of the drawer, keyboard navigation and announcements; verify by hand and with screenshots at 1280×800 and 390×844 in both themes

## 4. 3D view (`map3d.ts`)

- [x] 4.1 Native 3d-force-graph view seeded from the 2D layout with a z spread; settles on screen within a bounded cooldown; full-resolution bloom; pixel ratio capped at 2; node drag off; `nodeLabel`/`linkLabel` empty; verify framed at first frame and no page errors
- [x] 4.2 Names layer and capsules with the shared placement; hover and open focus; fly-in; signals on every link at rest (between projects only past ~250 links), a stream on the focused memory, speed scaled by measured frame time
- [x] 4.3 Turn: pauses on hover and while open, resumes after idle, rests a minute after the last input, Reset view restarts; `pauseAnimation()` when still, hidden or not the current view; verify idle under 1% and 500 memories at 50 fps or more with CPU slowed 4×
- [x] 4.4 Keyboard navigation and announcements on projected positions

## 5. Shared chrome (`map.ts`, `index.html`, `styles.css`)

- [x] 5.1 Toolbar (2D | 3D, zoom, Reset view, Key), 44 px targets, tooltips (delay then instant, keyboard focus), key popover; disabled 3D with reason when WebGL2 is unusable
- [x] 5.2 Default view: remembered choice, else 2D under Reduce Motion or without usable WebGL2, else 3D
- [x] 5.3 "No memories match" state with a clear-filter action; remove the old SVG map, legend and its CSS; `bunx tsc --noEmit` and `bun test` pass

## 6. Integration checks

- [ ] 6.1 `make check` passes (Go tests, tsc, bun test, vet, govulncheck), and the loopback-only network test passes with the new chunk
- [x] 6.2 Measure the targets on the demo home and a 500-memory home: idle under 1%, 50 fps or more at 4× CPU slowdown, and record before/after numbers
- [x] 6.3 Screenshots of both views, both themes, desktop and phone, read back; `web-design-guidelines` review of the changed UI
- [ ] 6.4 Retake `docs/screenshots/map.png`, `docs/tour.gif` and a 12-second launch clip in 3D from the demo home; update README and CHANGELOG

## Workflow follow-up

- Archive the change with `openspec archive redesign-memory-map` after it merges.
