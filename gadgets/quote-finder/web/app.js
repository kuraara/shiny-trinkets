// Quote Finder search page.
const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };

const fmtTime = (ms) => {
  const s = Math.floor(ms / 1000), h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60;
  return (h ? `${h}:${String(m).padStart(2, '0')}` : `${m}`) + `:${String(x).padStart(2, '0')}`;
};
const epCode = (h) => h.kind !== 'tv' ? (h.year ? `(${h.year})` : '')
  : `S${String(h.season).padStart(2, '0')}E${String(h.episode).padStart(2, '0')}${h.episode2 ? `-E${String(h.episode2).padStart(2, '0')}` : ''}`;

// Highlight what was searched for, without ever inserting HTML from the data.
function highlighter(q, regex) {
  if (regex) {
    try { return new RegExp(q, 'gi'); } catch { return null; }
  }
  const words = (q.match(/[\p{L}\p{N}']+/gu) || []).filter((w) => w.length > 1)
    .map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  if (!words.length) return null;
  // match word starts so "likes" still lights up "like" and "liked"
  return new RegExp(`\\b(${words.map((w) => w.length > 4 ? w.slice(0, -1) : w).join('|')})[\\p{L}']*`, 'giu');
}

function markText(node, text, re) {
  if (!re) { node.textContent = text; return; }
  re.lastIndex = 0;
  let last = 0, m;
  while ((m = re.exec(text)) && m[0] !== '') {
    node.append(text.slice(last, m.index), el('mark', '', m[0]));
    last = m.index + m[0].length;
  }
  node.append(text.slice(last));
}

let seq = 0;
async function search(pushHistory) {
  const q = $('#q').value.trim();
  const regex = $('#regex').checked;
  const t = $('#title').value;
  const params = new URLSearchParams();
  if (q) params.set('q', q);
  if (regex) params.set('regex', '1');
  if (t) params.set('t', t);
  const url = params.toString() ? `?${params}` : location.pathname;
  if (pushHistory) history.replaceState(null, '', url);
  const list = $('#results');
  if (!q) { list.replaceChildren(); $('#status').textContent = ''; $('#hint').hidden = false; return; }
  $('#hint').hidden = true;
  const api = new URLSearchParams({ q, limit: '60' });
  if (regex) api.set('regex', '1');
  if (t.startsWith('kind:')) api.set('kind', t.slice(5)); else if (t) api.set('title', t);
  const mine = ++seq;
  $('#status').textContent = 'Searching…';
  let res;
  try {
    const r = await fetch(`/api/search?${api}`);
    res = await r.json();
    if (!r.ok) throw new Error(res.error || 'Search failed');
  } catch (e) {
    if (mine === seq) { $('#status').textContent = e.message; list.replaceChildren(); }
    return;
  }
  if (mine !== seq) return; // a newer search has started
  const n = res.hits.length;
  $('#status').textContent = n === 0 ? 'No matches.'
    : `${n}${res.more ? '+' : ''} match${n === 1 ? '' : 'es'}${res.mode === 'any' ? ' with some of the words (none had all of them)' : ''} · ${res.tookMs} ms`;
  const re = highlighter(q, regex);
  list.replaceChildren(...res.hits.map((h) => result(h, re)));
}

let panelsOn = false; // set from /api/config: can this server make panel scripts?

function result(h, re) {
  const li = el('li', 'hit');
  const head = el('div', 'head');
  head.append(el('b', 'title', h.title), el('span', 'code', epCode(h)));
  if (h.epTitle) head.append(el('span', 'eptitle', h.epTitle));
  head.append(el('span', 'time', fmtTime(h.start)));
  const lines = el('div', 'lines');
  let current = h.lines;
  let pick = null; // {a, b}: indexes of the first and last chosen line while choosing
  const draw = () => {
    const [lo, hi] = pick && pick.a != null ? [Math.min(pick.a, pick.b ?? pick.a), Math.max(pick.a, pick.b ?? pick.a)] : [-1, -1];
    lines.replaceChildren(...current.map((l, i) => {
      const p = el('p', `line${l.hit ? ' hit' : ''}${i >= lo && i <= hi ? ' chosen' : ''}${i === lo ? ' first' : ''}${i === hi ? ' last' : ''}`);
      p.append(el('span', 'ts', fmtTime(l.start)));
      const t = el('span', 'txt');
      markText(t, l.text, re);
      p.append(t);
      if (pick) {
        p.tabIndex = 0;
        p.setAttribute('role', 'button');
        p.addEventListener('click', () => choose(i));
        p.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); choose(i); } });
      }
      return p;
    }));
    lines.classList.toggle('picking', !!pick);
    updateBar();
  };
  const loadScene = async (before, after) => {
    const r = await fetch(`/api/context?media=${h.mediaId}&at=${h.start}&before=${before}&after=${after}`);
    if (!r.ok) throw new Error('Could not load the scene');
    current = await r.json();
  };

  const actions = el('div', 'actions');
  const more = el('button', 'more', 'More of the scene');
  more.type = 'button';
  more.addEventListener('click', async () => {
    more.disabled = true;
    try { await loadScene(12, 12); draw(); more.remove(); } catch { more.disabled = false; }
  });
  actions.append(more);

  // choosing lines for a panel script
  const bar = el('div', 'pickbar');
  bar.hidden = true;
  const barText = el('span', 'pick-text');
  const go = el('button', 'primary', 'Make panels');
  const goClip = el('button', 'primary', 'Make clip');
  const subs = el('label', 'subs');
  const subsBox = el('input'); subsBox.type = 'checkbox';
  subs.append(subsBox, ' subtitles on the clip');
  subs.title = 'Burn the subtitle lines into the clip';
  const cancel = el('button', '', 'Cancel');
  go.type = goClip.type = cancel.type = 'button';
  const btns = el('div', 'pick-btns');
  btns.append(go, goClip, subs, cancel);
  bar.append(barText, btns);
  function updateBar() {
    bar.hidden = !pick;
    if (!pick) return;
    go.hidden = goClip.hidden = subs.hidden = pick.b == null;
    if (pick.a == null) barText.textContent = 'Tap the first line of the scene';
    else if (pick.b == null) barText.textContent = 'Now tap the last line';
    else {
      const lo = Math.min(pick.a, pick.b), hi = Math.max(pick.a, pick.b);
      barText.textContent = `${hi - lo + 1} line${hi > lo ? 's' : ''} · ${fmtTime(current[lo].start)}–${fmtTime(current[hi].start)}`;
    }
  }
  function choose(i) {
    if (pick.a == null || pick.b != null) pick = { a: i, b: null };
    else pick.b = i;
    draw();
  }
  if (panelsOn) {
    const start = el('button', 'more', 'Panels or clip…');
    start.type = 'button';
    start.title = 'Pick the first and last line, then make panel images or a video clip';
    start.addEventListener('click', async () => {
      start.disabled = true;
      try { await loadScene(25, 40); more.remove(); } catch { /* keep what we have */ }
      start.disabled = false;
      pick = { a: null, b: null };
      draw();
      lines.querySelector('.line.hit')?.scrollIntoView({ block: 'center', behavior: 'smooth' });
    });
    actions.append(start);
  }
  cancel.addEventListener('click', () => { pick = null; draw(); });
  const send = async (clip) => {
    const lo = Math.min(pick.a, pick.b), hi = Math.max(pick.a, pick.b);
    go.disabled = goClip.disabled = cancel.disabled = true;
    barText.textContent = clip ? 'Cutting the scene and sending it to Video Tools…' : 'Cutting the scene and sending it to Panel Script…';
    try {
      const r = await fetch('/api/panels', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ media: h.mediaId, from: current[lo].start, to: current[hi].start, clip, subtitles: clip && subsBox.checked }) });
      const res = await r.json();
      if (!r.ok) throw new Error(res.error || 'Something went wrong');
      pick = null; draw();
      if (clip) {
        const box = clipBox(res);
        bar.after(box);
        box.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
        return;
      }
      const done = el('div', 'pickbar done');
      const a = el('a', 'primary', 'Open the panel editor');
      a.href = res.url;
      done.append(el('span', 'pick-text', `Panels for ${res.lines} line${res.lines === 1 ? '' : 's'} are being made.`), a);
      bar.after(done);
    } catch (e) {
      barText.textContent = e.message;
    } finally { go.disabled = goClip.disabled = cancel.disabled = false; }
  };
  go.addEventListener('click', () => send(false));
  goClip.addEventListener('click', () => send(true));

  draw();
  li.append(head, lines, bar, actions);
  return li;
}

