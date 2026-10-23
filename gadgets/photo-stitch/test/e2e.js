// Usage: node photo-stitch.e2e.js http://host:port outdir
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const BASE = process.argv[2], OUT = process.argv[3];
fs.mkdirSync(OUT, { recursive: true });
const errors = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const page = await browser.newPage();
  page.on('console', (m) => { if (['error', 'warning'].includes(m.type())) errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(e.message));
  await page.setViewport({ width: 1280, height: 900 });
  await page.goto(BASE + '/', { waitUntil: 'networkidle0' });
  await page.screenshot({ path: `${OUT}/s1-empty.png` });
  // test pics: a1 1600x1200, a2 1000x1500, a3 1800x1200
  const input = await page.$('#pick');
  await input.uploadFile('/tmp/testpics/a1.jpg', '/tmp/testpics/a2.jpg', '/tmp/testpics/a3.jpg');
  await page.waitForFunction(() => window.__stitch.items.length === 3);
  await sleep(300);
  const L = () => page.evaluate(() => window.__stitch.layout());
  const res = {};
  res.verticalAsIs = await L();                      // expect 1800 x 3900
  await page.screenshot({ path: `${OUT}/s2-vertical.png` });
  await page.click('input[name=layout][value=horizontal]'); await sleep(200);
  res.horizontalAsIs = await L();                    // expect 4400 x 1500
  await page.click('input[name=layout][value=grid]'); await sleep(200);
  await page.click('input[name=fit][value=same]'); await sleep(200);
  res.grid2Same = await L();                         // 2x2, ratio of first (4:3); crops cw: 1600,1000,1600 -> median 1600 -> tiles 1600x1200 -> 3200 x 2400
  await page.screenshot({ path: `${OUT}/s3-grid-same.png` });
  await page.$eval('#gap', (e) => { e.value = 40; e.dispatchEvent(new Event('input', { bubbles: true })); });
  await page.select('#ratio', '1'); await sleep(200);
  res.grid2SquareGap = await L();                    // crops: 1200,1000,1200 -> 1200 tiles -> 2*1200+40 = 2440 square
  // reorder: move item 3 to the top
  await page.click('#list li:nth-child(3) button[title="Move earlier"]');
  await page.click('#list li:nth-child(2) button[title="Move earlier"]');
  res.order = await page.evaluate(() => window.__stitch.items.map((i) => `${i.w}x${i.h}`));
  await page.click('input[name=layout][value=vertical]'); await sleep(300);
  await page.screenshot({ path: `${OUT}/s4-vertical-square.png` });
  // download works
  const client = await page.createCDPSession();
  await client.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: OUT });
  await page.click('#saveBtn'); await sleep(2500);
  res.downloads = fs.readdirSync(OUT).filter((f) => f.startsWith('stitched-'));
  // borders, margin and output size (sizes are in final-image pixels)
  const setVal = (sel, v) => page.$eval(sel, (e, v) => { e.value = v; e.dispatchEvent(new Event('input', { bubbles: true })); }, v);
  await page.click('input[name=fit][value=asis]');
  await setVal('#gap', 0); await setVal('#border', 20); await sleep(200);
  res.border20 = await L();                          // order a3,a1,a2: W 1800+40, H 3900+6*20 = 1840 x 4020
  await setVal('#margin', 30); await sleep(200);
  res.border20margin30 = await L();                  // 1900 x 4080
  await setVal('#margin', 0);
  await page.select('#size', '1080'); await sleep(200);
  res.size1080 = await L();                          // longest side exactly 1080, border still 20
  await page.screenshot({ path: `${OUT}/s4b-borders.png` });
  await page.select('#size', 'custom'); await setVal('#sizePx', 500); await sleep(200);
  res.custom500 = await L();
  await page.select('#format', 'jpeg'); await sleep(200);
  res.qualityShown = await page.$eval('#qualityRow', (e) => !e.hidden);
  await page.select('#size', 'full'); await page.select('#format', 'png');
  // percent units: switching converts values so the image keeps its size
  await setVal('#border', 20); await sleep(200);
  const beforePct = await L();
  await page.click('input[name=unit][value=pct]'); await sleep(300);
  res.pctAfterSwitch = { ...(await L()), border: await page.$eval('#border', (e) => e.value), label: await page.$eval('#borderOut', (e) => e.textContent) };
  res.pctSameSize = Math.abs(res.pctAfterSwitch.H - beforePct.H) <= 6 * 0.05 * res.pctAfterSwitch.u + 1;
  // in %, borders shrink with the output: at 1080 px the border is still 1.5% of a photo
  await setVal('#border', 2); await sleep(200);
  const full = await L();
  await page.select('#size', '1080'); await sleep(200);
  const small = await L();
  res.pctScales = { fullU: full.u, smallU: small.u, ratioMatches: Math.abs(small.u / full.u - small.s) < 1e-6 };
  await page.screenshot({ path: `${OUT}/s4c-percent.png` });
  await page.click('input[name=unit][value=px]'); await page.select('#size', 'full'); await sleep(200);
  // phone layout
  await page.setViewport({ width: 390, height: 844, isMobile: true, deviceScaleFactor: 2 });
  await sleep(300);
  await page.screenshot({ path: `${OUT}/s5-phone.png`, fullPage: true });
  console.log(JSON.stringify({ res, errors }, null, 1));
  await browser.close();
})().catch((e) => { console.error('FAILED', e); console.log(JSON.stringify({ errors })); process.exit(1); });
