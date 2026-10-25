// Registry home page: cards for every app, with a live up/down dot.
(async () => {
  const reg = await (await fetch('/_gadgets/registry.json', { cache: 'no-cache' })).json();
  const url = (a) => (a.proxy ? `${location.protocol}//${location.hostname}:${a.proxy.port}${a.proxy.path || '/'}` : a.url);
  const make = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };

  for (const a of reg.apps) {
    const box = document.getElementById(a.kind);
    if (!box) continue;
    const href = url(a);
    const card = make(href ? 'a' : 'div', 'card');
    if (href) card.href = href;
    card.dataset.search = `${a.name} ${a.desc || ''} ${a.about || ''} ${reg.hosts[a.host]?.name || ''}`.toLowerCase();
    card.title = a.desc || a.name;
    const icon = make('img'); icon.src = `/_gadgets/icons/${a.id}.svg`; icon.alt = '';
    const body = make('div', 'body');
    const top = make('div', 'top');
    top.append(make('b', '', a.name));
    const status = make('span', 'status');
    const dot = make('i', 'dot');
    const label = make('span', 'label', href ? 'checking' : 'no web page');
    status.append(dot, label);
    top.append(status);
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
