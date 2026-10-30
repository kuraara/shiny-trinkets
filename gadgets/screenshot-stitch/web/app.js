// Screenshot Stitch: overlapping screenshots in, one long image out, lined up on the text.
// Everything happens in the browser; the matching itself is in stitch.js.
import { rowFeatures, planStitch, splitRows } from './stitch.js';

const $ = (s) => document.querySelector(s);
const IOS = /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
// The biggest image the browser can draw: iPhones and iPads stop at 16.7 million pixels.
const MAX_AREA = IOS ? 16_777_216 : 100_000_000;
const MAX_SIDE = IOS ? 16_384 : 32_767;
const PREVIEW_MAX_H = 12_000;

const items = []; // {id, name, img, w, h, url, feat, featW, li, joinEl}
let nextId = 1;
let plan = null;  // from planStitch, plus W0 (the common width) and feats
const overrides = new Map(); // "idA>idB" -> overlap in rows, set by hand
let bars = null;   // {top, bottom} marked by hand, in the common width's rows (kept when switching back)
const marking = () => document.querySelector('input[name=bars]:checked').value === 'mark';
const pairKey = (a, b) => `${a.id}>${b.id}`;

function note(msg) { $('#note').textContent = msg; $('#note').hidden = !msg; }

// ---------------------------------------------------------------- adding screenshots

async function addFiles(files) {
  let added = 0;
  for (const file of files) {
    if (!file.type.startsWith('image/') && !/\.(jpe?g|png|gif|webp|avif|heic|bmp)$/i.test(file.name)) continue;
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.src = url;
    try {
      await img.decode();
    } catch {
      URL.revokeObjectURL(url);
      note(`Couldn’t read ${file.name || 'that image'}. HEIC pictures only open in Safari; try PNG or JPEG.`);
      continue;
    }
    items.push({ id: nextId++, name: file.name || 'Pasted screenshot', img, w: img.naturalWidth, h: img.naturalHeight, url });
    added++;
  }
  if (added) { renderList(); replan(); }
}

$('#pick').addEventListener('change', (e) => { addFiles([...e.target.files]); e.target.value = ''; });
document.addEventListener('paste', (e) => {
  const files = [...(e.clipboardData?.items || [])].filter((i) => i.kind === 'file' && i.type.startsWith('image/')).map((i) => i.getAsFile());
  if (files.length) { e.preventDefault(); addFiles(files); }
});
if (window.isSecureContext && navigator.clipboard?.read && !IOS) {
  $('#pasteBtn').hidden = false;
  $('#pasteBtn').addEventListener('click', async () => {
    try {
      const out = [];
      for (const it of await navigator.clipboard.read()) {
        const type = it.types.find((t) => t.startsWith('image/'));
        if (type) out.push(new File([await it.getType(type)], 'Pasted screenshot', { type }));
      }
      if (out.length) addFiles(out); else note('There’s no picture on the clipboard.');
    } catch { note('The browser didn’t allow reading the clipboard. Try Ctrl+V / ⌘V instead.'); }
  });
}
const drop = $('#drop');
['dragenter', 'dragover'].forEach((t) => document.addEventListener(t, (e) => {
  if ([...(e.dataTransfer?.types || [])].includes('Files')) { e.preventDefault(); drop.classList.add('over'); }
}));
['dragleave', 'drop'].forEach((t) => document.addEventListener(t, (e) => {
  if (t === 'dragleave' && e.relatedTarget) return;
  drop.classList.remove('over');
}));
document.addEventListener('drop', (e) => {
  if (e.dataTransfer?.files?.length) { e.preventDefault(); addFiles([...e.dataTransfer.files]); }
});

// ---------------------------------------------------------------- the list

