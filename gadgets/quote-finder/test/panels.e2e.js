// "Make panels" in headless Chromium at phone size. Usage: node panels.e2e.js http://host:port outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2] || 'http://localhost:8080';
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  const errors = [];
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(e.message));
  await page.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: 'dark' }]);
  await page.goto(`${BASE}/?q=melting+pearls&t=American+Dad!`, { waitUntil: 'load' });
  await page.waitForSelector('.hit .actions button:nth-child(2)');
  const card = (await page.$$('li.hit'))[0];
  await (await card.$('.actions button:nth-child(2)')).tap();
  await page.waitForSelector('.lines.picking');
  await sleep(1200); // let the smooth scroll to the quote finish
  const idx = await card.$$eval('.line', (ls) => ls.findIndex((l) => l.textContent.includes('fingers are still sticky')));
  const hit = await card.$$eval('.line', (ls) => ls.findIndex((l) => l.textContent.includes('melting pearls')));
  await (await card.$$('.line'))[idx].tap();
  await (await card.$$('.line'))[hit].tap();
  const text = await card.$eval('.pick-text', (e) => e.textContent);
  console.log('bar:', text);
  await (await card.$$('.line'))[hit].scrollIntoView();
  await page.screenshot({ path: `${OUT}/q1-picked.png` });
  await (await card.$('.pickbar .primary')).tap();
  await page.waitForSelector('.pickbar.done a', { timeout: 60000 });
  const href = await card.$eval('.pickbar.done a', (a) => a.href);
  console.log('link:', href);
  await page.screenshot({ path: `${OUT}/q2-sent.png` });
  // and a clip with subtitles from the same lines
  await (await card.$('.actions button:last-child')).tap();
  await page.waitForSelector('.lines.picking');
  await sleep(1200); // let the smooth scroll to the quote finish
  await (await card.$$('.line'))[idx].tap();
  await (await card.$$('.line'))[hit].tap();
  await (await card.$('.pick-btns .subs input')).tap();
  await page.screenshot({ path: `${OUT}/q4-clip-bar.png` });
  await (await card.$$('.pick-btns .primary'))[1].tap();
  await page.waitForFunction(() => document.querySelectorAll('.pickbar.done a').length === 2, { timeout: 60000 });
  const clipHref = (await card.$$eval('.pickbar.done a', (as) => as.map((a) => a.href)))[0];
  console.log('clip link:', clipHref);
  const jobId = clipHref.split('/').pop();
  const api = new URL(clipHref).origin.replace(':8103', ':8090');
  let job;
  for (let i = 0; i < 60; i++) {
    job = await (await fetch(`${api}/api/jobs/${jobId}`)).json();
    if (job.status !== 'queued' && job.status !== 'running') break;
    await sleep(1000);
  }
  console.log('clip job:', job.status, job.error || '', (job.outputs || []).map((o) => `${o.name} ${o.size}`).join(', '), (job.notes || []).join(' | '));
  if (job.status !== 'done') errors.push('clip job ' + job.status);
  const tools = await browser.newPage();
  await tools.setViewport({ width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 });
  await tools.goto(href, { waitUntil: 'load' });
  await sleep(1500);
  await tools.screenshot({ path: `${OUT}/q3-panel-review.png` });
  await tools.goto(clipHref, { waitUntil: 'load' });
  await sleep(1500);
  await tools.screenshot({ path: `${OUT}/q5-clip-job.png` });
  await browser.close();
  console.log(errors.length ? `errors: ${errors.join('; ')}` : 'all good');
  process.exit(errors.length || !text.startsWith('4 lines') ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