// A clip being made: follow the video-tools job, then show the clip here with Download and Share.
function clipBox(res) {
  const box = el('div', 'clipbox');
  const text = el('p', 'pick-text', `Making the clip (${res.lines} line${res.lines === 1 ? '' : 's'})…`);
  const prog = el('progress'); prog.max = 100; prog.value = 0;
  box.append(text, prog);
  const fail = (msg) => {
    prog.remove();
    text.textContent = msg;
    const a = el('a', 'quiet', 'Open in Video Tools ↗');
    a.href = res.toolsUrl || res.url; a.target = '_blank'; a.rel = 'noopener';
    box.append(a);
  };
  (async () => {
    const started = Date.now();
    for (;;) {
      let job;
      try { job = await (await fetch(`/api/vt/jobs/${res.jobId}`, { cache: 'no-store' })).json(); }
      catch { if (Date.now() - started > 600_000) return fail('Lost touch with Video Tools.'); await new Promise((r) => setTimeout(r, 2000)); continue; }
      if (job.status === 'done') {
        const out = (job.outputs || []).find((o) => o.kind === 'video');
        if (!out) return fail('The clip finished but there’s no video in it.');
        return showClip(box, res.jobId, out);
      }
      if (job.status === 'failed' || job.status === 'cancelled') return fail(`The clip couldn’t be made: ${job.error || job.status}.`);
      prog.value = job.progress || 0;
      if (job.message) text.textContent = `Making the clip: ${job.message}…`;
      await new Promise((r) => setTimeout(r, 1200));
    }
  })();
  return box;
}

