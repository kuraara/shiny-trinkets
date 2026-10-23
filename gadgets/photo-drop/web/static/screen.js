import { $, h, store, codeFromPath, live, photoURL } from './common.js';

const code = codeFromPath();
const frag = new URLSearchParams(location.hash.slice(1));
let key = frag.get('d');
if (key) {
  store.set(`pd:display:${code}`, key);
  history.replaceState(null, '', location.pathname);
}
const guest = store.get(`pd:p:${code}`);
key = key || store.get(`pd:display:${code}`) || store.get(`pd:host:${code}`) || guest?.token;
const isGuest = !!guest?.token && key === guest.token;

const desk = $('#desk');
const prints = new Map(); // id -> {el, p}
const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
let first = true;
let total = 0;

function message(title, text) {
  $('#msgTitle').textContent = title;
  $('#msgText').textContent = text;
  $('#message').hidden = false;
  $('#welcome').hidden = true;
  $('#join').hidden = true;
}

if (!key) {
  message('Open the desk from the host page', 'This screen needs the link from the host controls.');
} else {
  $('#qr').src = $('#wQR').src = `/api/rooms/${code}/qr.svg`;
  $('#code').textContent = code;
  if (isGuest) { const b = $('#back'); b.hidden = false; b.href = `/j/${code}`; }
  live(code, key, {
    onState: render,
    onClosed: () => { desk.replaceChildren(); prints.clear(); message('That’s a wrap', 'This session has ended.'); },
    onStatus: (s) => { $('#conn').hidden = s === 'ok'; },
  });
}

// How big a print is (its longest side), shrinking as the desk fills up.
function printSize(n) {
  const m = Math.min(innerWidth, innerHeight);
  const f = n <= 2 ? 0.52 : n <= 6 ? 0.44 : n <= 14 ? 0.36 : n <= 30 ? 0.3 : n <= 60 ? 0.25 : 0.21;
  return m * f;
}

function geometry(p, n) {
  const long = printSize(n);
  const r = p.w / p.h;
  const iw = r >= 1 ? long : long * r;
  const ih = r >= 1 ? long / r : long;
  const pad = Math.max(5, Math.round(long * 0.04));
  const label = p.caption || p.name ? Math.round(long * 0.1) : 0;
  const ow = iw + pad * 2;
  const oh = ih + pad * 2 + label;
  const W = innerWidth, H = innerHeight;
  return { ow, oh, pad, label, left: p.x * Math.max(0, W - ow), top: p.y * Math.max(0, H - oh), font: Math.max(11, long * 0.045) };
}

function place(el, p, n) {
  const g = geometry(p, n);
  Object.assign(el.style, {
    width: `${g.ow}px`, height: `${g.oh}px`, left: `${g.left}px`, top: `${g.top}px`,
    padding: `${g.pad}px ${g.pad}px ${g.pad + g.label}px`, transform: `rotate(${p.rot}deg)`, zIndex: p.z,
  });
  const cap = el.querySelector('figcaption');
  cap.style.height = `${g.label + g.pad}px`;
  cap.style.fontSize = `${g.font}px`;
  return g;
}

function captionText(p) {
  return [p.caption ? `“${p.caption}”` : '', p.name].filter(Boolean).join(' — ');
}

function makePrint(p) {
  const img = h('img', { src: photoURL(code, p.id), alt: captionText(p) || 'Photo', decoding: 'async' });
  const el = h('figure', { class: 'print' }, img, h('figcaption', { text: captionText(p) }));
  el.addEventListener('click', () => openLightbox(p));
  return el;
}

// The throw: in from below the screen edge, spinning, landing with a small settle.
function toss(el, p, g) {
  if (reduceMotion) return;
  const a = (20 + Math.random() * 140) * Math.PI / 180;
  const d = Math.max(innerWidth, innerHeight) * 0.75;
  const dx = Math.cos(a) * d, dy = Math.sin(a) * d;
  const spin = (Math.random() < 0.5 ? -1 : 1) * (25 + Math.random() * 35);
  el.animate([
    { transform: `translate(${dx}px, ${dy}px) rotate(${p.rot + spin}deg) scale(1.3)`, boxShadow: '0 40px 80px rgba(0,0,0,.35)' },
    { transform: `translate(0, 0) rotate(${p.rot - spin * 0.04}deg) scale(0.97)`, offset: 0.78, boxShadow: '0 4px 10px rgba(0,0,0,.3)' },
    { transform: `translate(0, 0) rotate(${p.rot}deg) scale(1)` },
  ], { duration: 1100, easing: 'cubic-bezier(.16,.84,.3,1)' });
  void g;
}

function render(s) {
  $('#title').textContent = s.title;
  $('#wTitle').textContent = s.title;
  $('#wURL').textContent = s.joinUrl;
  document.title = s.title;
  const showQR = s.settings.showQR && !isGuest;
  const n = s.desk.length;
  total = n;
  $('#welcome').hidden = n > 0 || !showQR;
  $('#join').hidden = n === 0 || !showQR;
  $('#count').textContent = `${s.counts.desk} photo${s.counts.desk === 1 ? '' : 's'} · ${s.counts.people} here`;

  const keep = new Set(s.desk.map((p) => p.id));
  for (const [id, item] of prints) {
    if (keep.has(id)) continue;
    prints.delete(id);
    const anim = reduceMotion ? null : item.el.animate([{ opacity: 1 }, { opacity: 0, transform: `rotate(${item.p.rot}deg) scale(.85)` }], { duration: 400 });
    if (anim) anim.finished.then(() => item.el.remove()); else item.el.remove();
  }
  for (const p of s.desk) {
    const known = prints.get(p.id);
    if (known) { known.p = p; place(known.el, p, n); continue; }
    const el = makePrint(p);
    prints.set(p.id, { el, p });
    if (first) { place(el, p, n); desk.append(el); continue; }
    // wait for the image so it doesn't fly in blank
    const img = el.querySelector('img');
    const go = () => { if (!prints.has(p.id)) return; const g = place(el, p, total); desk.append(el); toss(el, p, g); };
    Promise.race([img.decode(), new Promise((r) => setTimeout(r, 4000))]).then(go, go);
  }
  first = false;
}

addEventListener('resize', () => { for (const { el, p } of prints.values()) place(el, p, total); });

// lightbox
function openLightbox(p) {
  $('#lbImg').src = photoURL(code, p.id, 'full');
  $('#lbCap').textContent = captionText(p);
  $('#lightbox').hidden = false;
}
$('#lightbox').addEventListener('click', () => { $('#lightbox').hidden = true; });

// full screen and idle cursor
const toggleFS = () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen?.())?.catch?.(() => {});
$('#fs').addEventListener('click', toggleFS);
addEventListener('keydown', (e) => {
  if (e.key === 'f' || e.key === 'F') toggleFS();
  if (e.key === 'Escape') $('#lightbox').hidden = true;
});
let idleTimer;
const wake = () => { document.body.classList.remove('idle'); clearTimeout(idleTimer); idleTimer = setTimeout(() => document.body.classList.add('idle'), 3000); };
addEventListener('mousemove', wake);
addEventListener('touchstart', wake, { passive: true });
wake();
