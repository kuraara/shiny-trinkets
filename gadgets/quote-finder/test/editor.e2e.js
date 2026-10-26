// Panel editor + registry views in headless Chromium. Usage: node editor.e2e.js http://pc outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const PC = process.argv[2];
if (!PC) { console.error('usage: node test/editor.e2e.js http://<pc> [outdir]'); process.exit(2); }
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  const errors = [];
  const r = await fetch(`${PC}:8104/api/panels`, { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ media: 1293, from: 249583, to: 257375 }) });
  const res = await r.json();
  if (!r.ok) throw new Error(res.error);
  console.log('job', res.jobId, res.url);
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(`editor: ${e.message} ${e.stack}`));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`editor console: ${m.text()}`); });
  await page.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await page.goto(`${PC}:8104${res.url}`, { waitUntil: 'load' });
  await page.screenshot({ path: `${OUT}/e0-waiting.png` });
  await page.waitForSelector('#editor:not([hidden])', { timeout: 90000 });
  await sleep(800);
  await page.screenshot({ path: `${OUT}/e1-phone.png` });
  const names = ['Roger', 'Roger', 'Roger', 'Roger'];
  const inputs = await page.$$('.pline .speaker');
  for (let i = 0; i < inputs.length; i++) { await inputs[i].type(names[i] || 'Speaker'); }
  await sleep(500);
  await page.screenshot({ path: `${OUT}/e2-phone-named.png`, fullPage: true });
  // defaults: no panel numbers, one column
  const def = await page.evaluate(() => ({ labels: document.querySelector('#labels').checked, cols: opts().cols }));
  if (def.labels || def.cols !== '1') errors.push(`defaults ${JSON.stringify(def)}`);
  const full = await page.evaluate(async () => { const f = await exportBlob(); const L = plan(opts()); return { size: f.size, type: f.type, name: f.name, W: L.W, H: L.H }; });
  console.log('export default', JSON.stringify(full));
  // a 2-column square layout with numbers, 1080 wide, px spacing
  await page.setViewport({ width: 1280, height: 900 }); // leaving mobile emulation reloads the page
  await page.waitForSelector('#editor:not([hidden])', { timeout: 90000 });
  await sleep(500);
  await page.evaluate(() => {
    document.querySelector('input[name=cols][value="2"]').click();
    const set = (id, v) => { const e = document.querySelector(id); e.value = v; e.dispatchEvent(new Event('change', { bubbles: true })); };
    set('#ratio', '1'); set('#size', '1080');
    document.querySelector('#labels').click();
    document.querySelector('input[name=unit][value="px"]').click();
  });
  await sleep(800);
  const sq = await page.evaluate(async () => { const f = await exportBlob(); const L = plan(opts()); return { size: f.size, W: L.W, H: L.H, gap: document.querySelector('#gap').value, note: document.querySelector('#sizeNote').textContent }; });
  console.log('export square', JSON.stringify(sq));
  if (sq.gap !== String(Math.round(0.021 * 1080 / 2.07))) console.log('gap after unit switch', sq.gap);
  if (sq.W !== 1080 || Math.abs(sq.W - sq.H) > 2) errors.push(`square layout ${JSON.stringify(sq)}`);
  await page.screenshot({ path: `${OUT}/e3-desktop-square.png` });
  await page.click('#resetLayout'); await sleep(300);

  // registry: cards and tiles
  const reg = await browser.newPage();
  reg.on('pageerror', (e) => errors.push(`registry: ${e.message}`));
  for (const [w, h, tag] of [[1280, 1000, 'desk'], [390, 844, 'phone']]) {
    await reg.setViewport({ width: w, height: h, deviceScaleFactor: tag === 'phone' ? 2 : 1, isMobile: tag === 'phone' });
    await reg.goto(`${PC}/`, { waitUntil: 'load' });
    await reg.evaluate(() => document.querySelector('input[name=view][value="cards"]').click());
    await sleep(1500);
    await reg.screenshot({ path: `${OUT}/r-${tag}-cards.png` });
    await reg.evaluate(() => document.querySelector('input[name=view][value="tiles"]').click());
    await sleep(300);
    await reg.screenshot({ path: `${OUT}/r-${tag}-tiles.png` });
  }
  const links = await reg.$$eval('#gadget a.card', (as) => as.map((a) => a.href));
  console.log('gadget links', links.join(' '));
  // the navbar on a split page names the right gadget
  const vt = await browser.newPage();
  await vt.setViewport({ width: 1280, height: 800 });
  await vt.goto(`${PC}:8103/clip`, { waitUntil: 'load' });
  await sleep(1200);
  const brand = await vt.evaluate(() => document.querySelector('gadget-nav')?.shadowRoot?.querySelector('.brand span')?.textContent);
  console.log('nav brand on /clip:', brand);
  if (brand !== 'Cut a Clip') errors.push(`nav brand ${brand}`);
  await vt.screenshot({ path: `${OUT}/v-clip-page.png` });
  await browser.close();
  console.log(errors.length ? `problems:\n${errors.join('\n')}` : 'all good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
