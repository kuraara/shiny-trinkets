import { $, h, api, store, codeFromPath, live, reconcile, photoURL, shrinkPhoto, sendPhoto } from './common.js';

const code = codeFromPath();
const meKey = `pd:p:${code}`;
let me = store.get(meKey); // {token, id}
let info = null;
let conn = null;
const picked = []; // {file, url, caption, el}

const views = ['vMissing', 'vForm', 'vWaiting', 'vOut', 'vMain'];
const show = (id) => views.forEach((v) => { $(`#${v}`).hidden = v !== id; });

async function start() {
  try {
    info = await api(`/api/rooms/${code}`);
  } catch {
    $('#sub').textContent = '';
    return show('vMissing');
  }
  $('#title').textContent = info.title;
  document.title = info.title;
  $('#sub').textContent = 'Share your photos on the big screen';
  if (me?.token) connect();
  else showForm();
}

function showForm() {
  const input = $('#vForm').name;
  input.required = info.requireNames;
  input.placeholder = info.requireNames ? 'First and last name' : 'Leave blank to stay anonymous';
  $('#nameLabel').firstChild.textContent = info.requireNames ? 'Your name ' : 'Your name (optional) ';
  show('vForm');
}

$('#vForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const btn = e.target.querySelector('button');
  btn.disabled = true;
  $('#formErr').textContent = '';
  try {
    const r = await api(`/api/rooms/${code}/join`, { method: 'POST', body: { name: e.target.name.value } });
    me = { token: r.token, id: r.id };
    store.set(meKey, me);
    connect();
  } catch (err) {
    $('#formErr').textContent = err.message;
  } finally {
    btn.disabled = false;
  }
});

function connect() {
  conn?.stop();
  conn = live(code, me.token, {
    onState: render,
    onClosed: () => { out('This session has ended', 'Thanks for joining in!'); },
  });
}

function out(title, text) {
  $('#outTitle').textContent = title;
  $('#outText').textContent = text;
  show('vOut');
}

const statusText = { pending: 'Waiting for approval', approved: 'On the desk ✓', rejected: 'Not approved', removed: 'Taken off the desk' };

function render(s) {
  s.mine ||= [];
  if (!s.me) { store.del(meKey); me = null; return showForm(); }
  switch (s.me.status) {
    case 'waiting': return show('vWaiting');
    case 'denied': return out("The host didn't let you in", 'Ask the host if you think this is a mistake.');
    case 'kicked': return out('The host removed you from this session', '');
  }
  if ($('#vMain').hidden) {
    $('#hello').textContent = s.me.anonymous ? 'Add your photos to the desk.' : `Hi ${s.me.name}! Add your photos to the desk.`;
    show('vMain');
  }
  $('#mineCard').hidden = s.mine.length === 0;
  reconcile($('#mine'), [...s.mine].reverse(), (p) => p.id, (p) => h('figure', {},
    h('div', { class: 'ph' }, h('img', { src: photoURL(code, p.id), alt: '' })),
    h('figcaption', {}),
  ), (el, p) => {
    const cap = el.querySelector('figcaption');
    cap.className = p.status;
    cap.textContent = statusText[p.status] || p.status;
    if (p.status === 'rejected' || p.status === 'removed') {
      const ph = el.querySelector('.ph');
      if (ph.querySelector('img')) ph.replaceChildren('—'); // file is gone
    }
  });
}

$('#deskLink').addEventListener('click', (e) => { e.preventDefault(); location.href = `/s/${code}`; });

// ---- picking and sending ----

$('#pick').addEventListener('change', (e) => {
  for (const file of e.target.files) {
    const item = { file, url: URL.createObjectURL(file), caption: '' };
    const cap = h('input', { type: 'text', maxlength: 80, placeholder: 'Caption (optional)', oninput: (ev) => { item.caption = ev.target.value; } });
    item.el = h('div', { class: 'pick' },
      h('img', { src: item.url, alt: '' }),
      cap,
      h('button', { class: 'small', 'aria-label': 'Remove', onclick: () => drop(item) }, '✕'),
      h('div', { class: 'bar' }, h('i')),
    );
    picked.push(item);
    $('#picked').append(item.el);
  }
  e.target.value = '';
  updateSend();
});

function drop(item) {
  picked.splice(picked.indexOf(item), 1);
  item.el.remove();
  URL.revokeObjectURL(item.url);
  updateSend();
}

function updateSend() {
  const n = picked.length;
  $('#send').hidden = n === 0;
  $('#send').textContent = n === 1 ? 'Send photo' : `Send ${n} photos`;
}

$('#send').addEventListener('click', async () => {
  const btn = $('#send');
  btn.disabled = true;
  for (const item of [...picked]) {
    const bar = item.el.querySelector('.bar i');
    item.el.querySelectorAll('input,button').forEach((x) => { x.disabled = true; });
    try {
      bar.style.width = '5%';
      const blob = await shrinkPhoto(item.file);
      await sendPhoto(code, me.token, blob, item.caption, (f) => { bar.style.width = `${5 + f * 95}%`; });
      drop(item);
    } catch (err) {
      item.el.querySelector('.err')?.remove();
      item.el.append(h('div', { class: 'err', text: err.message }));
      item.el.querySelectorAll('input,button').forEach((x) => { x.disabled = false; });
      bar.style.width = '0';
      break; // limits apply to the rest too
    }
  }
  btn.disabled = false;
  updateSend();
});

start();
