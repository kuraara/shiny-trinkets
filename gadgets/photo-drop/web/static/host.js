import { $, h, api, store, codeFromPath, live, reconcile, photoURL, shrinkPhoto, sendPhoto, toast, twoStep } from './common.js';

const code = codeFromPath();
const key = store.get(`pd:host:${code}`);
const base = `/api/rooms/${code}`;
let snap = null;

if (!key) {
  $('#nokey').hidden = false;
} else {
  $('#app').hidden = false;
  $('#code').textContent = code;
  $('#qr').src = `${base}/qr.svg`;
  $('#download').href = `${base}/export.zip?k=${encodeURIComponent(key)}`;
  live(code, key, {
    onState: render,
    onClosed: () => { $('#app').hidden = true; $('#ended').hidden = false; store.del(`pd:host:${code}`); },
    onStatus: (s) => { $('#dot').classList.toggle('offline', s !== 'ok'); $('#dot').title = s === 'ok' ? 'Live' : 'Reconnecting…'; },
  });
}

const act = (path, body) => api(`${base}${path}`, { method: 'POST', key, body }).catch((e) => toast(e.message, 'bad'));

function render(s) {
  s.pending ||= [];
  s.people ||= [];
  snap = s;
  const title = $('#title');
  if (document.activeElement !== title) title.value = s.title;
  document.title = `${s.counts.pending + s.counts.waiting ? `(${s.counts.pending + s.counts.waiting}) ` : ''}${s.title} · host`;
  $('#joinUrl').textContent = s.joinUrl;
  $('#openDisplay').href = `/s/${code}#d=${encodeURIComponent(s.displayKey)}`;
  for (const k of ['requireNames', 'approveParticipants', 'showQR']) $(`#${k}`).checked = s.settings[k];
  for (const [k, def] of [['autoArrange', '0'], ['autoStyle', 'shuffle']]) {
    const sel = $(`#${k}`), v = String(s.settings[k] || def);
    if (document.activeElement === sel) continue;
    if (![...sel.options].some((o) => o.value === v)) sel.append(h('option', { value: v, text: `Every ${v} min` }));
    sel.value = v;
  }
  $('#autoStyle').disabled = !s.settings.autoArrange;

  // Photos waiting for approval
  $('#pendingCount').textContent = s.pending.length;
  $('#pendingEmpty').hidden = s.pending.length > 0;
  $('#approveAll').hidden = s.pending.length < 2;
  reconcile($('#pending'), s.pending, (p) => p.id, (p) => h('div', { class: 'pcard' },
    h('div', { class: 'img' }, h('img', { src: photoURL(code, p.id), alt: '', loading: 'lazy' })),
    h('div', { class: 'meta' }, h('b', { text: p.name }), p.caption ? h('span', { class: 'muted', text: `“${p.caption}”` }) : null),
    h('div', { class: 'acts' },
      h('button', { class: 'good', onclick: (e) => { e.target.closest('.pcard').style.opacity = .4; act(`/photos/${p.id}/approve`); } }, '✓ Approve'),
      h('button', { class: 'bad', onclick: (e) => { e.target.closest('.pcard').style.opacity = .4; act(`/photos/${p.id}/reject`); } }, '✕ Reject'),
    ),
  ));

  // Lobby
  const waiting = s.people.filter((p) => p.status === 'waiting');
  $('#lobbyCard').hidden = waiting.length === 0;
  $('#lobbyCount').textContent = waiting.length;
  reconcile($('#lobby'), waiting, (p) => p.id, (p) => h('li', {},
    h('div', { class: 'who' }, h('b', { text: p.name }), h('small', { text: p.anonymous ? 'No name given' : 'Waiting' })),
    h('button', { class: 'small good', onclick: () => act(`/people/${p.id}/admit`) }, 'Let in'),
    h('button', { class: 'small bad', onclick: () => act(`/people/${p.id}/deny`) }, 'Turn away'),
  ));

  // Desk (newest first)
  const desk = [...s.desk].reverse();
  $('#deskCount').textContent = s.counts.desk;
  $('#deskEmpty').hidden = desk.length > 0;
  $('#clearDesk').hidden = desk.length === 0;
  $('#tidyDesk').hidden = $('#shuffleDesk').hidden = desk.length < 2;
  $('#download').hidden = desk.length === 0;
  reconcile($('#desk'), desk, (p) => p.id, (p) => h('div', { class: 'thumb', title: [p.name, p.caption].filter(Boolean).join(' · ') },
    h('img', { src: photoURL(code, p.id), alt: '', loading: 'lazy' }),
    h('button', { 'aria-label': 'Take off the desk', title: 'Take off the desk', onclick: () => act(`/photos/${p.id}/remove`) }, '×'),
  ));

  // Guests
  const others = s.people.filter((p) => p.status !== 'waiting');
  $('#peopleCount').textContent = s.counts.people;
  $('#peopleEmpty').hidden = others.length > 0;
  reconcile($('#people'), others, (p) => `${p.id}:${p.status}`, (p) => {
    const out = p.status !== 'admitted';
    return h('li', { class: out ? 'out' : '' },
      h('div', { class: 'who' }, h('b', { text: p.name }),
        h('small', { text: out ? (p.status === 'kicked' ? 'Removed' : 'Turned away') : `${p.onDesk} on the desk · ${p.sent} sent` })),
      out ? h('button', { class: 'small', onclick: () => act(`/people/${p.id}/admit`) }, 'Let back in')
          : h('button', { class: 'small bad', title: 'Remove this guest and their waiting photos', onclick: () => act(`/people/${p.id}/kick`) }, 'Remove'),
    );
  }, (el, p) => {
    if (p.status === 'admitted') el.querySelector('small').textContent = `${p.onDesk} on the desk · ${p.sent} sent`;
  });
}

$('#approveAll').addEventListener('click', () => act('/approve-all'));
$('#admitAll').addEventListener('click', () => act('/admit-all'));
twoStep($('#clearDesk'), 'Sure? Tap again', () => act('/clear'));
twoStep($('#end'), 'Deletes everything. Tap again', async () => {
  try { await api(base, { method: 'DELETE', key }); } catch (e) { toast(e.message, 'bad'); }
});

for (const k of ['requireNames', 'approveParticipants', 'showQR']) {
  $(`#${k}`).addEventListener('change', (e) => act('/settings', { [k]: e.target.checked }));
}
$('#autoArrange').addEventListener('change', (e) => act('/settings', { autoArrange: Number(e.target.value) }));
$('#autoStyle').addEventListener('change', (e) => act('/settings', { autoStyle: e.target.value }));
$('#tidyDesk').addEventListener('click', () => act('/arrange', { style: 'tidy' }));
$('#shuffleDesk').addEventListener('click', () => act('/arrange', { style: 'shuffle' }));
$('#title').addEventListener('change', (e) => act('/settings', { title: e.target.value }));
$('#title').addEventListener('keydown', (e) => { if (e.key === 'Enter') e.target.blur(); });

$('#copy').addEventListener('click', async () => {
  const url = snap?.joinUrl;
  if (!url) return;
  try { await navigator.clipboard.writeText(url); toast('Link copied'); }
  catch { prompt('Copy this link:', url); }
});

// The host can add photos too; they skip the queue.
$('#hostPick').addEventListener('change', async (e) => {
  const files = [...e.target.files];
  e.target.value = '';
  for (const f of files) {
    try { await sendPhoto(code, key, await shrinkPhoto(f), ''); }
    catch (err) { toast(err.message, 'bad'); }
  }
});
