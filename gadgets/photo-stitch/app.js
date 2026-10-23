// Photo Stitch: join photos vertically, horizontally or in a grid, entirely in the browser.
const $ = (s) => document.querySelector(s);
const items = []; // {id, name, img, w, h, url}
let nextId = 1;

// Biggest canvas the browser will reliably make. iOS Safari caps canvases at ~16.7 MP.
const IOS = /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
const MAX_AREA = IOS ? 16_777_216 : 100_000_000;
const MAX_SIDE = 16_384;
const PREVIEW_SIDE = 1600;

// ---- options (remembered between visits) ----
const opts = () => ({
  layout: document.querySelector('input[name=layout]:checked').value,
  fit: document.querySelector('input[name=fit]:checked').value,
  cols: Math.max(1, parseInt($('#cols').value, 10) || 1),
  rows: parseInt($('#rows').value, 10) || 0,
  ratio: $('#ratio').value,
  gap: parseInt($('#gap').value, 10) || 0,
  margin: parseInt($('#margin').value, 10) || 0,
  border: parseInt($('#border').value, 10) || 0,
  borderColor: $('#borderColor').value,
  bg: $('#bg').value,
  transparent: $('#transparent').checked && $('#format').value === 'png',
  format: $('#format').value,
  quality: parseInt($('#quality').value, 10) || 92,
  size: $('#size').value, // 'full', a longest-side preset, or 'custom'
  sizePx: Math.max(100, parseInt($('#sizePx').value, 10) || 2048),
});

function saveOpts() {
  try { localStorage.setItem('photo-stitch', JSON.stringify(opts())); } catch { /* ignore */ }
}
function loadOpts() {
  let o;
  try { o = JSON.parse(localStorage.getItem('photo-stitch')); } catch { o = null; }
  if (!o) return;
  const set = (name, v) => { const el = document.querySelector(`input[name=${name}][value="${v}"]`); if (el) el.checked = true; };
  set('layout', o.layout); set('fit', o.fit);
  $('#cols').value = o.cols || 2;
  $('#rows').value = o.rows || '';
  $('#ratio').value = o.ratio || 'first';
  $('#gap').value = o.gap || 0;
  $('#margin').value = o.margin || 0;
  $('#border').value = o.border || 0;
  $('#borderColor').value = o.borderColor || '#ffffff';
  $('#bg').value = o.bg || '#ffffff';
  $('#transparent').checked = !!o.transparent;
  $('#format').value = o.format || 'png';
  $('#quality').value = o.quality || 92;
  $('#size').value = o.size || 'full';
  if (!$('#size').value) $('#size').value = 'full';
  $('#sizePx').value = o.sizePx || 2048;
}

// ---- adding photos ----
async function addFiles(files) {
  for (const file of files) {
    if (!file.type.startsWith('image/') && !/\.(jpe?g|png|gif|webp|avif|heic|bmp)$/i.test(file.name)) continue;
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.src = url;
    try {
      await img.decode(); // <img> applies EXIF rotation, so phone photos come out upright
    } catch {
      URL.revokeObjectURL(url);
      note(`Couldn't read ${file.name || 'that image'}. HEIC photos only open in Safari; try JPEG or PNG.`);
      continue;
    }
    items.push({ id: nextId++, name: file.name || 'Pasted image', img, w: img.naturalWidth, h: img.naturalHeight, url });
  }
  renderList();
  update();
}

$('#pick').addEventListener('change', (e) => { addFiles([...e.target.files]); e.target.value = ''; });

document.addEventListener('paste', (e) => {
  const files = [...(e.clipboardData?.items || [])].filter((i) => i.kind === 'file' && i.type.startsWith('image/')).map((i) => i.getAsFile());
  if (files.length) { e.preventDefault(); addFiles(files); }
});

