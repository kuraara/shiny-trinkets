// Quote Finder panel editor: name the speakers, lay out the panels (Photo Stitch's controls) and
// export the image, all in the browser. Frames and line data come from the video-tools panel
// job (through /api/vt/, same origin, so the canvas can be exported). The drawing follows
// video-tools' render.py: Inter Bold name, Inter SemiBold “line”, white with a black outline,
// one font size for the whole strip, balanced two-row wrapping.
const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
const store = {
  get(k) { try { return JSON.parse(localStorage.getItem(k)); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch { /* private mode */ } },
};

const IOS = /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
const MAX_AREA = IOS ? 16_777_216 : 100_000_000;
const MAX_SIDE = 16_384;
const FONT = 'QFInter';
const MAX_PANELS = 24;

const jobId = new URLSearchParams(location.search).get('job') || '';
const files = (name) => `/api/vt/jobs/${jobId}/files/${encodeURIComponent(name)}`;
let job = null;
let frames = []; // {img, w, h, t}
let lines = [];  // {text, start, end, speaker, panel (0-based, -1 = off), pitch, sheet}
let title = 'panels';

// ---------------------------------------------------------------- options

const DEFAULTS = { ratio: 'original', cols: '1', unit: 'pct', gap: 2.1, margin: 2.4, bg: '#000000', textScale: 100,
  labels: false, names: true, size: 'full', sizePx: 1600, format: 'png', quality: 90 };
const RANGES = { pct: { max: 10, step: 0.1 }, px: { max: 200, step: 1 } };

function opts() {
  return {
    ratio: $('#ratio').value,
    cols: document.querySelector('input[name=cols]:checked').value,
    unit: document.querySelector('input[name=unit]:checked').value,
    gap: parseFloat($('#gap').value) || 0,
    margin: parseFloat($('#margin').value) || 0,
    bg: $('#bg').value,
    textScale: parseInt($('#textScale').value, 10) || 100,
    labels: $('#labels').checked,
    names: $('#names').checked,
    size: $('#size').value,
    sizePx: Math.min(MAX_SIDE, Math.max(200, parseInt($('#sizePx').value, 10) || 1600)),
    format: $('#format').value,
    quality: parseInt($('#quality').value, 10) || 90,
  };
}

function applyOpts(o) {
  o = { ...DEFAULTS, ...o };
  $('#ratio').value = o.ratio;
  for (const [name, v] of [['cols', o.cols], ['unit', o.unit]]) {
    const r = document.querySelector(`input[name=${name}][value="${v}"]`);
    if (r) r.checked = true;
  }
  setRanges(o.unit);
  $('#gap').value = o.gap; $('#margin').value = o.margin;
  $('#bg').value = o.bg; $('#textScale').value = o.textScale;
  $('#labels').checked = o.labels; $('#names').checked = o.names;
  $('#size').value = o.size; $('#sizePx').value = o.sizePx;
  $('#format').value = o.format; $('#quality').value = o.quality;
  syncOutputs();
}

function setRanges(unit) {
  for (const id of ['gap', 'margin']) {
    $(`#${id}`).max = RANGES[unit].max;
    $(`#${id}`).step = RANGES[unit].step;
  }
}

function syncOutputs() {
  const o = opts();
  const u = o.unit === 'pct' ? '%' : ' px';
  $('#gapOut').textContent = `${o.gap}${u}`;
  $('#marginOut').textContent = `${o.margin}${u}`;
  $('#textScaleOut').textContent = `${o.textScale}%`;
  $('#qualityOut').textContent = `${o.quality}%`;
  $('#sizePx').hidden = o.size !== 'custom';
  $('#qualityRow').hidden = o.format !== 'jpeg';
}

// ---------------------------------------------------------------- layout

// plan works out every size in output pixels.
function plan(o) {
  const n = frames.length;
  const fw = frames[0].w, fh = frames[0].h;
  const r = o.ratio === 'original' ? fw / fh : parseFloat(o.ratio);
  const cols = Math.min(parseInt(o.cols, 10), n), rows = Math.ceil(n / cols);
  const natural = r >= fw / fh ? fw : fh * r; // panel width with no scaling
  const notes = [];
  let pw, g, m;
  const pct = o.unit === 'pct';
  if (o.size === 'full') {
    pw = natural;
  } else {
    const T = o.size === 'custom' ? o.sizePx : parseInt(o.size, 10);
    pw = pct ? T / (cols + (cols - 1) * o.gap / 100 + 2 * o.margin / 100) : (T - (cols - 1) * o.gap - 2 * o.margin) / cols;
    if (pw > natural) { pw = natural; notes.push('kept at the frames’ own size (no upscaling)'); }
    if (pw < 50) { pw = 50; notes.push('the spacing leaves almost no room for the pictures'); }
  }
  g = pct ? o.gap / 100 * pw : o.gap;
  m = pct ? o.margin / 100 * pw : o.margin;
  let ph = pw / r;
  let W = cols * pw + (cols - 1) * g + 2 * m, H = rows * ph + (rows - 1) * g + 2 * m;
  const k = Math.min(1, MAX_SIDE / W, MAX_SIDE / H, Math.sqrt(MAX_AREA / (W * H)));
  if (k < 1) {
    pw *= k; ph *= k; g *= k; m *= k; W *= k; H *= k;
    notes.push(`scaled down to fit what ${IOS ? 'iPhone and iPad browsers' : 'the browser'} can make`);
  }
  return { n, fw, fh, r, cols, rows, pw, ph, g, m, W: Math.round(W), H: Math.round(H), notes };
}

// ---------------------------------------------------------------- text (as render.py)

const tidy = (t) => t.trim().replace(/^["“”]+|["“”]+$/g, '').trim().replace(/'/g, '’');

function tokens(line, names) {
  const t = [];
  if (names && line.speaker.trim()) t.push({ w: `${line.speaker.trim()}:`, bold: true });
  for (const w of `“${tidy(line.text)}”`.split(/\s+/).filter(Boolean)) t.push({ w, bold: false });
  return t;
}

function fontFor(bold, size) { return `${bold ? 700 : 600} ${size}px ${FONT}, system-ui, sans-serif`; }

function measure(ctx, toks, size) {
  if (!toks.length) return 0;
  ctx.font = fontFor(false, size);
  const sp = ctx.measureText(' ').width;
  let w = sp * (toks.length - 1);
  for (const t of toks) { ctx.font = fontFor(t.bold, size); w += ctx.measureText(t.w).width; }
  return w;
}

function wrap(ctx, toks, size, maxw) {
  if (measure(ctx, toks, size) <= maxw) return [toks];
  let best = null;
  for (let k = 1; k < toks.length; k++) {
    const w = Math.max(measure(ctx, toks.slice(0, k), size), measure(ctx, toks.slice(k), size));
    if (!best || w < best[0]) best = [w, k];
  }
  if (best && best[0] <= maxw) return [toks.slice(0, best[1]), toks.slice(best[1])];
  const rows = []; let cur = [];
  for (const t of toks) {
    if (cur.length && measure(ctx, [...cur, t], size) > maxw) { rows.push(cur); cur = []; }
    cur.push(t);
  }
  return [...rows, cur];
}

// One font size for every panel: the largest between 3.2% and 2.6% of the panel width at which
// every line that can fit on one row does.
function fontSize(ctx, L, o, panelLines) {
  const maxw = L.pw - L.pw * 0.045;
  const sc = o.textScale / 100;
  const hi = Math.max(8, Math.round(L.pw * 0.032 * sc)), lo = Math.max(8, Math.round(L.pw * 0.026 * sc));
  const all = panelLines.flat().map((l) => tokens(l, o.names));
  const fit = all.filter((t) => measure(ctx, t, lo) <= maxw);
  for (let s = hi; s >= lo; s--) if (fit.every((t) => measure(ctx, t, s) <= maxw)) return s;
  return lo;
}

// ---------------------------------------------------------------- drawing

function draw(ctx, L, o, s) {
  ctx.setTransform(s, 0, 0, s, 0, 0);
  ctx.fillStyle = o.bg;
  ctx.fillRect(0, 0, L.W, L.H);
  const panelLines = frames.map((_, i) => lines.filter((l) => l.panel === i && l.text.trim()));
  const size = fontSize(ctx, L, o, panelLines);
  const stroke = Math.max(2, Math.round(size * 0.095)), lineh = Math.round(size * 1.33);
  const maxw = L.pw - L.pw * 0.045;
  frames.forEach((f, i) => {
    const row = Math.floor(i / L.cols), col = i % L.cols;
    const inRow = row === L.rows - 1 ? L.n - row * L.cols : L.cols;
    const x0 = L.m + col * (L.pw + L.g) + (L.cols - inRow) * (L.pw + L.g) / 2;
    const y0 = L.m + row * (L.ph + L.g);
    // centre crop to the panel shape
    let sx = 0, sy = 0, sw = f.w, sh = f.h;
    if (f.w / f.h > L.r) { sw = f.h * L.r; sx = (f.w - sw) / 2; } else { sh = f.w / L.r; sy = (f.h - sh) / 2; }
    ctx.drawImage(f.img, sx, sy, sw, sh, x0, y0, L.pw, L.ph);
    ctx.save();
    ctx.beginPath(); ctx.rect(x0, y0, L.pw, L.ph); ctx.clip();
    const rows = panelLines[i].flatMap((l) => wrap(ctx, tokens(l, o.names), size, maxw));
    let y = y0 + L.ph - Math.round(L.ph * 0.054) - rows.length * lineh;
    ctx.textBaseline = 'top';
    ctx.lineJoin = 'round';
    for (const r of rows) {
      let x = x0 + (L.pw - measure(ctx, r, size)) / 2;
      ctx.font = fontFor(false, size);
      const sp = ctx.measureText(' ').width;
      for (const t of r) {
        ctx.font = fontFor(t.bold, size);
        ctx.lineWidth = stroke * 2; ctx.strokeStyle = '#000'; ctx.strokeText(t.w, x, y);
        ctx.fillStyle = '#fff'; ctx.fillText(t.w, x, y);
        x += ctx.measureText(t.w).width + sp;
      }
      y += lineh;
    }
    if (o.labels) {
      const ls = Math.max(12, Math.round(L.pw * 0.028));
      ctx.font = fontFor(true, ls);
      const label = `${i + 1}/${L.n}`;
      const pad = ls * 0.45, w = ctx.measureText(label).width + pad * 2, h = ls + pad * 1.4;
      const lx = x0 + ls * 0.6, ly = y0 + ls * 0.6;
      ctx.fillStyle = 'rgba(0,0,0,.55)';
      ctx.beginPath(); ctx.roundRect(lx, ly, w, h, h / 2); ctx.fill();
      ctx.fillStyle = '#fff'; ctx.fillText(label, lx + pad, ly + pad * 0.7);
    }
    ctx.restore();
  });
}

let raf = 0;
function redraw() {
  cancelAnimationFrame(raf);
  raf = requestAnimationFrame(() => {
    if (!frames.length) return;
    const o = opts();
    const L = plan(o);
    const c = $('#preview');
    const box = c.parentElement.clientWidth - 2;
    const dpr = Math.min(2, devicePixelRatio || 1);
    const pwid = Math.min(L.W, Math.round(box * dpr));
    const s = pwid / L.W;
    c.width = pwid; c.height = Math.max(1, Math.round(L.H * s));
    c.style.width = `${pwid / dpr}px`;
    draw(c.getContext('2d'), L, o, s);
    $('#sizeNote').textContent = `${L.W} × ${L.H} px${L.notes.length ? ` · ${L.notes.join('; ')}` : ''}`;
  });
}

// ---------------------------------------------------------------- lines UI

const clock = (sec) => `${Math.floor(sec / 60)}:${(sec % 60).toFixed(1).padStart(4, '0')}`;

function saveLines() {
  store.set(`qf-panel:${jobId}`, lines.map((l) => ({ speaker: l.speaker, text: l.text, panel: l.panel })));
  const names = [...new Set(lines.map((l) => l.speaker.trim()).filter(Boolean))];
  $('#speakers').replaceChildren(...names.map((n) => { const o = el('option'); o.value = n; return o; }));
}

function buildLines() {
  const list = $('#lines');
  list.replaceChildren(...lines.map((l, i) => {
    const li = el('li', 'pline');
    const top = el('div', 'pl-top');
    top.append(el('span', 'ts', clock(l.start)));
    const sel = el('select');
    sel.setAttribute('aria-label', `Panel for line ${i + 1}`);
    frames.forEach((_, fi) => { const o = el('option', '', `Panel ${fi + 1}`); o.value = fi; sel.append(o); });
    const off = el('option', '', 'Leave off'); off.value = -1; sel.append(off);
    sel.value = l.panel;
    // ‹ › push the line to the panel before or after
    const prev = el('button', 'step', '‹'), next = el('button', 'step', '›');
    prev.type = next.type = 'button';
    const sync = () => {
      prev.disabled = l.panel <= 0;
      next.disabled = l.panel < 0 || l.panel >= frames.length - 1;
      prev.setAttribute('aria-label', `Move line ${i + 1} to panel ${l.panel}`);
      next.setAttribute('aria-label', `Move line ${i + 1} to panel ${l.panel + 2}`);
      prev.title = 'To the panel before'; next.title = 'To the panel after';
    };
    const setPanel = (p) => { l.panel = p; sel.value = p; sync(); saveLines(); redraw(); };
    prev.addEventListener('click', () => setPanel(l.panel - 1));
    next.addEventListener('click', () => setPanel(l.panel + 1));
    sel.addEventListener('change', () => setPanel(parseInt(sel.value, 10)));
    sync();
    const move = el('div', 'move');
    move.append(prev, sel, next);
    top.append(move);
    const who = el('input', 'speaker');
    who.setAttribute('list', 'speakers'); who.placeholder = 'Who says it?'; who.value = l.speaker;
    who.setAttribute('aria-label', `Speaker for line ${i + 1}`);
    who.addEventListener('input', () => { l.speaker = who.value; redraw(); });
    who.addEventListener('change', saveLines);
    const text = el('textarea', 'ltext');
    text.rows = 2; text.value = l.text;
    text.setAttribute('aria-label', `Words of line ${i + 1}`);
    text.addEventListener('input', () => { l.text = text.value; redraw(); });
    text.addEventListener('change', saveLines);
    li.append(top, who, text);
    if (l.sheet) {
      const d = el('details', 'whotalks');
      const sum = el('summary', '', 'Who’s talking?');
      d.append(sum);
      d.addEventListener('toggle', () => {
        if (!d.open || d.querySelector('img')) return;
        const img = el('img'); img.alt = `Eight frames across line ${i + 1}`; img.src = files(l.sheet);
        d.append(img);
        if (l.pitch) {
          d.append(el('p', 'muted', `Voice ${l.pitch.median} Hz · ${l.pitch.range}${l.pitch.flat_suspect ? ' · very steady, may be music or an effect' : ''}`));
        }
      });
      li.append(d);
    }
    return li;
  }));
  saveLines();
}

// ---------------------------------------------------------------- number of panels

// A different number of panels means new frames: Quote Finder starts a fresh video-tools job on
// the same scene with that many, and this page switches to it. Names and wording carry over.
function setupCount() {
  const inp = $('#panelCount'), btn = $('#retake');
  inp.max = MAX_PANELS;
  inp.value = frames.length;
  const want = () => Math.min(MAX_PANELS, Math.max(1, parseInt(inp.value, 10) || frames.length));
  const update = () => {
    const n = want();
    btn.hidden = n === frames.length;
    $('#countHint').hidden = btn.hidden;
    btn.textContent = `Take ${n} new frame${n === 1 ? '' : 's'}`;
  };
  const step = (d) => { inp.value = want() + d; inp.value = want(); update(); };
  $('#countDown').addEventListener('click', () => step(-1));
  $('#countUp').addEventListener('click', () => step(1));
  inp.addEventListener('input', update);
  inp.addEventListener('change', () => { inp.value = want(); update(); });
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    btn.textContent = 'Starting…';
    try {
      const r = await fetch(`/api/vt/jobs/${jobId}/retake`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ panels: want() }),
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok || !j.jobId) throw new Error(j.error || 'Couldn’t take new frames.');
      store.set(`qf-panel:${j.jobId}`, lines.map((l) => ({ speaker: l.speaker, text: l.text })));
      location.replace(j.url);
    } catch (e) {
      alert(e.message);
      btn.disabled = false;
      update();
    }
  });
  update();
}

