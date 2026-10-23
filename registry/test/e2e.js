// Usage: node registry.e2e.js http://<pc> outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const HOST = process.argv[2], OUT = process.argv[3];
fs.mkdirSync(OUT, { recursive: true });
const errors = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(`${page.url()} ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) errors.push(`${page.url()} ${m.text()}`); });
  await page.setViewport({ width: 1280, height: 900 });
  await page.goto(HOST + '/', { waitUntil: 'load' });
  await page.waitForFunction(() => !document.querySelector('.label') || [...document.querySelectorAll('.label')].every((l) => l.textContent !== 'checking'), { timeout: 15000 });
  const statuses = await page.$$eval('.card', (cs) => cs.map((c) => `${c.querySelector('b').textContent}: ${c.querySelector('.label').textContent}`));
  await page.screenshot({ path: `${OUT}/r1-home.png`, fullPage: true });
  // open drawer
  await page.evaluate(() => document.querySelector('gadget-nav').shadowRoot.querySelector('.burger').click());
  await sleep(400);
  await page.screenshot({ path: `${OUT}/r2-drawer.png` });
  const out = { statuses };
  for (const [name, port] of [['stitch', 8101], ['drop', 8102], ['video', 8103]]) {
    await page.goto(`${HOST}:${port}/`, { waitUntil: 'networkidle2' });
    await sleep(600);
    out[name] = await page.evaluate(() => { const r = document.querySelector('gadget-nav')?.shadowRoot; return r ? r.querySelector('.brand span')?.textContent : 'NO NAV'; });
    await page.screenshot({ path: `${OUT}/r3-${name}.png` });
  }
  // phone home page + drawer
  await page.setViewport({ width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 });
  await page.goto(HOST + '/', { waitUntil: 'load' });
  await sleep(1500);
  await page.screenshot({ path: `${OUT}/r4-phone-home.png` });
  // offline page: port with no app behind it is not configured; check status endpoint for a down app
  out.jellyfinStatus = await page.evaluate(async () => (await fetch('/_gadgets/status/jellyfin')).status);
  console.log(JSON.stringify({ out, errors }, null, 1));
  await browser.close();
})().catch((e) => { console.error('FAILED', e); console.log(JSON.stringify({ errors })); process.exit(1); });
