// Drives the README clips, added only to the copy of the UI that scripts/tour.sh serves. It runs
// in the real app (so the clips have the app's own smooth rendering), draws a cursor and uses the
// map's ?tour hook (window.__mapTour) to find memories, open them and swing the camera.
//
// Each scene is one idea, about 6-10 seconds, and ends where it began so the clip loops cleanly.
// The scene comes from ?scene=; a click in the header's empty middle reloads the page and starts
// it, so the recording opens on the scene's first frame.
(() => {
  const qs = new URLSearchParams(location.search);
  if (!qs.has('tour')) return;
  const scene = qs.get('scene') ?? 'map';
  if (!sessionStorage.getItem('tour-go')) {
    addEventListener('pointerdown', (e) => {
      if (e.clientY > 64 || e.clientX < 760 || e.clientX > 960) return;
      sessionStorage.setItem('tour-go', '1'); localStorage.setItem('cca-map-view', '3d'); location.reload();
    }, true);
    return;
  }
  sessionStorage.removeItem('tour-go');

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const frame = () => new Promise(requestAnimationFrame);
  const T = () => window.__mapTour;
  const ease = (k) => 1 - Math.pow(1 - k, 3);           // strong ease-out: a hand that knows where it's going
  const smooth = (k) => 0.5 - Math.cos(k * Math.PI) / 2; // in-out, for a drag

  // A macOS-style arrow, drawn above everything.
  const cur = document.createElement('div');
  cur.style.cssText = 'position:fixed;z-index:2147483647;left:0;top:0;width:22px;height:22px;pointer-events:none;transform-origin:3px 2px;transition:transform .1s ease-out;will-change:left,top';
  cur.innerHTML = '<svg width="22" height="22" viewBox="0 0 22 22"><path d="M3 2v15.5l4.2-4 2.8 6.3 2.7-1.2-2.8-6.2H16z" fill="#000" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>';
  let at = [innerWidth * 0.78, innerHeight * 0.82];
  const place = () => { cur.style.left = at[0] + 'px'; cur.style.top = at[1] + 'px'; };
  const hoverAt = (x, y) => {
    const e = document.elementFromPoint(x, y)?.closest('.m3d, #mhost canvas');
    if (e) e.dispatchEvent(new PointerEvent('pointermove', { clientX: x, clientY: y, bubbles: true, pointerType: 'mouse', isPrimary: true }));
  };
  const move = async (x, y, ms = 650, hover = false) => {
    const [x0, y0] = at, t0 = performance.now();
    for (;;) {
      const k = Math.min(1, (performance.now() - t0) / ms), e = ease(k);
      at = [x0 + (x - x0) * e, y0 + (y - y0) * e]; place();
      if (hover) hoverAt(at[0], at[1]);
      if (k >= 1) break;
      await frame();
    }
  };
  const press = async (fn) => { cur.style.transform = 'scale(.82)'; await sleep(90); fn?.(); cur.style.transform = ''; await sleep(140); };
  const center = (e) => { const r = e.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; };
  const el = (sel) => document.querySelector(sel);
  const clickEl = async (sel, ms = 700) => { const e = el(sel); if (!e) return false; await move(...center(e), ms); await sleep(140); await press(() => e.click()); return true; };
  const toNode = async (stem, ms = 900) => {
    let p = T()?.locate(stem); if (!p) return false;
    await move(p[0] + 4, p[1] + 3, ms, true);
    p = T()?.locate(stem); if (p) await move(p[0] + 4, p[1] + 3, 180, true);
    return true;
  };
  const drag = async (dx, deg, ms) => {
    await press(); cur.style.transform = 'scale(.9)';
    T()?.turn(deg, ms);
    const [x0, y0] = at, t0 = performance.now();
    for (;;) { const k = Math.min(1, (performance.now() - t0) / ms); at = [x0 + dx * smooth(k), y0]; place(); if (k >= 1) break; await frame(); }
    cur.style.transform = '';
  };
  const waitFor = async (sel, ms = 8000) => { for (let i = 0; i < ms / 100 && !el(sel); i++) await sleep(100); return el(sel); };
  // Each hop goes somewhere new: the first link in the editor to a memory not yet visited.
  const seen = new Set();
  const hop = async () => {
    const id = [...document.querySelectorAll('#drawer .rich [data-id]')].map((e) => e.dataset.id).find((i) => !seen.has(i));
    if (!id) return false;
    seen.add(id);
    return clickEl(`#drawer .rich [data-id="${CSS.escape(id)}"]`, 750);
  };

  const SCENES = {
    // Hero: the map lifts into depth and turns; a hand swings it, a hover lights a neighbourhood.
    async map() {
      await waitFor('.m3d canvas');
      await sleep(3600);
      const m = el('#map').getBoundingClientRect();
      await move(m.left + m.width * 0.6, m.bottom - 110, 700);
      await drag(-240, -60, 1700);
      await sleep(300);
      await toNode('feedback_deploy_checklist', 900);
      await sleep(1600);
      // End on motion: Reset view glides home and the map turns again, so the loop never sits still.
      await clickEl('#mreset', 800);
      await sleep(1800);
    },
    // Open a memory from the map, then follow its links; Reset view brings the whole map back.
    async navigate() {
      await waitFor('.m3d canvas');
      await sleep(2200);
      await toNode('feedback_deploy_checklist', 900);
      await sleep(500);
      await press(() => T()?.open('feedback_deploy_checklist'));
      await sleep(1700);
      for (const e of document.querySelectorAll('#drawer .rich [data-id]')) if (e.dataset.id.endsWith('/feedback_deploy_checklist.md')) seen.add(e.dataset.id);
      if (await hop()) await sleep(1600);
      if (await hop()) await sleep(1600);
      await clickEl('#mreset', 800);
      await sleep(2000);
    },
    // Edit like notes: open a memory, type [[ and pick a memory to link; it reads as its name.
    async editor() {
      await waitFor('#v-all .row');
      await sleep(600);
      await clickEl('#v-all .row[data-id$="/feedback_deploy_checklist.md"]', 800);
      await sleep(1300);
      await clickEl('#drawer [data-m="edit"]', 700);
      await sleep(400);
      const ta = el('#f-body');
      if (ta) {
        ta.focus(); ta.setSelectionRange(ta.value.length, ta.value.length);
        for (const ch of ' Also [[') {
          ta.setRangeText(ch, ta.selectionStart, ta.selectionEnd, 'end');
          ta.dispatchEvent(new InputEvent('input', { bubbles: true, data: ch, inputType: 'insertText' }));
          await sleep(110);
        }
        await sleep(900);
        await sleep(500);
        ta.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        await sleep(900);
      }
      await clickEl('#drawer [data-m="read"]', 700);
      await sleep(1700);
      // Back to the start: close without saving (the demo stays as it was).
      await clickEl('#dx', 700);
      await sleep(300);
      if (el('#ldrop')) await clickEl('#ldrop', 500);
      await sleep(900);
    },
    // What loads here: every project's startup cost, then one project's sources and files.
    async loads() {
      await waitFor('.wrow');
      await sleep(1000);
      if (!(await clickEl('.wrow[data-p$="/storefront"]', 800))) await clickEl('.wrow', 800);
      await sleep(1300);
      await clickEl('.bl[aria-controls]', 700);
      await sleep(1500);
      await clickEl('.bl[aria-controls][aria-expanded="true"]', 500);
      await sleep(500);
      await clickEl('#navback', 700);
      await sleep(1100);
    },
    // Review: a card with the evidence and the exact change, accepted in one click.
    async review() {
      await waitFor('#v-review .card');
      await sleep(1000);
      if (el('#chips [data-c="Links"]')) { await clickEl('#chips [data-c="Links"]', 800); await sleep(1100); }
      const card = el('#v-review .card');
      if (card) { const r = card.getBoundingClientRect(); await move(r.left + r.width * 0.45, r.top + 90, 800); await sleep(1100); }
      await clickEl('#v-review .card [data-a="primary"]', 700);
      await sleep(1800);
      await clickEl('#chips [data-c=""]', 800);
      await sleep(900);
    },
  };

  addEventListener('load', async () => {
    document.body.append(cur); place();
    try { await SCENES[scene](); } finally { document.title = 'tour-done'; }
  });
})();