// ---------------------------------------------------------------- export

async function exportBlob() {
  const o = opts();
  const L = plan(o);
  const c = document.createElement('canvas');
  c.width = L.W; c.height = L.H;
  draw(c.getContext('2d'), L, o, 1);
  const type = o.format === 'jpeg' ? 'image/jpeg' : 'image/png';
  const blob = await new Promise((res) => c.toBlob(res, type, o.quality / 100));
  if (!blob) throw new Error('The browser couldn’t make an image that big; pick a smaller width.');
  return new File([blob], `${title}.${o.format === 'jpeg' ? 'jpg' : 'png'}`, { type });
}

// Keep video-tools in step (its transcript and its own render); best effort.
function syncToVideoTools() {
  const body = { lines: lines.map((l) => ({ speaker: l.speaker.trim(), text: l.text.trim(), panel: l.panel + 1 })) };
  fetch(`/api/vt/jobs/${jobId}/render`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }).catch(() => {});
}

$('#download').addEventListener('click', async () => {
  const b = $('#download');
  b.disabled = true;
  try {
    const f = await exportBlob();
    const a = el('a'); a.href = URL.createObjectURL(f); a.download = f.name;
    document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 30_000);
    syncToVideoTools();
  } catch (e) { alert(e.message); } finally { b.disabled = false; }
});
$('#share').addEventListener('click', async () => {
  try {
    const f = await exportBlob();
    await navigator.share({ files: [f], title });
    syncToVideoTools();
  } catch (e) { if (e.name !== 'AbortError') alert(e.message); }
});