if (window.isSecureContext && navigator.clipboard?.read) {
  $('#pasteBtn').hidden = false;
  $('#pasteBtn').addEventListener('click', async () => {
    try {
      const out = [];
      for (const it of await navigator.clipboard.read()) {
        const type = it.types.find((t) => t.startsWith('image/'));
        if (type) out.push(new File([await it.getType(type)], 'Pasted image', { type }));
      }
      if (out.length) addFiles(out); else note('There’s no image on the clipboard.');
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

// ---- the list ----
let dragId = null;
function renderList() {
  const list = $('#list');
  list.replaceChildren();
  items.forEach((it, i) => {
    const li = document.createElement('li');
    li.draggable = true;
    li.dataset.id = it.id;
    const thumb = document.createElement('img');
    thumb.src = it.url; thumb.alt = '';
    const meta = document.createElement('div');
    meta.className = 'meta';
    const name = document.createElement('b'); name.textContent = it.name;
    const size = document.createElement('small'); size.textContent = `${it.w} × ${it.h}`;
    meta.append(name, size);
    const btn = (label, title, fn, disabled) => {
      const b = document.createElement('button');
      b.type = 'button'; b.textContent = label; b.title = title; b.setAttribute('aria-label', title);
      b.disabled = disabled; b.addEventListener('click', fn);
      return b;
    };
    li.append(
      Object.assign(document.createElement('span'), { className: 'num', textContent: i + 1 }),
      thumb, meta,
      btn('↑', 'Move earlier', () => move(i, i - 1), i === 0),
      btn('↓', 'Move later', () => move(i, i + 1), i === items.length - 1),
      btn('✕', 'Remove', () => remove(i), false),
    );
    li.addEventListener('dragstart', (e) => { dragId = it.id; e.dataTransfer.effectAllowed = 'move'; li.classList.add('dragging'); });
    li.addEventListener('dragend', () => { dragId = null; li.classList.remove('dragging'); });
    li.addEventListener('dragover', (e) => { if (dragId != null) { e.preventDefault(); e.stopPropagation(); } });
    li.addEventListener('drop', (e) => {
      if (dragId == null) return;
      e.preventDefault(); e.stopPropagation();
      const from = items.findIndex((x) => x.id === dragId);
      move(from, i);
    });
    list.append(li);
  });
  $('#listHint').hidden = items.length < 2;
}

function move(from, to) {
  if (to < 0 || to >= items.length || from === to) return;
  const [it] = items.splice(from, 1);
  items.splice(to, 0, it);
  renderList();
  update();
}

function remove(i) {
  const [it] = items.splice(i, 1);
  URL.revokeObjectURL(it.url);
  renderList();
  update();
}

// ---- layout ----
function median(xs) {
  const s = [...xs].sort((a, b) => a - b);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
}

// layout places photos with their pixels scaled by s; gaps, borders and the margin stay in
// final-image pixels so they look the same whatever output size is chosen.
function layout(list, o, s = 1) {
  const n = list.length;
  let cols, rows;
  if (o.layout === 'vertical') { cols = 1; rows = n; }
  else if (o.layout === 'horizontal') { cols = n; rows = 1; }
  else { cols = Math.min(o.cols, n); rows = o.rows || Math.ceil(n / cols); }
  const used = list.slice(0, cols * rows);
  const g = o.gap, b = o.border, m = o.margin;
  const cells = [];
  let W, H;

  if (o.fit === 'same') {
    const R = o.ratio === 'first' ? used[0].w / used[0].h : parseFloat(o.ratio);
    const crops = used.map((it) => { const cw = Math.min(it.w, it.h * R); return { cw, ch: cw / R }; });
    const tw = median(crops.map((c) => c.cw)) * s;
    const th = tw / R;
    const cw = tw + 2 * b, ch = th + 2 * b; // cell = photo + its border
    W = 2 * m + cols * cw + (cols - 1) * g;
    H = 2 * m + rows * ch + (rows - 1) * g;
    used.forEach((it, i) => {
      const c = i % cols, r = Math.floor(i / cols);
      const x = m + c * (cw + g), y = m + r * (ch + g);
      cells.push({ it, sx: (it.w - crops[i].cw) / 2, sy: (it.h - crops[i].ch) / 2, sw: crops[i].cw, sh: crops[i].ch,
        bx: x, by: y, bw: cw, bh: ch, dx: x + b, dy: y + b, dw: tw, dh: th });
    });
  } else {
    const colW = new Array(cols).fill(0), rowH = new Array(rows).fill(0);
    used.forEach((it, i) => {
      const c = i % cols, r = Math.floor(i / cols);
      colW[c] = Math.max(colW[c], it.w * s + 2 * b);
      rowH[r] = Math.max(rowH[r], it.h * s + 2 * b);
    });
    const colX = [], rowY = [];
    colW.reduce((x, w, c) => { colX[c] = x; return x + w + g; }, m);
    rowH.reduce((y, h, r) => { rowY[r] = y; return y + h + g; }, m);
    W = 2 * m + colW.reduce((a, v) => a + v, 0) + (cols - 1) * g;
    H = 2 * m + rowH.reduce((a, v) => a + v, 0) + (rows - 1) * g;
    used.forEach((it, i) => {
      const c = i % cols, r = Math.floor(i / cols);
      const dw = it.w * s, dh = it.h * s;
      const dx = colX[c] + (colW[c] - dw) / 2, dy = rowY[r] + (rowH[r] - dh) / 2;
      cells.push({ it, sx: 0, sy: 0, sw: it.w, sh: it.h, bx: dx - b, by: dy - b, bw: dw + 2 * b, bh: dh + 2 * b, dx, dy, dw, dh });
    });
  }
  return { W, H, cells, cols, rows, left: n - used.length, s };
}

const fitsBrowser = (W, H) => Math.max(W, H) <= MAX_SIDE && W * H <= MAX_AREA;

// plan picks how much to scale the photos so the image matches the chosen size (never
// enlarging them) and fits what the browser can save. Borders and gaps don't scale.
function plan(list, o) {
  const full = layout(list, o, 1);
  const fixed = layout(list, o, 0); // just the borders, gaps and margin
  const target = o.size === 'full' ? Infinity : o.size === 'custom' ? o.sizePx : parseInt(o.size, 10);
  let s = 1;
  for (const [a, z] of [[fixed.W, full.W], [fixed.H, full.H]]) {
    if (z > target) s = Math.min(s, Math.max(0.01, (target - a) / (z - a)));
  }
  let L = layout(list, o, s);
  for (let i = 0; i < 40 && !fitsBrowser(L.W, L.H); i++) { s *= 0.95; L = layout(list, o, s); }
  L.capped = !fitsBrowser(full.W, full.H) && (o.size === 'full' || s < 1);
  L.limited = L.capped && Math.max(L.W, L.H) < Math.min(target, Math.max(full.W, full.H)) - 1;
  return L;
}

function draw(canvas, L, o, scale) {
  canvas.width = Math.max(1, Math.round(L.W * scale));
  canvas.height = Math.max(1, Math.round(L.H * scale));
  const ctx = canvas.getContext('2d');
  ctx.imageSmoothingQuality = 'high';
  if (o.transparent) ctx.clearRect(0, 0, canvas.width, canvas.height);
  else { ctx.fillStyle = o.bg; ctx.fillRect(0, 0, canvas.width, canvas.height); }
  for (const c of L.cells) {
    if (o.border > 0) {
      ctx.fillStyle = o.borderColor;
      ctx.fillRect(c.bx * scale, c.by * scale, c.bw * scale, c.bh * scale);
    }
    ctx.drawImage(c.it.img, c.sx, c.sy, c.sw, c.sh, c.dx * scale, c.dy * scale, c.dw * scale, c.dh * scale);
  }
}

// ---- preview and saving ----
let pending = 0;
function update() {
  const o = opts();
  saveOpts();
  $('#gridOpts').hidden = o.layout !== 'grid';
  $('#ratioRow').hidden = o.fit !== 'same';
  $('#gapOut').textContent = `${o.gap} px`;
  $('#marginOut').textContent = `${o.margin} px`;
  $('#borderOut').textContent = `${o.border} px`;
  $('#borderColor').disabled = o.border === 0;
  $('#transparent').disabled = o.format !== 'png';
  $('#qualityRow').hidden = o.format !== 'jpeg';
  $('#qualityOut').textContent = `${o.quality}%`;
  $('#sizePx').hidden = o.size !== 'custom';
  cancelAnimationFrame(pending);
  pending = requestAnimationFrame(() => render(o));
}

function note(msg) {
  $('#note').textContent = msg || '';
  $('#note').hidden = !msg;
}

let current = null;
function render(o) {
  const canvas = $('#canvas');
  const ready = items.length >= 1;
  $('#empty').hidden = ready;
  canvas.hidden = !ready;
  $('#saveBtn').disabled = !ready;
  $('#copyBtn').hidden = !(ready && window.isSecureContext && window.ClipboardItem && navigator.clipboard?.write);
  if (!ready) { $('#dims').textContent = 'Add photos to start'; note(''); current = null; return; }
  const L = plan(items, o);
  current = { L, o };
  const w = Math.round(L.W), h = Math.round(L.H);
  $('#dims').textContent = `${w.toLocaleString()} × ${h.toLocaleString()} px · ${L.cols} × ${L.rows}${L.s < 1 ? ` · photos at ${Math.round(L.s * 100)}%` : ''}`;
  const msgs = [];
  if (L.limited) msgs.push(`That's bigger than this browser can save, so it's been made smaller.`);
  if (L.left > 0) msgs.push(`${L.left} photo${L.left > 1 ? 's don’t' : ' doesn’t'} fit in ${L.cols} × ${L.rows} and ${L.left > 1 ? 'are' : 'is'} left out.`);
  note(msgs.join(' '));
  draw(canvas, L, o, Math.min(1, PREVIEW_SIDE / Math.max(L.W, L.H)));
}

function finalBlob() {
  const { L, o } = current;
  const c = document.createElement('canvas');
  draw(c, L, o, 1);
  const type = o.format === 'jpeg' ? 'image/jpeg' : 'image/png';
  return new Promise((res, rej) => c.toBlob((b) => (b ? res(b) : rej(new Error('too big'))), type, o.quality / 100));
}

$('#saveBtn').addEventListener('click', async () => {
  if (!current) return;
  const btn = $('#saveBtn');
  btn.disabled = true; btn.textContent = 'Saving…';
  try {
    const blob = await finalBlob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `stitched-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.${current.o.format === 'jpeg' ? 'jpg' : 'png'}`;
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 60_000);
  } catch {
    note('The browser ran out of room making that image. Try fewer photos or JPEG.');
  } finally {
    btn.disabled = false; btn.textContent = 'Download';
  }
});

$('#copyBtn').addEventListener('click', async () => {
  try {
    const { L, o } = current;
    const c = document.createElement('canvas');
    draw(c, L, o, 1);
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'));
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
    $('#copyBtn').textContent = 'Copied ✓';
    setTimeout(() => { $('#copyBtn').textContent = 'Copy'; }, 2000);
  } catch { note('Couldn’t copy; use Download instead.'); }
});

document.querySelectorAll('.controls input, .controls select').forEach((el) => {
  if (el.id !== 'pick') el.addEventListener('input', update);
});

loadOpts();
update();

// test hook: lets automated checks read the layout
window.__stitch = { items, layout: () => current && { W: Math.round(current.L.W), H: Math.round(current.L.H), cols: current.L.cols, rows: current.L.rows, s: current.L.s } };
