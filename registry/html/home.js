// Registry home page: cards for every app, with a live up/down dot.
(async () => {
  const reg = await (await fetch('/_gadgets/registry.json', { cache: 'no-cache' })).json();
  // a gadget answers http, https or both on its port: keep this page's protocol when it can
  const scheme = (a) => {
    const ps = a.proxy.protocols || reg.protocols || ['http', 'https'];
    const here = location.protocol.replace(':', '');
    return ps.includes(here) ? here : ps[0];
  };
  // {pc}-style placeholders left in a URL (a host with no address in site.local.json): this host
  const url = (a) => (a.proxy ? `${scheme(a)}://${location.hostname}:${a.proxy.port}${a.proxy.path || '/'}`
    : a.url && a.url.replace(/\{[a-z][a-z0-9-]*\}/g, location.hostname));
  const make = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };

  for (const a of reg.apps) {
    const box = document.getElementById(a.kind);
    if (!box) continue;
    const href = url(a);
    const acts = a.actions || [];
    // a card with buttons can't itself be a link (buttons inside a link misbehave): its name links instead
    const card = make(href && !acts.length ? 'a' : 'div', 'card');
    if (href && !acts.length) card.href = href;
    card.dataset.search = `${a.name} ${a.desc || ''} ${a.about || ''} ${reg.hosts[a.host]?.name || ''}`.toLowerCase();
    card.title = a.desc || a.name;
    const icon = make('img'); icon.src = `/_gadgets/icons/${a.id}.svg`; icon.alt = '';
    const body = make('div', 'body');
    const top = make('div', 'top');
    if (href && acts.length) { const n = make('a', 'name', a.name); n.href = href; top.append(n); }
    else top.append(make('b', '', a.name));
    const status = make('span', 'status');
    const dot = make('i', 'dot');
    const label = make('span', 'label', href ? 'checking' : 'no web page');
    status.append(dot, label);
    top.append(status);
    if (acts.length) {
      const box = make('span', 'actions');
      for (const x of acts) box.append(actionButton(a, x));
      top.append(box);
    }
    body.append(top);
    if (a.desc) body.append(make('p', 'desc', a.desc));
    if (a.about) body.append(make('p', 'about', a.about));
    const meta = make('div', 'meta');
    meta.append(make('span', 'chip', reg.hosts[a.host]?.name || a.host));
    if (href) meta.append(make('span', 'addr', a.proxy ? `:${a.proxy.port}${a.proxy.path || ''}` : href.replace(/^https?:\/\//, '').replace(/\/$/, '')));
    body.append(meta);
    card.append(icon, body);
    box.append(card);
    if (href) check(a.id, dot, label);
  }

  // A card action: POST /_gadgets/action/<app>/<action>; the reply's message (or error) shows
  // under the button for a few seconds. confirm: the first tap only arms it.
  function actionButton(app, x) {
    const wrap = make('span', 'act');
    const btn = make('button', 'act-btn', x.label);
    btn.type = 'button';
    btn.title = x.title || x.label;
    const msg = make('span', 'act-msg');
    msg.setAttribute('role', 'status');
    let armed = null, clear = null;
    const say = (text, bad) => {
      msg.textContent = text; msg.classList.toggle('bad', !!bad);
      clearTimeout(clear); clear = setTimeout(() => { msg.textContent = ''; }, 5000);
    };
    btn.addEventListener('click', async (e) => {
      e.preventDefault(); e.stopPropagation();
      if (x.confirm && !armed) {
        btn.textContent = 'Tap again'; btn.classList.add('armed');
        armed = setTimeout(() => { armed = null; btn.textContent = x.label; btn.classList.remove('armed'); }, 3000);
        return;
      }
      clearTimeout(armed); armed = null; btn.classList.remove('armed');
      btn.disabled = true; btn.textContent = '…';
      try {
        const r = await fetch(`/_gadgets/action/${app.id}/${x.id}`, { method: 'POST', headers: { 'X-Gadget-Action': '1' }, cache: 'no-store' });
        const j = await r.json().catch(() => ({}));
        if (r.ok) say(j.message || 'Done');
        else say(j.error || j.message || `Didn't work (${r.status})`, true);
      } catch {
        say("Couldn't reach it", true);
      } finally {
        btn.disabled = false; btn.textContent = x.label;
      }
    });
    wrap.append(btn, msg);
    return wrap;
  }

  async function check(id, dot, label) {
    const ctl = new AbortController();
    const t = setTimeout(() => ctl.abort(), 7000);
    let up = false;
    try {
      const r = await fetch(`/_gadgets/status/${id}`, { method: 'HEAD', cache: 'no-store', redirect: 'manual', signal: ctl.signal });
      up = r.type === 'opaqueredirect' || (r.status > 0 && r.status !== 502 && r.status !== 504);
    } catch { up = false; }
    clearTimeout(t);
    dot.className = `dot ${up ? 'up' : 'down'}`;
    label.textContent = up ? 'up' : 'down';
  }

  // Tiles (compact) or cards (with what each one does); remembered per browser.
  const views = document.querySelectorAll('input[name=view]');
  const setView = (v) => {
    document.body.dataset.view = v;
    for (const r of views) r.checked = r.value === v;
    try { localStorage.setItem('gadgets-view', v); } catch { /* private mode */ }
  };
  let saved = null;
  try { saved = localStorage.getItem('gadgets-view'); } catch { /* ignore */ }
  setView(saved === 'tiles' ? 'tiles' : 'cards');
  for (const r of views) r.addEventListener('change', () => setView(r.value));

  document.getElementById('q').addEventListener('input', (e) => {
    const q = e.target.value.trim().toLowerCase();
    document.querySelectorAll('.card').forEach((c) => { c.hidden = q && !c.dataset.search.includes(q); });
  });
})();
