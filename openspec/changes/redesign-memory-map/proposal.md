# Proposal

## Why

The map is the screen people will see first in the launch post, and today it reads as a static diagram:
a layout computed once, unlabeled dots, opacity that snaps on hover, and cross-project links drawn as long
arcs across empty space. It shows that memories connect, but it doesn't let you explore them or make the
set of memories feel like one connected mind.

## What Changes

- The map runs a live layout: memories settle in, can be dragged, and the rest of the graph responds,
  while redraws (a save, a live refresh, a filter change) move nodes from where they are instead of
  rebuilding the picture.
- Memory names appear on the map itself, with more of them shown as you zoom in, so you can read it
  without hovering each dot.
- Focus: hovering or opening a memory brings its neighborhood forward with eased transitions; opening one
  glides the camera to it and keeps it clear of the editor.
- Projects read as regions (a soft shape behind each cluster), and a moved or deleted project's
  memories look faded.
- Broken `[[links]]` appear as faint placeholder nodes, so a missing note shows where it would sit.
- The filter box also searches the map: matches light up, everything else recedes.
- Keyboard and screen-reader access: every memory can be reached with the keyboard and opened.
- Reduced motion swaps every movement for a cross-fade; reduced transparency drops glow and blur.
- Optional, depending on the option chosen: a 3D "brain" view for showing off, loaded only when asked for.

## Capabilities

### New Capabilities

- `memory-map`: how the map lays out, labels, highlights, searches and opens memories, and how it
  responds to input, live refreshes and accessibility settings.

### Modified Capabilities

None: no specs exist yet.

## Impact

- `web/src/map.ts` is rewritten; `web/src/styles.css` (map section) and `web/index.html` (map markup)
  change. `main.ts` keeps calling `drawMap` and `markMap`.
- A rendering library may be added to `web/package.json` (see design.md: the options under review are
  d3 on Canvas with no new dependency, sigma.js on WebGL, and a 3D view on three.js). The UI is embedded
  in the Go binary, so its bundle size adds to the download.
- `docs/screenshots/map.png` and `docs/tour.gif` are retaken for the README and the post.
- No Go, API or data model changes.
