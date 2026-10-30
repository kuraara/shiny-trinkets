// Screenshot Stitch's matching. It works on plain pixel data ({width, height, data: RGBA}), so it
// runs (and is tested) without a browser. Every screenshot is first scaled to one width; then each
// row is summarised as the brightness and the edge strength in 64 strips across it. That summary
// shrugs off JPEG noise and slight rescaling but still tells one line of text from the next.

export const BINS = 64;
const D = BINS * 2;
const ACCEPT = 14;      // a join is trusted below this (average grey-level difference per strip)
const CAP = 45;         // one badly matching row (a floating button, a shadow) can't sink a join

// rowFeatures summarises every row of an image (already scaled to the common width).
export function rowFeatures(img) {
  const { width: w, height: h, data } = img;
  const f = new Float32Array(h * D);
  const tex = new Float32Array(h);
  const g = new Float32Array(w);
  const xs = new Int32Array(BINS + 1);
  for (let b = 0; b <= BINS; b++) xs[b] = Math.floor((b * w) / BINS);
  for (let y = 0; y < h; y++) {
    for (let x = 0, p = y * w * 4; x < w; x++, p += 4) g[x] = (data[p] * 54 + data[p + 1] * 183 + data[p + 2] * 19) / 256;
    let sum = 0, sum2 = 0, esum = 0;
    for (let b = 0; b < BINS; b++) {
      const a = xs[b], z = xs[b + 1];
      let m = 0, e = 0;
      for (let x = a; x < z; x++) m += g[x];
      for (let x = a; x < z - 1; x++) e += Math.abs(g[x + 1] - g[x]);
      m /= Math.max(1, z - a);
      e = (e / Math.max(1, z - a - 1)) * 2; // edges count double: text is thin strokes
      f[y * D + b] = m;
      f[y * D + BINS + b] = e;
      sum += m; sum2 += m * m; esum += e;
    }
    const mean = sum / BINS;
    tex[y] = Math.sqrt(Math.max(0, sum2 / BINS - mean * mean)) + esum / BINS;
  }
  return { width: w, height: h, f, tex };
}

function dist(A, ya, B, yb) {
  const a = ya * D, b = yb * D;
  let s = 0;
  for (let k = 0; k < D; k++) s += Math.abs(A.f[a + k] - B.f[b + k]);
  return s / D;
}

// Two rows are "the same" if most strips match: a clock (whose digits also shift sideways when one
// changes), a spinner or a battery level may differ between screenshots. Rows of text that have
// scrolled differ in far more strips (25+ of 64 in real screenshots).
function sameRow(A, ya, B, yb) {
  if (ya < 0 || ya >= A.height || yb < 0 || yb >= B.height) return false;
  const a = ya * D, b = yb * D;
  let off = 0;
  for (let k = 0; k < BINS; k++) {
    // loose per strip (rescaling softens text edges), strict on how many strips may differ
    if (Math.abs(A.f[a + k] - B.f[b + k]) > 16 || Math.abs(A.f[a + BINS + k] - B.f[b + BINS + k]) > 40) {
      if (++off > 12) return false;
    }
  }
  return true;
}

// fixedBand: how many rows at the top (or bottom) of two screenshots stay put: the status bar, a
// title bar, a tab bar or a typing box. A row may be up to two pixels out: screenshots saved at a
// different size and scaled back don't land exactly on the same rows.
export function fixedBand(A, B, fromBottom = false) {
  const limit = Math.floor(Math.min(A.height, B.height) * (fromBottom ? 0.3 : 0.35));
  let k = 0;
  for (; k < limit; k++) {
    const ya = fromBottom ? A.height - 1 - k : k, yb = fromBottom ? B.height - 1 - k : k;
    if (![0, -1, 1, -2, 2].some((d) => sameRow(A, ya + d, B, yb))) break;
  }
  return k;
}

// score compares B rows [y0, y1) with A rows shifted by s (B row y ↔ A row y + s). Rows with text
// count most; rows that are blank in both count a little; a stretch with no text at all can't match.
function score(A, B, s, y0, y1, step) {
  let num = 0, den = 0, mass = 0;
  for (let y = y0; y < y1; y += step) {
    const ta = A.tex[y + s], tb = B.tex[y];
    const w = ta + tb + 1;
    num += w * Math.min(CAP, dist(A, y + s, B, y));
    den += w;
    mass += Math.min(ta, tb) * step;
  }
  if (mass < 400) return Infinity; // about one line of text at the least
  return num / den + 30 / Math.sqrt(mass);
}

// texturedBand: the part of a fixed band that actually has something in it. Blank rows that happen
// to sit at the same height in both screenshots can be scrolled content, and with a small overlap
// every one of them counts.
function texturedBand(F, band, fromBottom) {
  for (let k = band; k > 0; k--) if (F.tex[fromBottom ? F.height - k : k - 1] > 8) return k;
  return 0;
}

