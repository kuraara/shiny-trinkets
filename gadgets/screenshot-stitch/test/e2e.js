// Screenshot Stitch in headless Chromium (on the build box: NODE_PATH has puppeteer-core).
//   node test/e2e.js https://<pc>:8105 /tmp/shots [screenshot.png ...]
// 1. made-up screenshots of a made-up chat (clock changing, one saved smaller, all JPEG): the result
//    must be the chat with one set of bars;
// 2. the same with see-through bars (iOS 26: content shows under the title and around a floating
//    search pill), stitched with the bars marked by hand, and the marking lines dragged;
// 3. any real screenshots given: stitched and saved for a look; 4. the page at phone size.
const puppeteer = require('puppeteer-core');
const fs = require('fs');
const path = require('path');
const [BASE, OUT = '/tmp/shots', ...REAL] = process.argv.slice(2);
if (!BASE) { console.error('usage: node test/e2e.js https://<pc>:8105 [outdir] [screenshots…]'); process.exit(2); }
fs.mkdirSync(OUT, { recursive: true });
const problems = [];
const check = (ok, what) => { console.log(`${ok ? '✓' : '✗'} ${what}`); if (!ok) problems.push(what); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const brief = (p) => JSON.stringify({ height: p.height, joins: p.joins.map((j) => ({ ok: j.ok, hdr: j.hdr, ftr: j.ftr, overlap: j.overlap, score: j.score && +j.score.toFixed(2) })) });

async function planReady(page, n, extra = '() => true') {
  await page.waitForFunction((n, extra) => { const p = window.__stitch.plan; return p && p.segments.length === n && eval(extra)(p); },
    { timeout: 60000 }, n, extra);
  return page.evaluate(() => { const p = window.__stitch.plan; return { height: p.height, W0: p.W0, joins: p.joins }; });
}

// Makes three screenshots of a made-up chat in the page and adds them. glass: the bars are
// see-through, with the chat carrying on underneath them.
function makeChat(page, glass) {
  return page.evaluate(async (glass) => {
    const W = 780, PAGE = 3600, HDR = 150, FTR = 120, VIEW = 1100, tops = [0, 760, 1530];
    const words = 'the quick brown fox jumps over a lazy dog while seven bright gadgets stitch long screenshots into one tidy picture every morning'.split(' ');
    let seed = 3; const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
    const pg = document.createElement('canvas'); pg.width = W; pg.height = PAGE;
    const c = pg.getContext('2d');
    c.fillStyle = '#16171b'; c.fillRect(0, 0, W, PAGE);
    c.font = '28px sans-serif'; c.textBaseline = 'top';
    for (let y = 30; y < PAGE - 60;) {
      const bubble = rnd() < 0.3, lines = 1 + Math.floor(rnd() * 4);
      if (bubble) { c.fillStyle = '#2b2d33'; c.fillRect(260, y - 10, W - 290, lines * 40 + 16); }
      c.fillStyle = '#eceef2';
      for (let l = 0; l < lines; l++, y += 40) {
        let s = ''; while (s.length < (bubble ? 26 : 40)) s += words[Math.floor(rnd() * words.length)] + ' ';
        c.fillText(s.trim(), bubble ? 280 : 30, y);
      }
      y += 34;
    }
    // glass: the screenshot shows page rows [top, top + height); otherwise only the middle does
    const shot = (top, n, scale) => {
      const s = document.createElement('canvas'); s.width = W; s.height = HDR + VIEW + FTR;
      const x = s.getContext('2d');
      if (glass) {
        x.drawImage(pg, 0, top, W, s.height, 0, 0, W, s.height);
        const g = x.createLinearGradient(0, 0, 0, HDR); g.addColorStop(0, 'rgba(0,0,0,.92)'); g.addColorStop(1, 'rgba(0,0,0,.45)');
        x.fillStyle = g; x.fillRect(0, 0, W, HDR);
        const f = x.createLinearGradient(0, HDR + VIEW, 0, s.height); f.addColorStop(0, 'rgba(0,0,0,.4)'); f.addColorStop(1, 'rgba(0,0,0,.9)');
        x.fillStyle = f; x.fillRect(0, HDR + VIEW, W, FTR);
        x.fillStyle = '#26282d'; x.beginPath(); x.roundRect(40, HDR + VIEW + 30, W - 80, 64, 32); x.fill();
      } else {
        x.fillStyle = '#0d0e11'; x.fillRect(0, 0, W, s.height);
        x.drawImage(pg, 0, top, W, VIEW, 0, HDR, W, VIEW);
        x.fillStyle = '#2b2d33'; x.fillRect(30, HDR + VIEW + 25, W - 60, 70);
      }
      x.fillStyle = '#fff'; x.font = '600 26px sans-serif'; x.textBaseline = 'top';
      x.fillText(`14:4${n}`, 40, 18); x.fillText('100%', W - 110, 18);
      x.font = '600 30px sans-serif'; x.fillText(glass ? 'Settings' : 'Chat about gadgets', glass ? 330 : 220, 90);
      x.fillStyle = '#9aa0aa'; x.font = '26px sans-serif'; x.fillText(glass ? 'Search' : 'Reply…', 80, HDR + VIEW + 47);
      if (scale === 1) return s;
      const t = document.createElement('canvas'); t.width = Math.round(W * scale); t.height = Math.round(s.height * scale);
      t.getContext('2d').drawImage(s, 0, 0, t.width, t.height);
      return t;
    };
    const files = [];
    for (const [i, top] of tops.entries()) {
      const s = shot(top, i, i === 2 ? 0.95 : 1);
      const b = await new Promise((r) => s.toBlob(r, 'image/jpeg', 0.85));
      files.push(new File([b], `${glass ? 'settings' : 'chat'}-${i + 1}.jpg`, { type: 'image/jpeg' }));
    }
    await window.__stitch.addFiles(files);
    // on a made-up screenshot, chat row r sits at screenshot row r - top + HDR (both kinds)
    window.__truth = { pg, W, HDR, FTR, VIEW, tops, glass };
    return { expected: HDR + tops[2] + VIEW + FTR, HDR, FTR };
  }, glass);
}

// How far the stitched chat (between the bars) is from the made-up chat itself.
function compare(page) {
  return page.evaluate(async () => {
    const [f] = await window.__stitch.files();
    const img = new Image(); img.src = URL.createObjectURL(f); await img.decode();
    const T = window.__truth, H = img.naturalHeight;
    const got = document.createElement('canvas'); got.width = T.W; got.height = H;
    got.getContext('2d').drawImage(img, 0, 0);
    const want = document.createElement('canvas'); want.width = T.W; want.height = H;
    // the chat starts under the title bar on a normal screenshot, at the very top on a see-through one
    const sy = T.glass ? T.HDR : 0;
    want.getContext('2d').drawImage(T.pg, 0, sy, T.W, H - T.HDR - T.FTR, 0, T.HDR, T.W, H - T.HDR - T.FTR);
    const a = got.getContext('2d').getImageData(0, 0, T.W, H).data, b = want.getContext('2d').getImageData(0, 0, T.W, H).data;
    const rowDiff = (y) => {
      let d = 0;
      for (let x = 0; x < T.W; x += 2) { const i = (y * T.W + x) * 4; d += Math.abs(a[i] - b[i]); }
      return d / (T.W / 2);
    };
    let sum = 0, rows = 0;
    for (let y = T.HDR; y < H - T.FTR; y++) { sum += rowDiff(y); rows++; }
    // around each join: a seam a row or more out shows up here as text that doesn't line up.
    // (Single rows elsewhere can differ more: a screenshot saved smaller and scaled back is soft.)
    let r = 0; const seams = window.__stitch.plan.segments.map((g) => (r += g.y1 - g.y0) - (g.y1 - g.y0)).slice(1);
    const atSeams = seams.map((s) => { let t = 0; for (let y = s - 15; y <= s + 15; y++) t += rowDiff(y); return +(t / 31).toFixed(2); });
    return { mean: +(sum / rows).toFixed(2), seams, atSeams };
  });
}

(async () => {
  const browser = await puppeteer.launch({ executablePath: process.env.PUPPETEER_EXECUTABLE_PATH,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--ignore-certificate-errors'] });
  const watch = (pg, tag) => {
    pg.on('pageerror', (e) => problems.push(`${tag} page error: ${e.message}`));
    pg.on('console', (m) => { if (m.type() === 'error') problems.push(`${tag} console: ${m.text()}`); });
  };

  // ---- 1. made-up chat, bars found automatically
  const page = await browser.newPage();
  watch(page, 'chat');
  await page.setViewport({ width: 1280, height: 900 });
  await page.goto(`${BASE}/`, { waitUntil: 'load' });
  const t1 = await makeChat(page, false);
  const p = await planReady(page, 3);
  console.log('made-up:', brief(p));
  check(p.joins.every((j) => j.ok), 'both joins found');
  check(Math.abs(p.height - t1.expected) <= 3, `height ${p.height} ≈ ${t1.expected} (one set of bars, nothing doubled or lost)`);
  const d1 = await compare(page);
  console.log('  vs the chat itself:', JSON.stringify(d1));
  check(d1.mean < 4 && d1.atSeams.every((v) => v < 6), 'the stitched chat matches the original, joins included (JPEG noise only)');
  await page.click('#keepBottom');
  const q = await planReady(page, 3, `(p) => p.height !== ${p.height}`);
  check(q.height === p.height - p.joins[1].ftr, 'leaving off the bottom bar takes off just that');
  await page.click('#keepBottom');
  await page.click('#showJoins');
  await sleep(300);
  await page.screenshot({ path: `${OUT}/s1-desktop-madeup.png` });

  // ---- 2. see-through bars, marked by hand
  const glass = await browser.newPage();
  watch(glass, 'glass');
  await glass.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await glass.goto(`${BASE}/`, { waitUntil: 'load' });
  const t2 = await makeChat(glass, true);
  const g = await planReady(glass, 3);
  console.log('see-through, found automatically:', brief(g));
  await glass.click('input[name=bars][value=mark]');
  await glass.waitForSelector('#marker:not([hidden])');
  const set = (id, v) => glass.evaluate((id, v) => { const e = document.querySelector(id); e.value = v; e.dispatchEvent(new Event('change')); }, id, v);
  await set('#topPx', t2.HDR);
  await set('#bottomPx', t2.FTR);
  const m = await planReady(glass, 3, `(p) => p.joins.every((j) => j.hdr === ${t2.HDR} && j.ftr === ${t2.FTR})`);
  console.log('see-through, marked:', brief(m));
  check(m.joins.every((j) => j.ok), 'with the bars marked, both joins found');
  check(Math.abs(m.height - t2.expected) <= 3, `height ${m.height} ≈ ${t2.expected}`);
  const d2 = await compare(glass);
  console.log('  vs the chat itself:', JSON.stringify(d2));
  check(d2.mean < 4 && d2.atSeams.every((v) => v < 6), 'the stitched chat matches the original, joins included: nothing from under the bars got in');
  // dragging the top line down on the screenshot
  await glass.evaluate(() => document.querySelector('#markCanvas').scrollIntoView({ block: 'center' }));
  const box = await (await glass.$('#markCanvas')).boundingBox();
  const lineY = box.y + (t2.HDR / (t2.HDR + 1100 + t2.FTR)) * box.height;
  await glass.mouse.move(box.x + box.width / 2, lineY);
  await glass.mouse.down();
  await glass.mouse.move(box.x + box.width / 2, lineY + 40, { steps: 5 });
  await glass.mouse.up();
  const dragged = await glass.evaluate(() => window.__stitch.bars.top);
  check(dragged > t2.HDR + 40 && dragged < t2.HDR + 160, `dragging the top line moved it (${t2.HDR} → ${dragged})`);
  await glass.screenshot({ path: `${OUT}/s4-phone-marking.png`, fullPage: true });
  await set('#topPx', t2.HDR);

  // ---- 3. real screenshots
  if (REAL.length) {
    const real = await browser.newPage();
    watch(real, 'real');
    await real.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 3 });
    await real.goto(`${BASE}/`, { waitUntil: 'load' });
    await (await real.$('#pick')).uploadFile(...REAL);
    const r = await planReady(real, REAL.length);
    console.log('real:', brief(r));
    check(r.joins.every((j) => j.ok), 'real screenshots: every join found');
    const data = await real.evaluate(async () => {
      const [f] = await window.__stitch.files();
      return new Promise((res) => { const rd = new FileReader(); rd.onload = () => res(rd.result.split(',')[1]); rd.readAsDataURL(f); });
    });
    fs.writeFileSync(path.join(OUT, 'real-stitched.png'), Buffer.from(data, 'base64'));
    await real.screenshot({ path: `${OUT}/s2-phone-real.png`, fullPage: true });
    await real.close();
  }

  // ---- 4. phone layout
  const phone = await browser.newPage();
  await phone.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  await phone.goto(`${BASE}/`, { waitUntil: 'load' });
  await phone.screenshot({ path: `${OUT}/s3-phone-empty.png` });
  check(!(await phone.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)), 'no sideways scrolling on a phone');

  await browser.close();
  console.log(problems.length ? `\n${problems.length} problem(s):\n${problems.join('\n')}` : '\nall good');
  process.exit(problems.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
