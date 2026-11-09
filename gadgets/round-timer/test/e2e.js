// Round Timer in headless Chromium (on the build box: NODE_PATH has puppeteer-core).
// Usage: node test/e2e.js http://<host>:8107 [outdir]
//
// Checks that "/" is round-timer.html byte for byte, then at 390 px and 1280 px, light and dark:
// the setup page loads with no script errors, a session starts, skipping through it reaches the
// stats page, and "I'm knackered" (two taps) ends a session early. Screenshots go to outdir.
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const path = require('path');
const BASE = process.argv[2];
if (!BASE) { console.error('usage: node test/e2e.js http://<host>:8107 [outdir]'); process.exit(2); }
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const errors = [];
  const want = fs.readFileSync(path.join(__dirname, '..', 'round-timer.html'));
  const got = Buffer.from(await (await fetch(BASE + '/')).arrayBuffer());
  if (!got.equals(want)) errors.push(`"/" differs from round-timer.html (${got.length} vs ${want.length} bytes)`);

  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--ignore-certificate-errors', '--autoplay-policy=no-user-gesture-required'] });
  for (const [w, h, tag] of [[390, 844, 'phone'], [1280, 800, 'desktop']]) {
    for (const scheme of ['light', 'dark']) {
      const name = `${tag}-${scheme}`;
      const page = await browser.newPage();
      page.on('pageerror', (e) => errors.push(`${name}: ${e.message}`));
      await page.setViewport({ width: w, height: h, isMobile: tag === 'phone', deviceScaleFactor: 2 });
      await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: scheme }]);
      await page.goto(BASE + '/', { waitUntil: 'load' });
      await page.screenshot({ path: `${OUT}/round-timer-${name}-setup.png` });

      await page.click('#startBtn');
      await page.waitForFunction(() => getComputedStyle(document.querySelector('#timer')).display !== 'none'
        && document.querySelector('#clock').textContent.trim() !== '');
      await page.screenshot({ path: `${OUT}/round-timer-${name}-running.png` });
      // skip through every section to the stats page
      for (let i = 0; i < 60; i++) {
        const done = await page.$eval('#done', (d) => getComputedStyle(d).display !== 'none').catch(() => false);
        if (done) break;
        await page.click('#skipBtn').catch(() => {});
        await new Promise((r) => setTimeout(r, 60));
      }
      const reached = await page.$eval('#done', (d) => getComputedStyle(d).display !== 'none').catch(() => false);
      if (!reached) errors.push(`${name}: skipping through didn't reach the stats page`);
      await page.screenshot({ path: `${OUT}/round-timer-${name}-stats.png` });
      await page.close();
    }
  }
  // "I'm knackered" ends a session early and shows the stats page
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(`knackered: ${e.message}`));
  await page.setViewport({ width: 390, height: 844, isMobile: true });
  await page.goto(BASE + '/', { waitUntil: 'load' });
  await page.click('#startBtn');
  await new Promise((r) => setTimeout(r, 300));
  // the first tap only arms it; a second tap within 3 s ends the session
  const shown = () => page.$eval('#done', (d) => getComputedStyle(d).display !== 'none').catch(() => false);
  await page.click('#knackeredBtn').catch(() => errors.push('knackered: no "I\'m knackered" button'));
  await new Promise((r) => setTimeout(r, 300));
  if (await shown()) errors.push('knackered: one tap ended the session (it should need two)');
  await page.click('#knackeredBtn').catch(() => {});
  await new Promise((r) => setTimeout(r, 300));
  if (!(await shown())) errors.push('knackered: two taps did not show the stats page');
  await browser.close();
  console.log(errors.length ? `problems:\n${errors.join('\n')}` : 'all good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