// findOverlap looks for where B continues A: the shift s for which B's rows (below the top bar)
// repeat A's rows s further down. Coarse pass over every shift, then the best few are checked
// row by row over the whole overlap.
export function findOverlap(A, B, hdr = 0, ftr = 0) {
  const aEnd = A.height - texturedBand(A, ftr, true), bEnd = B.height - ftr;
  const LA = aEnd - hdr, LB = bEnd - hdr;
  if (LA < 40 || LB < 40) return { ok: false, score: Infinity };
  // a few rows of margin next to the bars (anti-aliased edges, a shadow). Kept small: people often
  // scroll nearly a whole screen, leaving just one line in common.
  const skip = Math.min(Math.round(B.width * 0.005), Math.floor(LB / 4));
  const tail = Math.min(Math.round(A.width * 0.005), Math.floor(LA / 6));
  const p0 = hdr + skip;
  const P = Math.min(bEnd - p0, Math.round(B.width * 0.3));
  const minRows = 10; // what counts is the amount of text (score's mass), not rows
  const coarse = [];
  for (let s = 0; p0 + s + minRows <= aEnd - tail; s++) {
    const n = Math.min(P, aEnd - tail - (p0 + s));
    coarse.push([score(A, B, s, p0, p0 + n, 3), s]);
  }
  coarse.sort((x, y) => x[0] - y[0]);
  const picks = [];
  for (const [sc, s] of coarse) {
    if (!Number.isFinite(sc) || picks.length >= 6) break;
    if (picks.every((p) => Math.abs(p - s) > 4)) picks.push(s);
  }
  let best = { ok: false, score: Infinity };
  for (const s0 of picks) {
    for (let s = Math.max(0, s0 - 3); s <= s0 + 3; s++) {
      const y1 = Math.min(bEnd, aEnd - tail - s);
      if (y1 - p0 < minRows) continue;
      const sc = score(A, B, s, p0, y1, 1);
      if (sc < best.score) best = { ok: false, score: sc, shift: s };
    }
  }
  best.ok = best.score <= ACCEPT;
  return best;
}

// cut picks where to switch from A to B inside their overlap (B rows [y0, y1)): a blank gap
// between lines near the middle, so the seam doesn't show.
function cut(A, B, s, y0, y1) {
  const lo = Math.floor(y0 + (y1 - y0) * 0.25), hi = Math.max(lo + 1, Math.ceil(y0 + (y1 - y0) * 0.75));
  const mid = (y0 + y1) / 2;
  let best = lo, bc = Infinity;
  for (let y = lo; y < hi; y++) {
    const c = A.tex[y + s] + B.tex[y] + dist(A, y + s, B, y) + Math.abs(y - mid) * 0.02;
    if (c < bc) { bc = c; best = y; }
  }
  return best;
}

// planStitch works out which rows of each screenshot make the long image. The bars that stay put
// (status bar, title, typing box) are left out of the matching and appear once: keepTop keeps the
// first screenshot's top bar, keepBottom the last one's bottom bar. overrides[i] sets the overlap
// (rows) of join i (screenshot i and i + 1) by hand. Rows are in the common width's scale.
export function planStitch(feats, { keepTop = true, keepBottom = true, overrides = {} } = {}) {
  const n = feats.length;
  const starts = new Array(n).fill(0), ends = feats.map((F) => F.height);
  const joins = [];
  for (let i = 0; i + 1 < n; i++) {
    const A = feats[i], B = feats[i + 1];
    const hdr = fixedBand(A, B, false);
    const ftr = fixedBand(A, B, true);
    const LA = A.height - ftr - hdr;
    let m;
    if (overrides[i] != null) {
      const ov = Math.max(0, Math.min(LA - 1, Math.round(overrides[i])));
      m = { ok: ov > 0, manual: true, score: null, shift: LA - ov };
    } else {
      m = findOverlap(A, B, hdr, ftr);
    }
    const j = { hdr, ftr, ok: m.ok, manual: !!m.manual, score: m.score, overlap: 0 };
    if (m.ok) {
      const s = m.shift;
      const y0 = Math.max(hdr, starts[i] - s + 1); // never reach above where A itself starts
      const y1 = Math.min(B.height - ftr, A.height - ftr - s);
      if (y1 - y0 >= 2) {
        const y = cut(A, B, s, y0, y1);
        ends[i] = y + s;
        starts[i + 1] = y;
        j.overlap = LA - s;
        j.seam = y;
      } else {
        j.ok = false;
      }
    }
    if (!j.ok) {
      ends[i] = A.height - ftr;
      starts[i + 1] = hdr;
    }
    joins.push(j);
  }
  if (joins.length && !keepTop) starts[0] = Math.max(starts[0], joins[0].hdr);
  if (joins.length && !keepBottom) ends[n - 1] = Math.min(ends[n - 1], feats[n - 1].height - joins[n - 2].ftr);
  const segments = feats.map((F, i) => ({ i, y0: starts[i], y1: Math.max(starts[i], ends[i]) }));
  return { joins, segments, height: segments.reduce((t, g) => t + g.y1 - g.y0, 0) };
}

// splitRows chooses where to cut a very tall result into parts no taller than maxH, at the
// quietest row near each limit (between lines of text). texAt(r) is the texture of output row r.
export function splitRows(height, maxH, texAt) {
  const cuts = [0];
  while (height - cuts[cuts.length - 1] > maxH) {
    const start = cuts[cuts.length - 1], hi = start + maxH, lo = start + Math.floor(maxH * 0.85);
    let best = hi, bt = Infinity;
    for (let r = hi; r > lo; r--) {
      const t = texAt(r);
      if (t < bt) { bt = t; best = r; }
    }
    cuts.push(best);
  }
  cuts.push(height);
  return cuts;
}
