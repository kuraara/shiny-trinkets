import { $, h, api, store } from './common.js';

api('/api/config').then((c) => { $('#pinField').hidden = !c.pinRequired; }).catch(() => {});

$('#start').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = e.target;
  const btn = f.querySelector('button');
  btn.disabled = true;
  $('#startErr').textContent = '';
  try {
    const r = await api('/api/rooms', {
      method: 'POST',
      body: {
        title: f.title.value, pin: f.pin.value,
        requireNames: f.requireNames.checked, approveParticipants: f.approveParticipants.checked,
      },
    });
    store.set(`pd:host:${r.code}`, r.hostKey);
    location.href = `/h/${r.code}`;
  } catch (err) {
    $('#startErr').textContent = err.message;
    btn.disabled = false;
  }
});

$('#join').addEventListener('submit', (e) => {
  e.preventDefault();
  const code = e.target.code.value.trim().toUpperCase().replace(/[^A-Z0-9]/g, '');
  if (code) location.href = `/j/${code}`;
});

// Sessions this browser is hosting; forget the ones that have ended.
(async () => {
  const list = $('#mineList');
  for (const k of store.keys('pd:host:')) {
    const code = k.slice('pd:host:'.length);
    try {
      const info = await api(`/api/rooms/${code}`);
      list.append(h('li', {},
        h('div', { class: 'who' }, h('b', { text: info.title }), h('small', { text: `Code ${code}` })),
        h('a', { class: 'btn small', href: `/h/${code}` }, 'Host'),
      ));
      $('#mine').hidden = false;
    } catch (err) {
      if (err.status === 404) store.del(k);
    }
  }
})();