// ---------------------------------------------------------------- controls

function onChange() { syncOutputs(); store.set('qf-panel-layout', opts()); redraw(); }
for (const e of document.querySelectorAll('#editor input, #editor select')) {
  if (e.closest('#lines') || e.id === 'panelCount') continue;
  e.addEventListener('input', onChange);
  e.addEventListener('change', onChange);
}
// switching units keeps the spacing the same size on screen
for (const r of document.querySelectorAll('input[name=unit]')) {
  r.addEventListener('change', () => {
    if (!frames.length) return;
    const o = opts();
    const L = plan({ ...o, unit: o.unit === 'px' ? 'pct' : 'px' });
    const conv = (v) => (o.unit === 'px' ? Math.round(v / 100 * L.pw) : Math.round(v / L.pw * 1000) / 10);
    setRanges(o.unit);
    $('#gap').value = conv(parseFloat($('#gap').value));
    $('#margin').value = conv(parseFloat($('#margin').value));
    onChange();
  });
}
$('#resetLayout').addEventListener('click', () => { applyOpts({ ...DEFAULTS, size: $('#size').value, format: $('#format').value }); onChange(); });
addEventListener('resize', redraw);

// ---------------------------------------------------------------- load

async function load() {
  if (document.referrer && new URL(document.referrer).origin === location.origin) {
    $('#back').addEventListener('click', (e) => { e.preventDefault(); history.back(); });
  }
  if (!/^j_[a-z0-9]+$/.test(jobId)) return fail('No panel job given.');
  applyOpts(store.get('qf-panel-layout') || {});
  const fonts = [new FontFace(FONT, 'url(fonts/Inter-Bold.otf)', { weight: '700' }), new FontFace(FONT, 'url(fonts/Inter-SemiBold.otf)', { weight: '600' })];
  const fontsReady = Promise.all(fonts.map((f) => f.load().then(() => document.fonts.add(f)).catch(() => {})));
  for (;;) {
    let r;
    try { r = await fetch(`/api/vt/jobs/${jobId}`); job = await r.json(); } catch { return fail('Couldn’t reach Video Tools.'); }
    if (!r.ok) return fail(job.error || 'That panel job doesn’t exist any more (jobs are kept for a day).');
    title = (job.title || 'panels').replace(/\.[a-z0-9]+$/i, '');
    $('#title').textContent = title;
    document.title = `${title} · Panels`;
    if (job.status === 'failed' || job.status === 'cancelled') return fail(job.error || 'Making the frames failed.');
    if (job.status === 'review' || job.status === 'done') break;
    $('#waitText').textContent = job.message ? job.message.charAt(0).toUpperCase() + job.message.slice(1) : 'Waiting…';
    $('#waitSub').textContent = job.progress ? `${job.progress}%` : 'This takes a few seconds.';
    await new Promise((res) => setTimeout(res, 1000));
  }
  const p = job.panel;
  const base = (u) => u.split('/').pop();
  const choices = Array.isArray(p.current_choices) ? p.current_choices : []; // {speaker, text, frame: 0-based, -1 off}
  const saved = store.get(`qf-panel:${jobId}`);
  lines = p.lines.map((l, i) => {
    const ch = choices[i] || {};
    const sv = saved?.[i];
    return {
      text: sv?.text ?? ch.text ?? l.text, start: l.start, end: l.end, speaker: sv?.speaker ?? ch.speaker ?? '',
      panel: Number.isInteger(sv?.panel) ? sv.panel : (ch.frame ?? l.panel - 1), pitch: l.pitch_hz, sheet: l.contact_sheet_url ? base(l.contact_sheet_url) : '',
    };
  });
  frames = await Promise.all(p.frames.map((f) => new Promise((res, rej) => {
    const img = new Image();
    img.onload = () => res({ img, w: img.naturalWidth, h: img.naturalHeight, t: f.time });
    img.onerror = () => rej(new Error('A frame wouldn’t load.'));
    img.src = files(base(f.image_url));
  }))).catch((e) => { fail(e.message); return []; });
  if (!frames.length) return;
  for (const l of lines) if (l.panel >= frames.length) l.panel = frames.length - 1;
  await fontsReady;
  $('#wait').hidden = true;
  $('#editor').hidden = false;
  const cfg = await fetch('/api/config').then((r) => r.json()).catch(() => ({}));
  if (cfg.videoTools) $('#toolsLink').href = `${cfg.videoTools}/jobs/${jobId}`; else $('#toolsLink').hidden = true;
  // the registry knows video-tools' port and protocols; prefer its link when it's there
  window.gadgets?.url('video-tools', `/jobs/${jobId}`).then((u) => {
    if (u) { $('#toolsLink').href = u; $('#toolsLink').hidden = false; }
  }).catch(() => {});
  buildLines();
  setupCount();
  if (navigator.canShare && IOS) { // the share sheet has "Save Image"; a download goes to Files
    $('#share').hidden = false;
    $('#share').textContent = 'Save to Photos…';
  }
  redraw();
}

function fail(msg) {
  $('#wait').hidden = true;
  const f = $('#fail'); f.textContent = msg; f.hidden = false;
}

load();
