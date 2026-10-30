// node --test test/  (plain Node, no browser): stitching made-up screenshots of a made-up page.
import test from 'node:test';
import assert from 'node:assert/strict';
import { rowFeatures, fixedBand, findOverlap, planStitch, splitRows } from '../web/stitch.js';

// A seeded random number generator, so a failure can be reproduced.
function rng(seed) {
  return () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 2 ** 32; };
}

// page: a dark background with lines of "words" (bright blocks with gaps), paragraph breaks.
function page(w, h, seed) {
  const r = rng(seed), g = new Uint8Array(w * h).fill(18);
  for (let y = 40; y + 16 < h; y += 30) {
    if (r() < 0.12) { y += 24; continue; } // paragraph gap
    let x = 16;
    while (x < w - 40) {
      const len = 10 + Math.floor(r() * 60);
      for (let yy = y; yy < y + 14; yy++) for (let xx = x; xx < Math.min(w - 16, x + len); xx++) {
        if ((xx * 7 + yy * 3 + Math.floor(r() * 3)) % 5) g[yy * w + xx] = 225;
      }
      x += len + 8 + Math.floor(r() * 6);
    }
  }
  return g;
}

// shot: a screenshot of the page scrolled to `top`: status bar with a clock that changes, a title
// bar, the page, and a typing box at the bottom. Returns RGBA with a little noise.
function shot(pg, w, top, view, n, seed) {
  const HDR = 80, FTR = 70, h = HDR + view + FTR, r = rng(seed), out = new Uint8ClampedArray(w * h * 4);
  const grey = (y, x) => {
    if (y < 30) return x >= 20 && x < 60 && ((x < 48 ? x : x + n * 5) % 9 < 4) && y > 10 && y < 22 ? 240 : 30; // clock: last digit changes
    if (y < HDR) return y > 45 && y < 62 && x > 120 && x < 280 && (x % 6 < 4) ? 220 : 26;          // title
    if (y < HDR + view) return pg[(top + y - HDR) * w + x];
    return y > h - 50 && y < h - 20 && x > 20 && x < w - 20 ? 60 : 24;                              // typing box
  };
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
    const v = Math.max(0, Math.min(255, grey(y, x) + Math.round((r() - 0.5) * 6)));
    const p = (y * w + x) * 4;
    out[p] = out[p + 1] = out[p + 2] = v; out[p + 3] = 255;
  }
  return { width: w, height: h, data: out };
}

const W = 400, VIEW = 760;
const pg = page(W, 3200, 7);
const tops = [0, 520, 1110];
const shots = tops.map((t, i) => shot(pg, W, t, VIEW, i, 100 + i));
const feats = shots.map(rowFeatures);

test('the status bar and title bar count as one fixed band, despite the clock', () => {
  const hdr = fixedBand(feats[0], feats[1], false);
  assert.ok(hdr >= 80 && hdr <= 125, `top band ${hdr}`); // may run on into blank rows below it (harmless)
  const ftr = fixedBand(feats[0], feats[1], true);
  assert.ok(ftr >= 70 && ftr <= 110, `bottom band ${ftr}`);
});

test('finds where each screenshot continues the last', () => {
  const m = findOverlap(feats[0], feats[1], 80, 70);
  assert.ok(m.ok, `score ${m.score}`);
  assert.equal(m.shift, 520);
  const m2 = findOverlap(feats[1], feats[2], 80, 70);
  assert.ok(m2.ok);
  assert.equal(m2.shift, 590);
});

test('the stitched image is the page with one set of bars', () => {
  const plan = planStitch(feats);
  assert.ok(plan.joins.every((j) => j.ok), JSON.stringify(plan.joins));
  assert.equal(plan.height, 80 + tops[2] + VIEW + 70);
  // rebuild it and compare with the page itself
  let r = 0, diff = 0, rows = 0;
  for (const s of plan.segments) {
    for (let y = s.y0; y < s.y1; y++, r++) {
      if (r < 80 || r >= plan.height - 70) continue;
      for (let x = 0; x < W; x += 3) diff += Math.abs(shots[s.i].data[(y * W + x) * 4] - pg[(r - 80) * W + x]);
      rows++;
    }
  }
  assert.ok(diff / (rows * Math.ceil(W / 3)) < 3, `mean difference ${diff / (rows * Math.ceil(W / 3))}`);
});

test('screenshots that don\'t overlap are put end to end, and an overlap can be set by hand', () => {
  const far = [feats[0], rowFeatures(shot(pg, W, 2300, VIEW, 3, 9))];
  const plan = planStitch(far);
  assert.equal(plan.joins[0].ok, false);
  assert.equal(plan.height, 2 * (80 + VIEW + 70) - plan.joins[0].hdr - plan.joins[0].ftr);
  const hand = planStitch(feats.slice(0, 2), { overrides: { 0: 100 } });
  assert.ok(hand.joins[0].manual && hand.joins[0].overlap === 100);
});

test('the top and bottom bars can be left off altogether', () => {
  const full = planStitch(feats);
  const bare = planStitch(feats, { keepTop: false, keepBottom: false });
  assert.ok(bare.joins.every((j) => j.ok));
  assert.equal(bare.height, full.height - bare.joins[0].hdr - bare.joins[1].ftr);
  assert.equal(bare.segments[0].y0, bare.joins[0].hdr);
});

test('bars marked by hand are used as given', () => {
  const plan = planStitch(feats, { bars: { top: 80, bottom: 70 } });
  assert.ok(plan.joins.every((j) => j.ok && j.hdr === 80 && j.ftr === 70), JSON.stringify(plan.joins));
  assert.equal(plan.height, 80 + tops[2] + VIEW + 70);
  const silly = planStitch(feats, { bars: { top: 5000, bottom: -3 } }); // kept to something sensible
  assert.ok(silly.joins.every((j) => j.hdr <= 0.45 * 910 && j.ftr === 0));
});

test('splitting a tall image cuts between lines', () => {
  const tex = (r) => ((r % 30) < 14 ? 50 : 1); // text rows every 30, gaps between
  const cuts = splitRows(1000, 300, tex);
  assert.equal(cuts[0], 0);
  assert.equal(cuts[cuts.length - 1], 1000);
  for (let i = 1; i < cuts.length; i++) assert.ok(cuts[i] - cuts[i - 1] <= 300);
  for (const c of cuts.slice(1, -1)) assert.equal(tex(c), 1);
});