let dragId = null;
function renderList() {
  const list = $('#list');
  list.replaceChildren();
  items.forEach((it, i) => {
    const li = document.createElement('li');
    li.draggable = true;
    const num = Object.assign(document.createElement('span'), { className: 'num', textContent: i + 1 });
    const thumb = Object.assign(document.createElement('img'), { className: 'thumb', src: it.url, alt: '' });
    const meta = document.createElement('div');
    meta.className = 'meta';
    meta.append(Object.assign(document.createElement('b'), { textContent: it.name }),
      Object.assign(document.createElement('small'), { textContent: `${it.w} × ${it.h}` }));
    const tools = document.createElement('div');
    tools.className = 'tools';
    const btn = (label, title, fn, disabled) => {
      const b = Object.assign(document.createElement('button'), { type: 'button', textContent: label, title, disabled });
      b.setAttribute('aria-label', `${title} (screenshot ${i + 1})`);
      b.addEventListener('click', fn);
      return b;
    };
    tools.append(btn('↑', 'Move up', () => move(i, i - 1), i === 0),
      btn('↓', 'Move down', () => move(i, i + 1), i === items.length - 1),
      btn('✕', 'Remove', () => remove(i), false));
    li.append(num, thumb, meta, tools);
    it.joinEl = null;
    if (i > 0) { // how it joins the one before
      const j = document.createElement('div');
      j.className = 'join';
      li.append(j);
      it.joinEl = j;
    }
    li.addEventListener('dragstart', (e) => { dragId = it.id; e.dataTransfer.effectAllowed = 'move'; li.classList.add('dragging'); });
    li.addEventListener('dragend', () => { dragId = null; li.classList.remove('dragging'); });
    li.addEventListener('dragover', (e) => { if (dragId != null) { e.preventDefault(); e.stopPropagation(); } });
    li.addEventListener('drop', (e) => {
      if (dragId == null) return;
      e.preventDefault(); e.stopPropagation();
      move(items.findIndex((x) => x.id === dragId), i);
    });
    list.append(li);
  });
  $('#listHint').hidden = items.length < 2;
}

function move(from, to) {
  if (to < 0 || to >= items.length || from === to) return;
  items.splice(to, 0, ...items.splice(from, 1));
  renderList();
  replan();
}

function remove(i) {
  const [it] = items.splice(i, 1);
  URL.revokeObjectURL(it.url);
  renderList();
  replan();
}

// One line per join: lined up (and how much they share), set by hand, or not found; plus a way
// to set the overlap by hand.
function showJoins() {
  items.forEach((it, i) => {
    const el = it.joinEl;
    if (!el || !plan) return;
    const j = plan.joins[i - 1];
    const k = pairKey(items[i - 1], it);
    el.replaceChildren();
    const msg = document.createElement('span');
    if (j.manual) { msg.className = 'hand'; msg.textContent = `Overlap with ${i} set by hand: ${j.overlap} px`; }
    else if (j.ok) { msg.className = 'ok'; msg.textContent = `✓ Lines up with ${i} (${j.overlap} px shared)`; }
    else { msg.className = 'bad'; msg.textContent = `No shared lines with ${i} found: put straight after it. Check the order, or set the overlap.`; }
    el.append(msg);
    const adj = document.createElement('span');
    adj.className = 'adjust';
    const input = Object.assign(document.createElement('input'), { type: 'number', min: 0, step: 1, value: j.overlap, inputMode: 'numeric' });
    input.setAttribute('aria-label', `Overlap between screenshots ${i} and ${i + 1}, in pixels`);
    const set = (v) => { overrides.set(k, Math.max(0, Math.round(v))); replan(); };
    const b = (label, title, fn) => {
      const x = Object.assign(document.createElement('button'), { type: 'button', textContent: label, title });
      x.setAttribute('aria-label', title);
      x.addEventListener('click', fn);
      return x;
    };
    adj.append(b('−', 'One pixel less overlap', () => set(j.overlap - 1)), input, b('+', 'One pixel more overlap', () => set(j.overlap + 1)));
    input.addEventListener('change', () => set(parseFloat(input.value) || 0));
    el.append(adj);
    if (j.manual) el.append(b('Auto', 'Find the overlap again', () => { overrides.delete(k); replan(); }));
    else adj.hidden = j.ok && !el.dataset.open;
    if (!j.manual && j.ok) {
      const open = Object.assign(document.createElement('button'), { type: 'button', className: 'linkish', textContent: el.dataset.open ? 'Done' : 'Adjust' });
      open.addEventListener('click', () => { if (el.dataset.open) delete el.dataset.open; else el.dataset.open = '1'; showJoins(); });
      el.append(open);
    }
  });
}

