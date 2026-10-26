// Gadget navbar. nginx adds <script src="/_gadgets/nav.js"> to every proxied gadget's pages.
// A page can opt out with <meta name="gadget-nav" content="off">, or ask for the small
// floating button instead of a bar with content="fab".
//
// It also gives every gadget page window.gadgets, for linking to other gadgets without knowing
// their ports or protocols:   const url = await gadgets.url('video-tools', '/jobs/j_123');
(() => {
  if (window.__gadgetNav) return;
  window.__gadgetNav = true;
  const BASE = '/_gadgets/';
  const BAR_H = 44;

  // A gadget answers http, https or both (on the same port). Links keep this page's protocol
  // when the target supports it.
  const schemeFor = (reg, app) => {
    const ps = (app && app.proxy && app.proxy.protocols) || reg.protocols || ['http', 'https'];
    const here = location.protocol.replace(':', '');
    return ps.includes(here) ? here : ps[0];
  };
  const homeURL = (reg) => {
    const s = schemeFor(reg, null);
    const port = (reg.home || {})[s];
    const dflt = s === 'https' ? 443 : 80;
    return `${s}://${location.hostname}${port && port !== dflt ? `:${port}` : ''}/`;
  };
  const appURL = (reg, app, path) => (app.proxy
    ? `${schemeFor(reg, app)}://${location.hostname}:${app.proxy.port}${path || app.proxy.path || '/'}`
    : (path ? new URL(path, app.url).href : app.url));

  const registry = fetch(`${BASE}registry.json`, { cache: 'no-cache' }).then((r) => r.json());
  window.gadgets = {
    registry,
    // URL of another gadget (or one of its pages), e.g. gadgets.url('video-tools', '/jobs/j_1')
    async url(id, path) {
      const reg = await registry;
      const app = reg.apps.find((a) => a.id === id);
      return app ? appURL(reg, app, path) : null;
    },
  };

  const meta = document.querySelector('meta[name="gadget-nav"]');
  const pageMode = meta && meta.content;
  if (pageMode === 'off' || window.top !== window) return;
  // Several gadgets can share a port (one app, several pages): the one whose path we're on wins,
  // else the entry without a path.
  const currentApp = (apps) => {
    const here = apps.filter((a) => a.proxy && a.proxy.port === portNow());
    return here.find((a) => a.proxy.path && location.pathname.startsWith(a.proxy.path)) || here.find((a) => !a.proxy.path) || here[0] || null;
  };
  const portNow = () => Number(location.port || (location.protocol === 'https:' ? 443 : 80));

  function el(tag, attrs = {}, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'text') e.textContent = v;
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
      else e.setAttribute(k, v === true ? '' : v);
    }
    kids.flat().forEach((k) => k != null && e.append(k));
    return e;
  }

  const svg = (d) => {
    const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    s.setAttribute('viewBox', '0 0 24 24');
    s.setAttribute('aria-hidden', 'true');
    s.innerHTML = d; // fixed markup below, never data
    return s;
  };
  const ICON_MENU = '<path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none"/>';
  const ICON_GRID = '<g fill="currentColor"><rect x="4" y="4" width="7" height="7" rx="1.5"/><rect x="13" y="4" width="7" height="7" rx="1.5"/><rect x="4" y="13" width="7" height="7" rx="1.5"/><rect x="13" y="13" width="7" height="7" rx="1.5"/></g>';
  const ICON_X = '<path d="M6 6l12 12M18 6L6 18" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none"/>';
  const ICON_OUT = '<path d="M14 5h5v5M19 5l-8 8M17 14v4a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1h4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" fill="none"/>';

  function build(reg) {
    const isHome = !!document.querySelector('meta[name="gadget-home"]');
    const current = isHome ? null : currentApp(reg.apps);
    const mode = pageMode || (current && current.proxy && current.proxy.nav) || 'bar';
    if (mode === 'off') return;

    const host = el('gadget-nav');
    host.style.cssText = 'all: initial; display: contents;';
    const root = host.attachShadow({ mode: 'open' });
    root.append(el('link', { rel: 'stylesheet', href: `${BASE}nav.css` }));

    const home = homeURL(reg);
    const scrim = el('div', { class: 'scrim', hidden: true });
    const drawer = el('nav', { class: 'drawer', hidden: true, 'aria-label': 'Gadgets' });
    let opener = null;

    const open = () => {
      scrim.hidden = drawer.hidden = false;
      requestAnimationFrame(() => { scrim.classList.add('on'); drawer.classList.add('on'); });
      opener?.setAttribute('aria-expanded', 'true');
      drawer.querySelector('a')?.focus();
    };
    const close = () => {
      scrim.classList.remove('on'); drawer.classList.remove('on');
      opener?.setAttribute('aria-expanded', 'false');
      setTimeout(() => { scrim.hidden = drawer.hidden = true; }, 200);
    };
    scrim.addEventListener('click', close);
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !drawer.hidden) close(); });

    // drawer contents
    drawer.append(el('div', { class: 'dhead' },
      el('img', { src: `${BASE}icons/gadgets.svg`, alt: '' }),
      el('b', { text: reg.title || 'Gadgets' }),
      el('button', { class: 'icon', 'aria-label': 'Close menu', onclick: close }, svg(ICON_X)),
    ));
    drawer.append(el('a', { class: `item${isHome ? ' current' : ''}`, href: home },
      el('span', { class: 'glyph' }, svg(ICON_GRID)), el('span', { class: 'name', text: 'All gadgets & services' })));
    const groups = [['gadget', 'Gadgets'], ['service', 'Services']];
    for (const [kind, label] of groups) {
      const apps = reg.apps.filter((a) => a.kind === kind && appURL(reg, a));
      if (!apps.length) continue;
      drawer.append(el('div', { class: 'group', text: label }));
      for (const a of apps) {
        const external = !a.proxy;
        drawer.append(el('a', { class: `item${a === current ? ' current' : ''}`, href: appURL(reg, a), 'aria-current': a === current ? 'page' : null },
          el('img', { src: `${BASE}icons/${a.id}.svg`, alt: '' }),
          el('span', { class: 'name', text: a.name }),
          el('span', { class: 'host', text: (reg.hosts[a.host] && (reg.hosts[a.host].short || reg.hosts[a.host].name)) || '' }),
          external ? el('span', { class: 'out', title: 'Opens the app directly' }, svg(ICON_OUT)) : null,
        ));
      }
    }

    if (mode === 'fab') {
      opener = el('button', { class: 'fab', 'aria-label': 'Gadgets menu', 'aria-expanded': 'false', onclick: open },
        el('img', { src: `${BASE}icons/gadgets.svg`, alt: '' }));
      root.append(opener, scrim, drawer);
    } else {
      opener = el('button', { class: 'icon burger', 'aria-label': 'Open menu', 'aria-expanded': 'false', onclick: open }, svg(ICON_MENU));
      const title = current
        ? el('a', { class: 'brand', href: appURL(reg, current) }, el('img', { src: `${BASE}icons/${current.id}.svg`, alt: '' }), el('span', { text: current.name }))
        : el('a', { class: 'brand', href: home }, el('img', { src: `${BASE}icons/gadgets.svg`, alt: '' }), el('span', { text: reg.title || 'Gadgets' }));
      const bar = el('div', { class: 'bar' }, opener, title, el('span', { class: 'spacer' }),
        el('a', { class: 'icon', href: home, title: 'All gadgets', 'aria-label': 'All gadgets' }, svg(ICON_GRID)));
      root.append(bar, scrim, drawer);
      // make room: pages can use var(--gadget-nav-h) for full-height layouts
      document.documentElement.style.setProperty('--gadget-nav-h', `${BAR_H}px`);
      const pt = parseFloat(getComputedStyle(document.body).paddingTop) || 0;
      document.body.style.paddingTop = `${pt + BAR_H}px`;
    }
    document.body.append(host);
  }

  const start = () => registry.then(build).catch(() => {});
  if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
})();
