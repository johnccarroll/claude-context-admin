// The README tour's driver, added only to the copy of the UI that scripts/tour.sh serves. It runs in
// the real app (so the recording has the app's own smooth rendering), draws a cursor and uses the
// map's ?tour hook (window.__mapTour) to find memories, open them and swing the camera.
//
// A click in the header's empty middle reloads the page and starts the tour, so the recording opens
// on the map's lift.
(() => {
  if (!new URLSearchParams(location.search).has('tour')) return;
  if (!sessionStorage.getItem('tour-go')) {
    // The start signal: a click on the empty stretch of the header between the title and "+ New memory".
    addEventListener('pointerdown', (e) => {
      if (e.clientY > 64 || e.clientX < 760 || e.clientX > 960) return;
      sessionStorage.setItem('tour-go', '1'); localStorage.setItem('cca-map-view', '3d'); location.reload();
    }, true);
    return;
  }
  sessionStorage.removeItem('tour-go');

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const T = () => window.__mapTour;
  const ease = (k) => 1 - Math.pow(1 - k, 3);
  const smooth = (k) => 0.5 - Math.cos(k * Math.PI) / 2;

  // A macOS-style arrow, drawn above everything.
  const cur = document.createElement('div');
  cur.style.cssText = 'position:fixed;z-index:2147483647;left:0;top:0;width:22px;height:22px;pointer-events:none;transform-origin:3px 2px;transition:transform .1s ease-out;will-change:left,top';
  cur.innerHTML = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2v15.5l4.2-4 2.8 6.3 2.7-1.2-2.8-6.2H16z" fill="#000" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>';
  let at = [innerWidth - 220, innerHeight - 120];
  const place = () => { cur.style.left = at[0] + 'px'; cur.style.top = at[1] + 'px'; };
  // Hovering the map: the 3D library and the map both read pointer moves on its container.
  const hoverAt = (x, y) => {
    const el = document.elementFromPoint(x, y)?.closest('.m3d, #mhost canvas');
    if (el) el.dispatchEvent(new PointerEvent('pointermove', { clientX: x, clientY: y, bubbles: true, pointerType: 'mouse', isPrimary: true }));
  };
  const move = async (x, y, ms = 650, hover = false) => {
    const [x0, y0] = at, t0 = performance.now();
    for (;;) {
      const k = Math.min(1, (performance.now() - t0) / ms), e = ease(k);
      at = [x0 + (x - x0) * e, y0 + (y - y0) * e]; place();
      if (hover) hoverAt(at[0], at[1]);
      if (k >= 1) break;
      await new Promise(requestAnimationFrame);
    }
  };
  const press = async (fn) => { cur.style.transform = 'scale(.82)'; await sleep(90); fn?.(); cur.style.transform = ''; await sleep(120); };
  const center = (el) => { const r = el.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; };
  const clickEl = async (sel, ms = 700) => { const el = document.querySelector(sel); if (!el) return false; await move(...center(el), ms); await sleep(160); await press(() => el.click()); return true; };
  const toNode = async (stem, ms = 900) => {
    // The map may still drift; aim again near the end of the move.
    let p = T()?.locate(stem); if (!p) return false;
    await move(p[0] + 4, p[1] + 3, ms, true);
    p = T()?.locate(stem); if (p) await move(p[0] + 4, p[1] + 3, 200, true);
    return true;
  };
  // A grab-and-drag that swings the camera, the cursor moving with it.
  const drag = async (dx, deg, ms) => {
    await press();
    cur.style.transform = 'scale(.9)';
    T()?.turn(deg, ms);
    const [x0, y0] = at, t0 = performance.now();
    for (;;) { const k = Math.min(1, (performance.now() - t0) / ms); at = [x0 + dx * smooth(k), y0]; place(); if (k >= 1) break; await new Promise(requestAnimationFrame); }
    cur.style.transform = '';
  };

  addEventListener('load', async () => {
    document.body.append(cur); place();
    for (let i = 0; i < 100 && !document.querySelector('.m3d canvas'); i++) await sleep(100);
    const map = document.getElementById('map').getBoundingClientRect();

    // 1. Wide: the map lifts into depth, reframes and starts to turn.
    await sleep(4200);
    // 2. Grab empty space low on the map and swing it around, then the other way a little.
    await move(map.left + map.width * 0.62, map.bottom - 90, 800);
    await drag(-260, -75, 1900);
    await sleep(500);
    await drag(140, 35, 1100);
    await sleep(500);
    // 3. Zoom in.
    await clickEl('#mzin', 900); await sleep(380);
    await press(() => document.getElementById('mzin').click()); await sleep(700);
    // 4. Hover a busy memory: its neighbourhood lights up and signals stream along its links.
    await toNode('feedback_deploy_checklist', 1000);
    await sleep(1500);
    // 5. Open it: the camera flies in and the editor slides out.
    await press(() => T()?.open('feedback_deploy_checklist'));
    await sleep(2000);
    // 6. Navigate: follow links in the editor, and the map flies to each memory.
    // Each hop goes somewhere new: the first link in the editor to a memory not yet visited.
    const seen = new Set();
    const hop = async () => {
      const id = [...document.querySelectorAll('#drawer .rich [data-id]')].map((e) => e.dataset.id).find((i) => !seen.has(i) && !i.endsWith('/feedback_deploy_checklist.md'));
      if (!id) return false;
      seen.add(id);
      return clickEl(`#drawer .rich [data-id="${CSS.escape(id)}"]`, 800);
    };
    if (await hop()) await sleep(2000);
    if (await hop()) await sleep(2000);
    // 7. Reset view: the whole map again, turning.
    await clickEl('#mreset', 900);
    await sleep(3000);
    // (the launch clip ends here)
    // 8. For the README: the same map flat, then what loads in a project, then Review.
    await clickEl('#mdim [data-v="2d"]', 800); await sleep(2200);
    await clickEl('button[data-v="what"]', 900);
    await sleep(2400);
    await clickEl('button[data-v="review"]', 700);
    await sleep(2600);
    localStorage.setItem('cca-map-view', '3d');
  });
})();
