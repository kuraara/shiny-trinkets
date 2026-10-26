// __NAME__ in headless Chromium (on the build box: NODE_PATH has puppeteer-core).
// Usage: node test/e2e.js https://<pc>:__PORT__ /tmp/shots
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2] || 'http://<pc>:__PORT__';
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });
(async () => {
  const errors = [];
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--ignore-certificate-errors'] });
  for (const [w, h, tag] of [[390, 844, 'phone'], [1280, 800, 'desktop']]) {
    const page = await browser.newPage();
    page.on('pageerror', (e) => errors.push(`${tag}: ${e.message}`));
    await page.setViewport({ width: w, height: h, isMobile: tag === 'phone', deviceScaleFactor: 2 });
    await page.goto(BASE + '/', { waitUntil: 'load' });
    await page.type('#who', 'Sam');
    await page.click('#go');
    await page.waitForFunction(() => document.querySelector('#out').textContent.includes('Sam'));
    await page.screenshot({ path: `${OUT}/__ID__-${tag}.png` });
  }
  await browser.close();
  console.log(errors.length ? `problems:\n${errors.join('\n')}` : 'all good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
