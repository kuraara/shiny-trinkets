// Host desk tools in headless Chromium: drag, tidy, shuffle, auto, and the way back on a phone.
// Usage: node arrange.e2e.js http://host:port outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2] || 'http://localhost:8080';
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const errors = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const watch = (page, label) => {
  page.on('pageerror', (e) => errors.push(`${label} pageerror: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`${label} console: ${m.text()}`); });
};
const check = (ok, msg) => { if (!ok) { errors.push(msg); console.log('FAIL', msg); } else console.log('ok  ', msg); };

(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const host = await browser.newPage();
  watch(host, 'host');
  await host.setViewport({ width: 1280, height: 900 });
  await host.goto(BASE + '/', { waitUntil: 'load' });
  await host.type('input[name=title]', 'Arrange test');
  await Promise.all([host.waitForNavigation(), host.click('#start button[type=submit]')]);
  await host.waitForSelector('#code:not(:empty)');
  const code = await host.$eval('#code', (e) => e.textContent);
  const key = await host.evaluate((c) => JSON.parse(localStorage.getItem(`pd:host:${c}`)), code);
  const api = (path, opts = {}) => host.evaluate(async (url, o, k) => {
    const r = await fetch(url, { method: o.method || 'GET', headers: { 'X-Key': k, 'Content-Type': 'application/json' }, body: o.body ? JSON.stringify(o.body) : undefined });
    return r.status === 204 ? null : r.json();
  }, `/api/rooms/${code}${path}`, opts, key);

  // seven photos of different shapes and colours, uploaded by the host
  await host.evaluate(async (code, key) => {
    const sizes = [[800, 600], [600, 800], [900, 600], [700, 700], [800, 500], [600, 900], [1000, 750]];
    for (const [i, [w, h]] of sizes.entries()) {
      const c = document.createElement('canvas'); c.width = w; c.height = h;
      const g = c.getContext('2d'); g.fillStyle = `hsl(${i * 50} 70% 55%)`; g.fillRect(0, 0, w, h);
      g.fillStyle = '#fff'; g.font = '200px sans-serif'; g.fillText(String(i + 1), w / 2 - 60, h / 2 + 70);
      const blob = await new Promise((r) => c.toBlob(r, 'image/jpeg', 0.9));
      const fd = new FormData(); fd.append('photo', blob, `p${i}.jpg`);
      await fetch(`/api/rooms/${code}/photos`, { method: 'POST', headers: { 'X-Key': key }, body: fd });
    }
  }, code, key);
  await sleep(500);
  check(await host.$eval('#tidyDesk', (e) => !e.hidden), 'host page shows Tidy/Shuffle');

  // desk on a laptop, same browser as the host
  const desk = await browser.newPage();
  watch(desk, 'desk');
  await desk.setViewport({ width: 1600, height: 900 });
  await desk.goto(await host.$eval('#openDisplay', (e) => e.href), { waitUntil: 'load' });
  await desk.waitForFunction(() => document.querySelectorAll('#desk .print').length === 7);
  await sleep(800);
  check(await desk.$eval('#hostTools', (e) => !e.hidden), 'desk shows host tools');
  check(await desk.$eval('#back', (e) => !e.hidden && e.getAttribute('href').startsWith('/h/')), 'desk links back to host controls');
  await desk.screenshot({ path: `${OUT}/a1-desk.png` });

  // drag the bottom print to the top-left corner
  let s = await api('/state');
  const bottom = s.desk[0];
  const box = await desk.evaluate((src) => {
    const el = [...document.querySelectorAll('#desk .print')].find((p) => p.querySelector('img').src.includes(src));
    const r = el.getBoundingClientRect(); const x = r.x + r.width / 2, y = r.y + r.height / 2;
    const hit = document.elementFromPoint(x, y).closest('.print').querySelector('img').src; // may be covered by another
    return { x, y, id: hit.match(/photos\/([a-z0-9]+)/)[1] };
  }, bottom.id);
  await desk.mouse.move(box.x, box.y);
  await desk.mouse.down();
  for (let i = 1; i <= 10; i++) await desk.mouse.move(box.x - (box.x * i) / 10, box.y - (box.y * i) / 10);
  await desk.mouse.up();
  await sleep(600);
  check(!(await desk.$eval('#lightbox', (e) => !e.hidden)), 'drag does not open the lightbox');
  s = await api('/state');
  const moved = s.desk[s.desk.length - 1];
  check(moved.id === box.id && moved.x === 0 && moved.y === 0 && s.desk.length === 7, `dragged print saved at top-left, on top (${moved.id === box.id} ${moved.x} ${moved.y})`);
  await desk.screenshot({ path: `${OUT}/a2-dragged.png` });

  // a plain click still opens the lightbox
  const c = await desk.evaluate(() => { const r = [...document.querySelectorAll('#desk .print')].pop().getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
  await desk.mouse.click(c.x, c.y);
  await sleep(300);
  check(await desk.$eval('#lightbox', (e) => !e.hidden), 'click still opens the lightbox');
  await desk.click('#lightbox');

  await desk.click('#tidy');
  await sleep(1200);
  s = await api('/state');
  check(s.desk.every((p) => Math.abs(p.rot) <= 2), 'tidy straightens prints');
  await desk.screenshot({ path: `${OUT}/a3-tidy.png` });

  await desk.click('#shuffle');
  await sleep(1200);
  await desk.screenshot({ path: `${OUT}/a4-shuffle.png` });

  await desk.select('#auto', '5');
  await sleep(500);
  s = await api('/state');
  check(s.settings.autoArrange === 5, 'auto re-arrange saved from the desk');
  await host.bringToFront(); await sleep(300);
  check(await host.$eval('#autoArrange', (e) => e.value) === '5', 'host page shows the auto setting');
  await host.select('#autoStyle', 'tidy'); await sleep(300);
  s = await api('/state');
  check(s.settings.autoStyle === 'tidy', 'auto style saved from host page');
  await host.screenshot({ path: `${OUT}/a5-host.png`, fullPage: true });

  // iPhone-sized desk (touch): drag with a finger, then go back
  const phone = await browser.newPage();
  watch(phone, 'phone');
  await phone.setUserAgent('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1');
  await phone.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await phone.evaluateOnNewDocument(() => Object.defineProperty(Document.prototype, 'fullscreenEnabled', { get: () => false }));
  await phone.goto(await host.$eval('#openDisplay', (e) => e.href), { waitUntil: 'load' });
  await phone.waitForFunction(() => document.querySelectorAll('#desk .print').length === 7);
  await sleep(4000); // let the controls go idle
  check(await phone.$eval('#fs', (e) => e.hidden), 'no dead full-screen button on iPhone');
  const op = await phone.$eval('#controls', (e) => getComputedStyle(e).opacity);
  check(Number(op) > 0.3, `controls stay visible when idle on touch (${op})`);
  const overlap = await phone.evaluate(() => {
    const a = document.querySelector('#back').getBoundingClientRect(), b = document.querySelector('#hostTools').getBoundingClientRect();
    return a.right > b.left;
  });
  check(!overlap, 'back link and tools fit side by side at 390px');
  await phone.screenshot({ path: `${OUT}/a6-phone.png` });
  const t = await phone.evaluate(() => { const r = [...document.querySelectorAll('#desk .print')].pop().getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
  await phone.touchscreen.touchStart(t.x, t.y);
  for (let i = 1; i <= 8; i++) await phone.touchscreen.touchMove(t.x + i * 5, t.y + i * 12);
  await phone.touchscreen.touchEnd();
  await sleep(600);
  check(!(await phone.$eval('#lightbox', (e) => !e.hidden)), 'finger drag does not open the lightbox');
  await Promise.all([phone.waitForNavigation(), phone.tap('#back')]);
  check(phone.url().includes(`/h/${code}`), 'Back goes to the host controls');

  await api('', { method: 'DELETE' }); // end the test session
  await browser.close();
  console.log(errors.length ? `\n${errors.length} problem(s):\n${errors.join('\n')}` : '\nall good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
