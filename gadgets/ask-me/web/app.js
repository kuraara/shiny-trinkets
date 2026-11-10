// Ask Me: lists open questions and sends answers. Refreshes every 5 s without disturbing a
// half-typed answer (cards are kept by id; only new or finished ones are added or removed).
const $ = (id) => document.getElementById(id);
const cards = new Map(); // id -> element

function ago(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso)) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
}
function el(tag, cls, text) { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }

async function send(q, card, answer) {
  const err = card.querySelector('.err');
  err.textContent = '';
  card.querySelectorAll('button, textarea').forEach((b) => (b.disabled = true));
  const url = `api/questions/${q.id}/${answer == null ? 'dismiss' : 'answer'}`;
  try {
    const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ answer }) });
    const j = await r.json().catch(() => ({}));
    if (!r.ok && r.status !== 409) throw new Error(j.error || `The page couldn't send that (${r.status}).`);
    refresh();
  } catch (e) {
    err.textContent = e.message + ' Try again.';
    card.querySelectorAll('button, textarea').forEach((b) => (b.disabled = false));
  }
}

function card(q) {
  const c = el('article', 'q');
  const meta = el('div', 'meta');
  if (q.from) meta.append(el('span', 'from', q.from));
  const when = el('span', 'when', ago(q.asked)); when.dataset.asked = q.asked; meta.append(when);
  c.append(meta, el('p', 'text', q.question));
  if (q.context) { const d = el('details'); d.append(el('summary', null, 'What the agent is doing'), el('p', null, q.context)); c.append(d); }
  if (q.options && q.options.length) {
    const o = el('div', 'opts');
    q.options.forEach((t) => { const b = el('button', 'opt', t); b.type = 'button'; b.addEventListener('click', () => send(q, c, t)); o.append(b); });
    c.append(o);
  }
  const f = el('form', 'reply');
  const ta = el('textarea'); ta.id = `answer-${q.id}`; ta.placeholder = q.options && q.options.length ? 'Or type your own answer' : 'Your answer';
  ta.setAttribute('aria-label', 'Your answer');
  const row = el('div', 'row');
  const sb = el('button', 'send', 'Send answer'); sb.type = 'submit';
  const db = el('button', 'dismiss', 'Dismiss'); db.type = 'button'; db.title = "Take it off the page without answering; the agent is told it's withdrawn";
  db.addEventListener('click', () => send(q, c, null));
  row.append(sb, db);
  f.append(ta, row, el('p', 'err'));
  f.addEventListener('submit', (e) => { e.preventDefault(); const t = ta.value.trim(); if (!t) { f.querySelector('.err').textContent = 'Type an answer, or tap one of the suggestions.'; return; } send(q, c, t); });
  c.append(f);
  return c;
}

async function refresh() {
  try {
    const r = await fetch('api/questions', { cache: 'no-store' });
    const { questions } = await r.json();
    const open = questions.filter((q) => q.status === 'open');
    const done = questions.filter((q) => q.status !== 'open').slice(0, 30);
    const box = $('open');
    for (const [id, c] of cards) if (!open.some((q) => q.id === id)) { c.remove(); cards.delete(id); }
    open.slice().reverse().forEach((q) => { if (!cards.has(q.id)) { const c = card(q); cards.set(q.id, c); box.prepend(c); } });
    box.querySelectorAll('.when').forEach((w) => (w.textContent = ago(w.dataset.asked)));
    $('count').textContent = open.length || '';
    $('none').hidden = open.length > 0;
    document.title = open.length ? `(${open.length}) Ask Me` : 'Ask Me';
    const log = $('done'); log.replaceChildren();
    done.forEach((q) => {
      const li = el('li');
      const m = el('div', 'meta'); if (q.from) m.append(el('span', 'from', q.from)); m.append(el('span', null, ago(q.answeredAt || q.asked)));
      const a = q.status === 'answered' ? el('div', 'la', q.answer) : el('div', 'la withdrawn', 'Withdrawn, no answer');
      li.append(m, el('div', 'lq', q.question), a); log.append(li);
    });
    $('nodone').hidden = done.length > 0;
    $('status').textContent = '';
  } catch {
    $('status').textContent = "Can't reach the server right now; retrying.";
  }
}
refresh();
setInterval(refresh, 5000);