function showClip(box, jobId, out) {
  const src = `/api/vt/jobs/${jobId}/files/${encodeURIComponent(out.name)}`;
  const v = el('video', 'clip');
  v.controls = true; v.playsInline = true; v.preload = 'metadata'; v.src = src;
  const mb = out.size ? ` (${(out.size / 1048576).toFixed(1)} MB)` : '';
  const dl = el('a', 'primary', `Download${mb}`);
  dl.href = src; dl.download = out.name;
  const row = el('div', 'clip-actions');
  row.append(dl);
  const type = out.name.endsWith('.mkv') ? 'video/x-matroska' : 'video/mp4';
  const ios = /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  if (navigator.canShare && navigator.canShare({ files: [new File([], out.name, { type })] })) {
    // iPhones save videos to Photos only through the share sheet ("Save Video"); a download goes to Files
    const label = ios ? 'Save to Photos…' : 'Share…';
    const share = el('button', ios ? 'primary' : '', label);
    share.type = 'button';
    let file = null;
    share.addEventListener('click', async () => {
      try {
        if (!file) {
          share.disabled = true; share.textContent = 'Preparing…';
          file = new File([await (await fetch(src)).blob()], out.name, { type });
          share.disabled = false; share.textContent = label;
        }
        await navigator.share({ files: [file], title: out.name });
      } catch (e) {
        share.disabled = false;
        // iOS only shares straight after a tap; fetching the video can outlast that
        share.textContent = e.name === 'NotAllowedError' && file ? 'Tap again to save' : label;
        if (e.name !== 'AbortError' && e.name !== 'NotAllowedError') alert(`Couldn’t share: ${e.message}`);
      }
    });
    if (ios) { dl.className = ''; dl.textContent = `Download to Files${mb}`; row.prepend(share); } else row.append(share);
  }
  box.replaceChildren(v, row, el('p', 'muted clip-name', out.name));
}

let timer;
$('#q').addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(() => search(true), 250); });
$('#regex').addEventListener('change', () => search(true));
$('#title').addEventListener('change', () => search(true));
$('#form').addEventListener('submit', (e) => { e.preventDefault(); clearTimeout(timer); search(true); });

async function loadTitles() {
  const [titles, st] = await Promise.all([fetch('/api/titles').then((r) => r.json()), fetch('/api/stats').then((r) => r.json())]);
  const sel = $('#title');
  for (const [kind, label] of [['tv', 'TV shows'], ['movie', 'Movies']]) {
    const group = el('optgroup'); group.label = label;
    for (const t of titles.filter((x) => x.kind === kind && x.indexed > 0)) {
      const o = el('option', '', t.year && kind === 'movie' ? `${t.title} (${t.year})` : t.title);
      o.value = t.title;
      group.append(o);
    }
    if (group.children.length) sel.append(group);
  }
  $('#sub').textContent = `${st.indexed.toLocaleString()} of ${st.files.toLocaleString()} files searchable · ${st.lines.toLocaleString()} lines of dialogue`;
  $('#covSummary').textContent = `${st.indexed} searchable · ${st.none} without subtitles · ${st.image} with picture-only subtitles${st.errors ? ` · ${st.errors} couldn't be read` : ''}`;
  $('#covRows').replaceChildren(...titles.map((t) => {
    const tr = el('tr');
    tr.append(el('td', '', t.year && t.kind === 'movie' ? `${t.title} (${t.year})` : t.title),
      el('td', '', `${t.indexed} / ${t.files}`), el('td', '', String(t.missing + t.image)));
    return tr;
  }));
}

// restore a shared or bookmarked search
const params = new URLSearchParams(location.search);
$('#q').value = params.get('q') || '';
$('#regex').checked = params.get('regex') === '1';
const config = fetch('/api/config').then((r) => r.json()).then((c) => { panelsOn = !!c.panels; }).catch(() => {});
Promise.all([loadTitles(), config]).then(() => {
  if (params.get('t')) $('#title').value = params.get('t');
  if ($('#q').value) search(false);
});