// ---------------------------------------------------------------- matching

const work = document.createElement('canvas');
function featuresFor(it, W) {
  if (it.feat && it.featW === W) return it.feat;
  const h = Math.max(1, Math.round((it.h * W) / it.w));
  work.width = W; work.height = h;
  const ctx = work.getContext('2d', { willReadFrequently: true });
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(it.img, 0, 0, W, h);
  it.feat = rowFeatures(ctx.getImageData(0, 0, W, h));
  it.featW = W;
  work.width = work.height = 1; // give the memory back
  return it.feat;
}

let gen = 0;
async function replan() {
  const my = ++gen;
  if (!items.length) { plan = null; draw(); return; }
  const W0 = items[0].w; // everything is scaled to the first screenshot's width
  if (items.some((it) => it.featW !== W0)) $('#dims').textContent = 'Lining them up…';
  const feats = [];
  for (const it of items) {
    if (it.featW !== W0) await new Promise((r) => setTimeout(r, 0)); // let the page breathe
    if (my !== gen) return;
    feats.push(featuresFor(it, W0));
  }
  const ov = {};
  for (let i = 1; i < items.length; i++) {
    const k = pairKey(items[i - 1], items[i]);
    if (overrides.has(k)) ov[i - 1] = overrides.get(k);
  }
  plan = { ...planStitch(feats, { keepTop: $('#keepTop').checked, keepBottom: $('#keepBottom').checked, overrides: ov,
    bars: marking() && bars ? bars : null }), W0, feats };
  showJoins();
  showBars();
  draw();
}

// ---------------------------------------------------------------- the bars, found or marked

function showBars() {
  const j = plan && plan.joins[0];
  const found = $('#barsFound');
  found.hidden = marking() || !j;
  if (j && !marking()) found.textContent = `Found a ${j.hdr} px top bar and a ${j.ftr} px bottom bar. If that’s wrong (some of the bars show through, or the text doubles up at a join), mark them.`;
  $('#marker').hidden = !marking() || !items.length;
  if (marking() && items.length) {
    const sel = $('#markShot'), keep = sel.value;
    sel.replaceChildren(...items.map((it, i) => Object.assign(document.createElement('option'), { value: i, textContent: `Screenshot ${i + 1}` })));
    sel.value = keep && keep < items.length ? keep : '0';
    $('#markShotRow').hidden = items.length < 2;
    $('#topPx').value = bars.top;
    $('#bottomPx').value = bars.bottom;
    drawMarker();
  }
}

// The chosen screenshot with the bars shaded and a line (and handle) at each edge.
const mc = $('#markCanvas');
function shotHeight(it) { return Math.round((it.h * plan.W0) / it.w); }
function drawMarker() {
  if (!plan || !marking()) return;
  const it = items[parseInt($('#markShot').value, 10) || 0];
  if (!it) return;
  const dpr = Math.min(2, devicePixelRatio || 1);
  const cssW = Math.max(160, mc.parentElement.clientWidth);
  const Hn = shotHeight(it), k = (cssW * dpr) / plan.W0;
  mc.width = Math.round(cssW * dpr);
  mc.height = Math.round(Hn * k);
  const ctx = mc.getContext('2d');
  ctx.drawImage(it.img, 0, 0, mc.width, mc.height);
  const top = bars.top * k, bot = mc.height - bars.bottom * k;
  ctx.fillStyle = 'rgba(255, 47, 208, .28)';
  ctx.fillRect(0, 0, mc.width, top);
  ctx.fillRect(0, bot, mc.width, mc.height - bot);
  ctx.font = `700 ${Math.round(13 * dpr)}px system-ui, sans-serif`;
  for (const [y, label, below] of [[top, 'Top bar ends', true], [bot, 'Bottom bar starts', false]]) {
    ctx.fillStyle = '#ff2fd0';
    ctx.fillRect(0, Math.round(y - dpr), mc.width, Math.max(2, 2 * dpr));
    ctx.beginPath(); ctx.arc(mc.width - 22 * dpr, y, 11 * dpr, 0, Math.PI * 2); ctx.fill();
    const tw = ctx.measureText(label).width;
    ctx.fillRect(6 * dpr, below ? y + 4 * dpr : y - 24 * dpr, tw + 12 * dpr, 20 * dpr);
    ctx.fillStyle = '#fff';
    ctx.fillText(label, 12 * dpr, below ? y + 19 * dpr : y - 9 * dpr);
  }
}

