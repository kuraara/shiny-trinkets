// Ask Me in headless Chromium (on the build box: NODE_PATH has puppeteer-core).
// Usage: node test/e2e.js http://<pi>:8108 [outdir]
//
// Asks a question over MCP, checks the page shows it at 390 px and 1280 px in light and dark,
// answers it by tapping a suggestion, and checks the waiting agent gets that answer. A second
// question is answered by typing. Both are real questions on the live page, marked as tests.
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2];
if (!BASE) { console.error('usage: node test/e2e.js http://<pi>:8108 [outdir]'); process.exit(2); }
const OUT = process.argv[3] || '/tmp/shots';
fs.mkdirSync(OUT, { recursive: true });

async function mcp(name, args) {
  const r = await fetch(BASE + '/mcp', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }) });
  return JSON.parse((await r.json()).result.content[0].text);
}

(async () => {
  const errors = [];
  const q1 = await mcp('ask_user', { question: '(test) Ship the new page today?', options: ['Yes', 'Not yet'],
    context: 'An automated check of the Ask Me page. It answers itself.', from: 'e2e test', wait_seconds: 0 });
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  for (const [w, h, tag] of [[390, 844, 'phone'], [1280, 800, 'desktop']]) {
    for (const scheme of ['light', 'dark']) {
      const page = await browser.newPage();
      page.on('pageerror', (e) => errors.push(`${tag}-${scheme}: ${e.message}`));
      await page.setViewport({ width: w, height: h, isMobile: tag === 'phone', deviceScaleFactor: 2 });
      await page.emulateMediaFeatures([{ name: 'prefers-color-scheme', value: scheme }]);
      await page.goto(BASE + '/', { waitUntil: 'networkidle0' });
      const shown = await page.$$eval('.q .text', (ps) => ps.map((p) => p.textContent));
      if (!shown.includes(q1.question)) errors.push(`${tag}-${scheme}: question not on the page`);
      const wide = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      if (wide) errors.push(`${tag}-${scheme}: page scrolls sideways`);
      await page.screenshot({ path: `${OUT}/ask-me-${tag}-${scheme}.png`, fullPage: true });
      await page.close();
    }
  }
  // tap a suggestion while the "agent" waits
  const waiting = mcp('get_answer', { id: q1.id, wait_seconds: 20 });
  const page = await browser.newPage();
  page.on('pageerror', (e) => errors.push(`answer: ${e.message}`));
  await page.setViewport({ width: 390, height: 844, isMobile: true });
  await page.goto(BASE + '/', { waitUntil: 'networkidle0' });
  const [btn] = await page.$$('xpath/.//button[contains(@class,"opt") and text()="Not yet"]');
  if (!btn) errors.push('no "Not yet" button'); else await btn.click();
  const got = await waiting;
  if (got.status !== 'answered' || got.answer !== 'Not yet') errors.push(`agent got ${JSON.stringify(got)}`);
  // type an answer to a second question
  const q2 = await mcp('ask_user', { question: '(test) Which colour?', from: 'e2e test', wait_seconds: 0 });
  await page.reload({ waitUntil: 'networkidle0' });
  await page.type(`#answer-${q2.id}`, 'Green, please');
  await page.click(`#answer-${q2.id} ~ .row .send`);
  const got2 = await mcp('get_answer', { id: q2.id, wait_seconds: 10 });
  if (got2.answer !== 'Green, please') errors.push(`typed answer: ${JSON.stringify(got2)}`);
  await new Promise((r) => setTimeout(r, 6000)); // let the page refresh into the log
  await page.screenshot({ path: `${OUT}/ask-me-answered.png`, fullPage: true });
  await browser.close();
  console.log(errors.length ? `problems:\n${errors.join('\n')}` : 'all good');
  process.exit(errors.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
