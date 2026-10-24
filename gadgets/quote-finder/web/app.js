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

function result(h, re) {
  const li = el('li', 'hit');
  const head = el('div', 'head');
  head.append(el('b', 'title', h.title), el('span', 'code', epCode(h)));
  if (h.epTitle) head.append(el('span', 'eptitle', h.epTitle));
  head.append(el('span', 'time', fmtTime(h.start)));
  const lines = el('div', 'lines');
  const draw = (ls) => lines.replaceChildren(...ls.map((l) => {
    const p = el('p', l.hit ? 'line hit' : 'line');
    p.append(el('span', 'ts', fmtTime(l.start)));
    const t = el('span', 'txt');
    markText(t, l.text, re);
    p.append(t);
    return p;
  }));
  draw(h.lines);
  const more = el('button', 'more', 'More of the scene');
  more.type = 'button';
  more.addEventListener('click', async () => {
    more.disabled = true;
    try {
      const r = await fetch(`/api/context?media=${h.mediaId}&at=${h.start}`);
      draw(await r.json());
      more.remove();
    } catch { more.disabled = false; }
  });
  li.append(head, lines, more);
  return li;
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
loadTitles().then(() => {
  if (params.get('t')) $('#title').value = params.get('t');
  if ($('#q').value) search(false);
});