function setBars(top, bottom, now = true) {
  const it = items[parseInt($('#markShot').value, 10) || 0];
  const most = it ? Math.floor(shotHeight(it) * 0.45) : 10_000;
  bars = { top: Math.max(0, Math.min(most, Math.round(top))), bottom: Math.max(0, Math.min(most, Math.round(bottom))) };
  $('#topPx').value = bars.top;
  $('#bottomPx').value = bars.bottom;
  drawMarker();
  if (now) replan();
}

// drag a line, or tap and the nearer line jumps there
let dragging = null;
const rowAt = (e) => {
  const r = mc.getBoundingClientRect(), it = items[parseInt($('#markShot').value, 10) || 0];
  return { y: ((e.clientY - r.top) / r.height) * shotHeight(it), H: shotHeight(it) };
};
mc.addEventListener('pointerdown', (e) => {
  if (!plan) return;
  const { y, H } = rowAt(e);
  dragging = Math.abs(y - bars.top) <= Math.abs(y - (H - bars.bottom)) ? 'top' : 'bottom';
  mc.setPointerCapture(e.pointerId);
  e.preventDefault();
  moveTo(e);
});
function moveTo(e) {
  const { y, H } = rowAt(e);
  if (dragging === 'top') setBars(y, bars.bottom, false); else setBars(bars.top, H - y, false);
}
mc.addEventListener('pointermove', (e) => { if (dragging) moveTo(e); });
for (const t of ['pointerup', 'pointercancel']) mc.addEventListener(t, () => { if (dragging) { dragging = null; replan(); } });
$('#markShot').addEventListener('change', drawMarker);
$('#topPx').addEventListener('change', () => setBars(parseFloat($('#topPx').value) || 0, bars.bottom));
$('#bottomPx').addEventListener('change', () => setBars(bars.top, parseFloat($('#bottomPx').value) || 0));
$('#topDown').addEventListener('click', () => setBars(bars.top - 1, bars.bottom));
$('#topUp').addEventListener('click', () => setBars(bars.top + 1, bars.bottom));
$('#bottomDown').addEventListener('click', () => setBars(bars.top, bars.bottom - 1));
$('#bottomUp').addEventListener('click', () => setBars(bars.top, bars.bottom + 1));
for (const r of document.querySelectorAll('input[name=bars]')) {
  r.addEventListener('change', () => {
    if (marking() && !bars) { // start from what was found
      const j = plan && plan.joins[0];
      bars = { top: j ? j.hdr : 0, bottom: j ? j.ftr : 0 };
    }
    replan();
  });
}

// ---------------------------------------------------------------- drawing

