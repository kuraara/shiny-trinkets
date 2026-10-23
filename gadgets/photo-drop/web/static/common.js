// Shared helpers for every Photo Drop page.

export const $ = (sel, root = document) => root.querySelector(sel);

// h('div', {class: 'x', onclick: fn, text: 'hi'}, child, ...) builds an element.
// Text always goes in as text, never as HTML.
export function h(tag, props = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'text') el.textContent = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

export const store = {
  get(k) { try { return JSON.parse(localStorage.getItem(k)); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch { /* private mode */ } },
  del(k) { try { localStorage.removeItem(k); } catch { /* ignore */ } },
  keys(prefix) {
    try { return Object.keys(localStorage).filter((k) => k.startsWith(prefix)); } catch { return []; }
  },
};

export async function api(path, { method = 'GET', body, key, signal } = {}) {
  const headers = {};
  if (key) headers['X-Key'] = key;
  let payload;
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  const res = await fetch(path, { method, headers, body: payload, signal, cache: 'no-store' });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `Something went wrong (${res.status}).`);
    err.status = res.status;
    throw err;
  }
  return data;
}

export function codeFromPath() {
  const m = location.pathname.match(/^\/[jhs]\/([A-Za-z0-9]+)/);
  return m ? m[1].toUpperCase() : '';
}

export const photoURL = (code, id, size = 'm') => `/api/rooms/${code}/photos/${id}${size === 'm' ? '_m' : ''}.jpg`;

// live keeps a page up to date: Server-Sent Events, falling back to polling when a proxy
// or network won't carry the stream.
export function live(code, key, { onState, onClosed, onStatus = () => {} }) {
  let es = null, timer = null, watchdog = null, stopped = false, last = -1;
  const handle = (snap) => {
    if (snap.version === last) return;
    last = snap.version;
    onState(snap);
  };
  const closed = () => { if (!stopped) { stopped = true; es?.close(); clearTimeout(timer); onClosed(); } };
  const poll = async () => {
    if (stopped) return;
    try {
      handle(await api(`/api/rooms/${code}/state`, { key }));
      onStatus('ok');
    } catch (e) {
      if (e.status === 404 || e.status === 410) return closed();
      onStatus('offline');
    }
    timer = setTimeout(poll, 4000);
  };
  const toPolling = () => { es?.close(); es = null; clearTimeout(watchdog); poll(); };
  if (!('EventSource' in window)) { poll(); }
  else {
    es = new EventSource(`/api/rooms/${code}/events?k=${encodeURIComponent(key)}`);
    watchdog = setTimeout(toPolling, 10000); // nothing arrived: something is buffering the stream
    es.addEventListener('state', (ev) => {
      clearTimeout(watchdog);
      onStatus('ok');
      handle(JSON.parse(ev.data));
    });
    es.addEventListener('closed', closed);
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) toPolling(); // e.g. 404/403: check by polling
      else onStatus('offline');
    };
  }
  return { stop() { stopped = true; es?.close(); clearTimeout(timer); clearTimeout(watchdog); } };
}

// reconcile keeps container's children in step with items, reusing elements by key so
// images don't reload and flicker on every update.
export function reconcile(container, items, keyOf, create, update = () => {}) {
  const old = new Map();
  for (const el of container.children) if (el.dataset.key) old.set(el.dataset.key, el);
  let prev = null;
  for (const item of items) {
    const k = keyOf(item);
    let el = old.get(k);
    if (el) old.delete(k);
    else { el = create(item); el.dataset.key = k; }
    update(el, item);
    const want = prev ? prev.nextSibling : container.firstChild;
    if (el !== want) container.insertBefore(el, want);
    prev = el;
  }
  for (const el of old.values()) el.remove();
}

// shrinkPhoto turns a picked photo into an upright JPEG no bigger than 2048 px.
// Drawing it through a canvas also strips the location and camera metadata.
export async function shrinkPhoto(file, max = 2048) {
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    const s = Math.min(1, max / Math.max(img.naturalWidth, img.naturalHeight));
    const w = Math.max(1, Math.round(img.naturalWidth * s));
    const ht = Math.max(1, Math.round(img.naturalHeight * s));
    const c = document.createElement('canvas');
    c.width = w; c.height = ht;
    const ctx = c.getContext('2d');
    ctx.fillStyle = '#fff';
    ctx.fillRect(0, 0, w, ht);
    ctx.drawImage(img, 0, 0, w, ht);
    const blob = await new Promise((res) => c.toBlob(res, 'image/jpeg', 0.86));
    return blob || file;
  } catch {
    return file; // let the server try (and explain if it can't)
  } finally {
    URL.revokeObjectURL(url);
  }
}

// sendPhoto uploads with progress (fetch can't report upload progress).
export function sendPhoto(code, key, blob, caption, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/rooms/${code}/photos`);
    xhr.setRequestHeader('X-Key', key);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      let data = null;
      try { data = JSON.parse(xhr.responseText); } catch { /* ignore */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else reject(new Error((data && data.error) || `Upload failed (${xhr.status}).`));
    };
    xhr.onerror = () => reject(new Error('Upload failed: check your connection.'));
    const form = new FormData();
    form.append('photo', blob, 'photo.jpg');
    form.append('caption', caption || '');
    xhr.send(form);
  });
}

export function toast(msg, kind = '') {
  let box = $('#toasts');
  if (!box) { box = h('div', { id: 'toasts', 'aria-live': 'polite' }); document.body.append(box); }
  const t = h('div', { class: `toast ${kind}`, text: msg });
  box.append(t);
  setTimeout(() => t.remove(), 4500);
}

// twoStep makes a destructive button ask "sure?" on the first press.
export function twoStep(btn, label, run) {
  let armed = null;
  const orig = btn.textContent;
  btn.addEventListener('click', () => {
    if (!armed) {
      btn.textContent = label;
      btn.classList.add('armed');
      armed = setTimeout(() => { armed = null; btn.textContent = orig; btn.classList.remove('armed'); }, 4000);
      return;
    }
    clearTimeout(armed); armed = null;
    btn.textContent = orig; btn.classList.remove('armed');
    run();
  });
}
