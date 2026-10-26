// Make a clip from a quote at phone size and check it shows, plays and can be downloaded.
// Usage: node clip.e2e.js http://pc outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const PC = process.argv[2];
if (!PC) { console.error('usage: node test/clip.e2e.js http://<pc> [outdir]'); process.exit(2); }
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  const errors = [];
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage', '--autoplay-policy=no-user-gesture-required', '--ignore-certificate-errors'] });
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(`quote finder: ${e.message}`));
  await page.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await page.goto(`${PC}:8104/?q=melting+pearls&t=American+Dad!`, { waitUntil: 'load' });
  await page.waitForSelector('.hit .actions button:last-child');
  const card = (await page.$$('li.hit'))[0];
  await (await card.$('.actions button:last-child')).tap();
  await page.waitForSelector('.lines.picking');
  await sleep(1200);
  const idx = await card.$$eval('.line', (ls) => ls.findIndex((l) => l.textContent.includes('fingers are still sticky')));
  const hit = await card.$$eval('.line', (ls) => ls.findIndex((l) => l.textContent.includes('melting pearls')));
  await (await card.$$('.line'))[idx].tap();
  await (await card.$$('.line'))[hit].tap();
  await (await card.$$('.pick-btns .primary'))[1].tap();
  await page.waitForSelector('.clipbox progress', { timeout: 30000 });
  await page.screenshot({ path: `${OUT}/c1-making.png` });
  await page.waitForSelector('.clipbox video.clip', { timeout: 120000 });
  const v = await page.waitForFunction(() => {
    const v = document.querySelector('video.clip');
    return v && v.readyState >= 1 && v.duration > 0 ? { d: v.duration, w: v.videoWidth, h: v.videoHeight, src: v.currentSrc } : null;
  }, { timeout: 30000 }).then((h) => h.jsonValue());
  console.log('video in quote finder:', JSON.stringify(v));
  const dl = await page.$eval('.clip-actions a', (a) => ({ text: a.textContent, href: a.href, download: a.download }));
  console.log('download link:', JSON.stringify(dl));
  console.log('secure context:', await page.evaluate(() => window.isSecureContext), 'share API:', await page.evaluate(() => typeof navigator.share));
  await page.$eval('video.clip', (v) => v.scrollIntoView({ block: 'center' }));
  await sleep(500);
  await page.screenshot({ path: `${OUT}/c2-clip-ready.png` });
  // the same clip on the video-tools page
  const vt = await browser.newPage();
  vt.on('pageerror', (e) => errors.push(`video tools: ${e.message}`));
  await vt.setViewport({ width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 });
  const jobId = v.src.match(/jobs\/(j_[a-z0-9]+)/)[1];
  await vt.goto(`${PC}:8103/jobs/${jobId}`, { waitUntil: 'load' });
  const vt2 = await vt.waitForFunction(() => {
    const v = document.querySelector('video.player');
    return v && v.readyState >= 1 ? { d: v.duration, dl: document.querySelector('.actions a[download]')?.textContent } : null;
  }, { timeout: 30000 }).then((h) => h.jsonValue());
  console.log('video tools page:', JSON.stringify(vt2));
  await vt.screenshot({ path: `${OUT}/c3-videotools.png`, fullPage: true });
  if (!(v.d > 5 && v.w > 0)) errors.push('clip did not load in quote finder');
  if (!(vt2.d > 5)) errors.push('clip did not load on video tools page');
  await browser.close();
  console.log(`JOB ${jobId}`);
  console.log(errors.length ? `problems:\n${errors.join('\n')}` : 'all good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