// output: the size to save at, and the rows of each part (in the common width's rows).
function output() {
  const W0 = plan.W0;
  const size = $('#size').value;
  let k = size === 'full' ? 1 : Math.min(1, parseInt(size, 10) / W0);
  const maxRows = Math.floor(Math.min(MAX_SIDE, MAX_AREA / Math.round(W0 * k)) / k);
  const tall = plan.height > maxRows;
  let parts = [[0, plan.height]];
  if (tall && document.querySelector('input[name=fit]:checked').value === 'split') {
    const cuts = splitRows(plan.height, maxRows, texAt);
    parts = cuts.slice(1).map((c, i) => [cuts[i], c]);
  } else if (tall) {
    k *= maxRows / plan.height;
  }
  return { k, W: Math.round(W0 * k), parts, tall, maxRows };
}

function texAt(r) {
  for (const s of plan.segments) {
    const h = s.y1 - s.y0;
    if (r < h) return plan.feats[s.i].tex[s.y0 + r];
    r -= h;
  }
  return 0;
}

// drawRows draws output rows [r0, r1) at scale k: each piece straight from its screenshot.
function drawRows(ctx, r0, r1, k) {
  let r = 0;
  for (const s of plan.segments) {
    const h = s.y1 - s.y0, a = Math.max(r0, r), b = Math.min(r1, r + h);
    if (b > a) {
      const it = items[s.i], q = it.w / plan.W0; // the screenshot's own pixels per common-width pixel
      const d0 = Math.round((a - r0) * k), d1 = Math.round((b - r0) * k);
      ctx.drawImage(it.img, 0, (s.y0 + a - r) * q, it.w, (b - a) * q, 0, d0, Math.round(plan.W0 * k), d1 - d0);
    }
    r += h;
  }
}

const canvas = $('#canvas');
function draw() {
  const has = !!plan;
  $('#empty').hidden = has;
  canvas.hidden = !has;
  for (const id of ['#saveBtn', '#shareBtn', '#copyBtn']) $(id).disabled = !has;
  if (!has) { $('#dims').textContent = 'Add screenshots to start'; note(''); $('#tallRow').hidden = true; return; }
  const o = output();
  const H = Math.round(plan.height * o.k);
  $('#dims').textContent = `${o.W.toLocaleString()} × ${H.toLocaleString()} px${o.parts.length > 1 ? ` · ${o.parts.length} parts` : ''}`;
  $('#tallRow').hidden = !o.tall;
  $('#tallText').textContent = o.tall ? `That’s taller than ${IOS ? 'an iPhone or iPad browser' : 'this browser'} can save in one go (${o.maxRows.toLocaleString()} px at this width).` : '';
  const bad = plan.joins.filter((j) => !j.ok).length;
  note(bad ? `${bad} screenshot${bad > 1 ? 's' : ''} didn’t line up with the one before; ${bad > 1 ? 'they’re' : 'it’s'} marked in the list.` : '');
  // preview at screen size
  const dpr = Math.min(2, devicePixelRatio || 1);
  const box = Math.max(200, $('#wrap').clientWidth - 2);
  const kp = Math.min(1, (box * dpr) / plan.W0, PREVIEW_MAX_H / plan.height);
  canvas.width = Math.round(plan.W0 * kp);
  canvas.height = Math.max(1, Math.round(plan.height * kp));
  canvas.style.width = `${canvas.width / dpr}px`;
  const ctx = canvas.getContext('2d');
  ctx.imageSmoothingQuality = 'high';
  drawRows(ctx, 0, plan.height, kp);
  if ($('#showJoins').checked || o.parts.length > 1) {
    ctx.save();
    ctx.lineWidth = Math.max(1, 2 * dpr);
    ctx.font = `600 ${Math.round(13 * dpr)}px system-ui, sans-serif`;
    let r = 0;
    plan.segments.forEach((s, i) => {
      if (i > 0 && $('#showJoins').checked) {
        const y = Math.round(r * kp) + 0.5;
        ctx.strokeStyle = '#ff2fd0'; ctx.setLineDash([8 * dpr, 6 * dpr]);
        ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(canvas.width, y); ctx.stroke();
        ctx.fillStyle = '#ff2fd0'; ctx.fillText(`${i} → ${i + 1}`, 6 * dpr, y - 5 * dpr);
      }
      r += s.y1 - s.y0;
    });
    for (const [r0] of o.parts.slice(1)) { // where a split falls
      const y = Math.round(r0 * kp) + 0.5;
      ctx.strokeStyle = '#22c55e'; ctx.setLineDash([]);
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(canvas.width, y); ctx.stroke();
    }
    ctx.restore();
  }
}

