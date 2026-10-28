// Panel editor: move lines with ‹ ›, then change the number of panels (new frames, names kept).
// Usage: node test/count.e2e.js http://<pc>:8124 [outdir]
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2];
if (!BASE) { console.error('usage: node test/count.e2e.js http://<pc>:8124 [outdir]'); process.exit(2); }
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const problems = [];
const check = (ok, what) => { console.log(`${ok ? '✓' : '✗'} ${what}`); if (!ok) problems.push(what); };

(async () => {
  const r = await fetch(`${BASE}/api/panels`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: BASE },
    body: JSON.stringify({ media: 1293, from: 249583, to: 257375 }) });
  const res = await r.json();
  if (!r.ok) throw new Error(res.error);
  console.log('job', res.jobId, `${res.lines} lines`);

  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error') problems.push(`console: ${m.text()}`); });
  page.on('dialog', async (d) => { problems.push(`alert: ${d.message()}`); await d.dismiss(); });
  await page.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await page.goto(`${BASE}${res.url}`, { waitUntil: 'load' });
  await page.waitForSelector('#editor:not([hidden])', { timeout: 120000 });
  await sleep(600);

  const state = () => page.evaluate(() => ({
    job: new URLSearchParams(location.search).get('job'),
    frames: frames.length,
    count: document.querySelector('#panelCount').value,
    panels: lines.map((l) => l.panel),
    names: [...document.querySelectorAll('.pline .speaker')].map((e) => e.value),
    retakeHidden: document.querySelector('#retake').hidden,
  }));
  let s = await state();
  console.log('start', JSON.stringify(s));
  check(s.frames === res.lines && s.count === String(res.lines), 'starts with one panel per line, and the count shows it');
  check(s.retakeHidden, 'no "take new frames" button until the count changes');

  // names (neutral placeholders) so we can see they survive new frames
  const who = await page.$$('.pline .speaker');
  for (let i = 0; i < who.length; i++) await who[i].type(`Speaker ${String.fromCharCode(65 + i)}`);
  await page.keyboard.press('Tab');

  // ‹ › on the first line
  const moves = await page.$$('.pline .move');
  const [prev0, next0] = [await moves[0].$('button:first-child'), await moves[0].$('button:last-child')];
  check(await prev0.evaluate((b) => b.disabled), '‹ is off on the first panel');
  await next0.tap();
  s = await state();
  check(s.panels[0] === 1 && (await moves[0].$eval('select', (e) => e.value)) === '1', '› pushes line 1 to panel 2 (and the dropdown follows)');
  await prev0.tap();
  s = await state();
  check(s.panels[0] === 0, '‹ brings it back to panel 1');
  const lastMove = moves[moves.length - 1];
  check(await lastMove.$eval('button:last-child', (b) => b.disabled), '› is off on the last panel');
  await next0.tap();
  await sleep(300);
  await page.screenshot({ path: `${OUT}/c1-phone-moved.png`, fullPage: true });

  // fewer panels
  await page.evaluate(() => document.querySelector('#countDown').scrollIntoView({ block: 'center' }));
  await page.tap('#countDown');
  s = await state();
  const want = res.lines - 1;
  check(!s.retakeHidden && s.count === String(want), `− sets ${want} and shows the button`);
  check((await page.$eval('#retake', (b) => b.textContent)) === `Take ${want} new frames`, 'button says what it will do');
  await page.screenshot({ path: `${OUT}/c2-phone-count.png` });
  await Promise.all([page.waitForNavigation({ waitUntil: 'load', timeout: 60000 }), page.tap('#retake')]);
  await page.waitForSelector('#editor:not([hidden])', { timeout: 120000 });
  await sleep(600);
  const s2 = await state();
  console.log('fewer', JSON.stringify(s2));
  check(s2.job !== s.job, 'switched to a new job');
  check(s2.frames === want && s2.count === String(want), `${want} panels after taking new frames`);
  check(s2.names.join() === s.names.join(), 'names carried over');
  check(s2.panels.every((p) => p >= 0 && p < want), 'every line is on one of the panels');
  await page.screenshot({ path: `${OUT}/c3-phone-fewer.png`, fullPage: true });

  // more panels than lines, at desktop size
  await page.setViewport({ width: 1280, height: 900 });
  await page.waitForSelector('#editor:not([hidden])', { timeout: 120000 });
  await sleep(500);
  await page.evaluate((n) => { const i = document.querySelector('#panelCount'); i.value = n; i.dispatchEvent(new Event('input')); }, res.lines + 2);
  await Promise.all([page.waitForNavigation({ waitUntil: 'load', timeout: 60000 }), page.click('#retake')]);
  await page.waitForSelector('#editor:not([hidden])', { timeout: 120000 });
  await sleep(600);
  const s3 = await state();
  console.log('more', JSON.stringify(s3));
  check(s3.frames === res.lines + 2, `${res.lines + 2} panels (more than lines)`);
  check(s3.names.join() === s.names.join(), 'names still there');
  await page.evaluate(() => { document.querySelector('input[name=cols][value="3"]').click(); });
  await sleep(500);
  await page.screenshot({ path: `${OUT}/c4-desktop-more.png` });

  await browser.close();
  console.log(problems.length ? `\n${problems.length} problem(s):\n${problems.join('\n')}` : '\nall good');
  process.exit(problems.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