// ---------------------------------------------------------------- saving

const stamp = () => new Date().toISOString().slice(0, 16).replace(/[:T]/g, '-');
async function files() {
  const o = output();
  const jpeg = $('#format').value === 'jpeg';
  const type = jpeg ? 'image/jpeg' : 'image/png';
  const base = `long-screenshot-${stamp()}`;
  const out = [];
  for (let p = 0; p < o.parts.length; p++) {
    const [r0, r1] = o.parts[p];
    const c = document.createElement('canvas');
    c.width = o.W;
    c.height = Math.max(1, Math.round((r1 - r0) * o.k));
    const ctx = c.getContext('2d');
    ctx.imageSmoothingQuality = 'high';
    if (jpeg) { ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, c.width, c.height); }
    drawRows(ctx, r0, r1, o.k);
    const blob = await new Promise((res) => c.toBlob(res, type, parseInt($('#quality').value, 10) / 100));
    c.width = c.height = 1;
    if (!blob) throw new Error('The browser ran out of room making that image. Try a smaller width, JPEG, or splitting it.');
    const part = o.parts.length > 1 ? `-${p + 1}-of-${o.parts.length}` : '';
    out.push(new File([blob], `${base}${part}.${jpeg ? 'jpg' : 'png'}`, { type }));
  }
  return out;
}

async function busyWhile(btn, label, fn) {
  const was = btn.textContent;
  btn.disabled = true; btn.textContent = label;
  try { await fn(); } catch (e) { if (e.name !== 'AbortError') note(e.message); } finally { btn.disabled = false; btn.textContent = was; }
}

$('#saveBtn').addEventListener('click', () => busyWhile($('#saveBtn'), 'Saving…', async () => {
  for (const f of await files()) {
    const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(f), download: f.name });
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 60_000);
  }
}));
// iPhones: the share sheet has "Save Image", which puts it in Photos (a download goes to Files).
if (IOS && navigator.canShare) {
  $('#shareBtn').hidden = false;
  $('#shareBtn').addEventListener('click', () => busyWhile($('#shareBtn'), 'Preparing…', async () => {
    const fs = await files();
    await navigator.share({ files: fs });
  }));
}
if (!IOS && window.isSecureContext && window.ClipboardItem) {
  $('#copyBtn').hidden = false;
  $('#copyBtn').addEventListener('click', () => busyWhile($('#copyBtn'), 'Copying…', async () => {
    const o = output();
    if (o.parts.length > 1) throw new Error('It’s in parts; use Download instead.');
    const c = document.createElement('canvas');
    c.width = o.W; c.height = Math.round(plan.height * o.k);
    drawRows(c.getContext('2d'), 0, plan.height, o.k);
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'));
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
    note('Copied.');
  }));
}

// ---------------------------------------------------------------- options

$('#keepTop').addEventListener('change', replan);
$('#keepBottom').addEventListener('change', replan);
for (const id of ['#showJoins', '#size', '#format']) $(id).addEventListener('change', () => { $('#qualityRow').hidden = $('#format').value !== 'jpeg'; draw(); });
for (const r of document.querySelectorAll('input[name=fit]')) r.addEventListener('change', draw);
$('#quality').addEventListener('input', () => { $('#qualityOut').textContent = `${$('#quality').value}%`; });
let rz = 0;
addEventListener('resize', () => { cancelAnimationFrame(rz); rz = requestAnimationFrame(() => { if (plan) { draw(); drawMarker(); } }); });
draw();

// for automated checks
window.__stitch = { addFiles, get plan() { return plan; }, get bars() { return bars; }, output: () => plan && output(), files };
